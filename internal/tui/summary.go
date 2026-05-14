package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/classify"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/skills"
	"github.com/ringo380/ccmcp/internal/stringslice"
)

// summaryView is the "Summary" tab: bird's-eye overview of every scope, plus
// a redundancies section that flags duplicated MCPs, installed-but-disabled
// plugins, and other inconsistencies worth knowing about.
//
// As of v0.10, fixable issues are cursor-selectable. Keys mirror Doctor:
//
//	j/k    cursor over fixable rows (display rows are skipped)
//	l      run an LLM review on the selected issue
//	f      open a fix-confirmation panel for the selected issue
//	y/n    approve / cancel
//	p      legacy bulk orphan-prune (kept for muscle-memory; superseded by f)
type summaryView struct {
	st   *state
	w, h int
	top  int

	rows   []summaryRow // rebuilt each render; cursor indexes the fixable subset
	cursor int          // index into the *fixable* subset, NOT raw rows

	pendingPrune bool
	flash        string

	// Pre-apply / post-apply fix gates — same semantics as doctorView.
	pendingFix    *fixProposal
	postReview    *fixProposal
	previewBody   string // styled multi-line body shown in the confirm panel
	previewScroll int

	// LLM review state (per-row review; only the selected row is sent).
	llmRunning bool
	llmResult  string // last review body
	llmFor     summaryRow

	// In-flight CLI fix state.
	fixRunning   bool
	fixStartedAt time.Time
	fixTarget    string
	fixOutput    []byte

	// claudeOnPath is cached at view init. When false, the keys gated on the
	// claude CLI (`l` review and `f` fix for fixClaudeCLI proposals) surface a
	// friendly hint instead of attempting to spawn. fixInMemory proposals are
	// unaffected and remain usable offline.
	claudeOnPath bool
}

// summaryReviewMsg is delivered when an `l` review finishes.
type summaryReviewMsg struct {
	row     summaryRow
	content string
	err     error
}

func newSummaryView(st *state) *summaryView {
	v := &summaryView{st: st}
	if _, err := exec.LookPath("claude"); err == nil {
		v.claudeOnPath = true
	}
	return v
}

func (v *summaryView) update(msg tea.Msg) tea.Cmd {
	if done, ok := msg.(fixDoneMsg); ok && done.origin == tabSummary {
		v.fixRunning = false
		v.fixOutput = done.output
		if done.err != nil {
			v.flash = styleErr.Render("fix failed: " + enrichExitStatus(done.err.Error()))
			if tail := tailOutput(done.output, 12); tail != "" {
				v.flash += "\n" + styleDim.Render(tail)
			}
			return nil
		}
		if done.proposal != nil && done.proposal.kind == fixClaudeCLI {
			after, err := os.ReadFile(done.proposal.target)
			if err != nil {
				v.flash = styleErr.Render(fmt.Sprintf(
					"read post-fix %s: %s — snapshot kept at %s",
					filepath.Base(done.proposal.target), err.Error(), done.proposal.snapshotPath,
				))
				return nil
			}
			diff := unifiedDiff(string(done.proposal.beforeBytes), string(after), 3)
			if diff == "" {
				deleteSnapshot(done.proposal.snapshotPath)
				v.flash = styleWarn.Render("claude CLI made no changes")
				return nil
			}
			v.postReview = done.proposal
			v.previewBody = diff
			v.previewScroll = 0
			v.flash = ""
			return nil
		}
		v.flash = styleOK.Render("fix applied")
		return nil
	}

	if review, ok := msg.(summaryReviewMsg); ok {
		v.llmRunning = false
		if review.err != nil {
			v.flash = styleErr.Render("LLM review failed: " + review.err.Error())
			return nil
		}
		v.llmResult = review.content
		v.llmFor = review.row
		v.flash = ""
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	// Post-apply review intercepts all keys.
	if v.postReview != nil {
		switch key.String() {
		case "y":
			deleteSnapshot(v.postReview.snapshotPath)
			v.flash = styleOK.Render("kept: " + v.postReview.summary)
			v.postReview = nil
			v.previewBody = ""
			v.previewScroll = 0
		case "u", "n", "esc":
			if err := v.revertFromSnapshot(v.postReview); err != nil {
				v.flash = styleErr.Render("revert failed: " + err.Error())
			} else {
				deleteSnapshot(v.postReview.snapshotPath)
				v.flash = styleOK.Render("reverted: " + v.postReview.summary)
			}
			v.postReview = nil
			v.previewBody = ""
			v.previewScroll = 0
		case "j", "down":
			v.previewScroll++
		case "k", "up":
			if v.previewScroll > 0 {
				v.previewScroll--
			}
		}
		return nil
	}

	// Pre-apply confirm intercepts all keys.
	if v.pendingFix != nil {
		switch key.String() {
		case "y":
			return v.executeFix()
		case "n", "esc":
			v.pendingFix = nil
			v.previewBody = ""
			v.previewScroll = 0
		case "j", "down":
			v.previewScroll++
		case "k", "up":
			if v.previewScroll > 0 {
				v.previewScroll--
			}
		}
		return nil
	}

	// Clear the LLM result on any key other than 'l' (so it auto-dismisses).
	if v.llmResult != "" && key.String() != "l" && key.String() != "j" && key.String() != "k" &&
		key.String() != "down" && key.String() != "up" && key.String() != "pgdn" && key.String() != "pgup" {
		v.llmResult = ""
		v.llmFor = summaryRow{}
	}

	// Clear pending prune on any key other than 'p'.
	if v.pendingPrune && key.String() != "p" {
		v.pendingPrune = false
	}

	// Rebuild rows up-front so cursor navigation + fixable lookups work even
	// before the first render() call (e.g. tests pressing f immediately after
	// switching tabs). buildRows is pure state-read — same work render does —
	// and keeps the cursor in sync with state mutations the prior tick landed
	// (e.g. an in-memory fix that shrank the fixable set). The CLAUDE.md
	// "lazy-load in render()" rule targets expensive first-time loads (network
	// probes, GC sweeps); a state-derived row list isn't that.
	v.rows = v.buildRows()
	fixable := v.fixableRows()
	if v.cursor >= len(fixable) {
		v.cursor = max0(len(fixable) - 1)
	}

	switch key.String() {
	case "up", "k":
		if v.cursor > 0 {
			v.cursor--
		} else if v.top > 0 {
			v.top--
		}
	case "down", "j":
		if len(fixable) > 0 && v.cursor < len(fixable)-1 {
			v.cursor++
		}
		// No fallthrough to body-scroll here: render() handles auto-scroll
		// for the cursor and clamps v.top to actual line count. Use pgdn/G
		// to scroll past the last fixable row. Falling through used to
		// increment v.top unboundedly on each j press at end-of-list — the
		// visual was masked by render's clamp but state drifted.
	case "pgup":
		v.top -= 10
		if v.top < 0 {
			v.top = 0
		}
	case "pgdn":
		v.top += 10
	case "g", "home":
		v.top = 0
		v.cursor = 0
	case "l":
		if v.llmRunning {
			return nil
		}
		if len(fixable) == 0 {
			v.flash = styleDim.Render("no fixable issues to review")
			return nil
		}
		if !v.claudeOnPath {
			v.flash = styleWarn.Render("LLM review unavailable — claude CLI not found in PATH")
			return nil
		}
		row := fixable[v.cursor]
		prompt, ok := buildSummaryReviewPrompt(row, v.st)
		if !ok {
			v.flash = styleDim.Render("no review available for this row")
			return nil
		}
		v.llmRunning = true
		v.flash = styleProgress.Render("running LLM review for " + summarizeRow(row) + "…")
		return func() tea.Msg {
			out, err := claudeReviewCmd(v.st.project, prompt)
			return summaryReviewMsg{row: row, content: out, err: err}
		}
	case "f":
		if len(fixable) == 0 {
			v.flash = styleDim.Render("no fixable issues — Summary is clean")
			return nil
		}
		row := fixable[v.cursor]
		proposal, ok := buildSummaryFixProposal(row, v.st)
		if !ok {
			v.flash = styleDim.Render("no automatic fix for " + summarizeRow(row))
			return nil
		}
		if proposal.kind == fixClaudeCLI && !v.claudeOnPath {
			v.flash = styleWarn.Render("auto-fix unavailable — claude CLI not found in PATH")
			return nil
		}
		v.previewBody = v.buildFixPreview(proposal)
		v.previewScroll = 0
		v.pendingFix = proposal
	case "p":
		if v.pendingPrune {
			v.doPrune()
		} else {
			v.pendingPrune = true
			v.flash = styleWarn.Render("press 'p' again to prune orphaned overrides, any other key cancels")
		}
	}
	return nil
}

// buildFixPreview formats the body shown in the confirm panel. For fixInMemory
// proposals it joins previewLines verbatim; for fixClaudeCLI it shows the prompt
// alongside the target file. fixInTUI is not used by Summary today.
func (v *summaryView) buildFixPreview(p *fixProposal) string {
	if len(p.previewLines) > 0 {
		return strings.Join(p.previewLines, "\n")
	}
	if p.kind == fixClaudeCLI {
		return buildCLIPromptPreview(p)
	}
	return p.summary
}

func (v *summaryView) executeFix() tea.Cmd {
	p := v.pendingFix
	v.pendingFix = nil
	v.previewBody = ""
	v.previewScroll = 0

	switch p.kind {
	case fixInMemory:
		if p.applyFn == nil {
			v.flash = styleErr.Render("internal error: in-memory fix has no applyFn")
			return nil
		}
		flash, err := p.applyFn(v.st)
		if err != nil {
			v.flash = styleErr.Render("fix failed: " + err.Error())
			return nil
		}
		// Per CLAUDE.md "TUI toggle keys that change the visible set should
		// reset v.index = 0; v.top = 0 to prevent dangling cursor." The
		// fix shrinks the fixable list — invalidate cursor + scroll so the
		// next render starts from a known-good frame regardless of where
		// the user was navigating before.
		v.rows = nil
		v.cursor = 0
		v.top = 0
		v.flash = styleOK.Render(flash)
		return nil

	case fixClaudeCLI:
		snapDir := doctorSnapshotDir(v.st.paths.BackupsDir)
		if p.target != "" {
			if path, err := snapshotForFix(p.target, snapDir); err != nil {
				v.flash = styleErr.Render("snapshot: " + err.Error())
				return nil
			} else {
				p.snapshotPath = path
			}
		}
		if b, err := os.ReadFile(p.target); err == nil {
			p.beforeBytes = b
		}
		cliPath, err := exec.LookPath("claude")
		if err != nil {
			if !v.claudeOnPath {
				v.flash = styleErr.Render("claude CLI not found in PATH — install it or run the fix manually")
				return nil
			}
			cliPath = "claude"
		}
		cmd := exec.Command(cliPath, p.cliArgs...)
		cmd.Dir = v.st.project
		v.fixRunning = true
		v.fixStartedAt = time.Now()
		v.fixTarget = p.target
		v.fixOutput = nil
		return execFixCmd(cmd, p, tabSummary)
	}
	return nil
}

// revertFromSnapshot restores p.target to its pre-fix bytes. Mirrors the
// Doctor implementation.
func (v *summaryView) revertFromSnapshot(p *fixProposal) error {
	if p == nil || p.target == "" {
		return fmt.Errorf("nothing to revert")
	}
	if p.snapshotPath != "" {
		if data, err := os.ReadFile(p.snapshotPath); err == nil {
			return os.WriteFile(p.target, data, 0o644)
		}
	}
	if p.beforeBytes != nil {
		return os.WriteFile(p.target, p.beforeBytes, 0o644)
	}
	return fmt.Errorf("no snapshot available")
}

func (v *summaryView) doPrune() {
	v.pendingPrune = false
	overrides := v.st.cj.ProjectDisabledMcpServers(v.st.project)
	if len(overrides) == 0 {
		v.flash = styleDim.Render("no overrides to prune")
		return
	}
	userMCPs := v.st.cj.UserMCPNames()
	projMCPs := v.st.cj.ProjectMCPNames(v.st.project)
	claudeAi := v.st.cj.ClaudeAiEverConnected()
	var stashNames []string
	if v.st.stash != nil {
		stashNames = v.st.stash.Names()
	}
	cls := classify.Classify(overrides, userMCPs, projMCPs, claudeAi, stashNames, v.st.pluginMCPs)
	toRemove := append(cls.OrphanPlugin, cls.OrphanStdio...)
	if len(toRemove) == 0 {
		v.flash = styleDim.Render("no orphaned overrides found")
		return
	}
	remaining := overrides
	for _, k := range toRemove {
		remaining = stringslice.Remove(remaining, k)
	}
	v.st.cj.SetProjectDisabledMcpServers(v.st.project, remaining)
	if err := config.Backup(v.st.cj.Path, v.st.paths.BackupsDir); err != nil {
		v.flash = styleErr.Render("backup: " + err.Error())
		return
	}
	if err := v.st.cj.Save(); err != nil {
		v.flash = styleErr.Render("save: " + err.Error())
		return
	}
	v.flash = styleOK.Render(fmt.Sprintf("pruned %d orphaned entr%s", len(toRemove), classify.PluralY(len(toRemove))))
}

func (v *summaryView) fixableRows() []summaryRow {
	out := make([]summaryRow, 0, len(v.rows))
	for _, r := range v.rows {
		if r.fixable() {
			out = append(out, r)
		}
	}
	return out
}

func (v *summaryView) render() string {
	if v.fixRunning {
		target := filepath.Base(v.fixTarget)
		if target == "" || target == "." {
			target = "config"
		}
		return "Summary — " + v.st.spinnerFrame +
			styleProgress.Render(fmt.Sprintf("Applying LLM fix to %s… (%s)", target, fixElapsed(v.fixStartedAt))) +
			"\n" + styleDim.Render("running claude --print non-interactively") +
			"\n" + v.flash
	}

	if v.llmRunning {
		return "Summary — " + v.st.spinnerFrame + styleProgress.Render("LLM review in progress…") + "\n" + v.flash
	}

	v.rows = v.buildRows()
	fixable := v.fixableRows()
	if v.cursor >= len(fixable) {
		v.cursor = max0(len(fixable) - 1)
	}

	// Resolve the cursor's row in the full v.rows slice by counting fixable
	// entries. fixable[v.cursor] and v.rows[i] both derive from this same
	// buildRows() pass, so the i-th fixable row in v.rows is identically the
	// cursor's target — no need to match on identity.
	var cursorRowIdx int = -1
	if len(fixable) > 0 {
		fixSeen := 0
		for i, r := range v.rows {
			if !r.fixable() {
				continue
			}
			if fixSeen == v.cursor {
				cursorRowIdx = i
				break
			}
			fixSeen++
		}
	}

	// Assemble display lines.
	var b strings.Builder
	if len(fixable) > 0 {
		fmt.Fprintf(&b, "%s  %s\n",
			styleTitle.Render("Summary"),
			styleDim.Render(fmt.Sprintf("%d fixable issue(s) — f: fix  l: LLM review  j/k: navigate", len(fixable))))
	} else {
		fmt.Fprintf(&b, "%s  %s\n", styleTitle.Render("Summary"), styleOK.Render("no actionable issues"))
	}

	for i, r := range v.rows {
		text := r.text
		if i == cursorRowIdx {
			text = styleOK.Render("▶ ") + text
		} else if r.fixable() {
			text = "  " + text
		}
		b.WriteString(text)
		b.WriteString("\n")
	}

	// scroll
	lines := strings.Split(b.String(), "\n")
	maxH := v.h - 2
	if maxH < 5 {
		maxH = 5
	}
	// Auto-scroll to keep cursor visible.
	if cursorRowIdx >= 0 {
		// Find which output-line the cursor row lands on (each row is one line in this builder).
		// The header is line 0; rows start at line 1.
		cursorLine := cursorRowIdx + 1
		if cursorLine < v.top {
			v.top = cursorLine
		} else if cursorLine >= v.top+maxH {
			v.top = cursorLine - maxH + 1
		}
	}
	if v.top > len(lines)-maxH {
		v.top = len(lines) - maxH
	}
	if v.top < 0 {
		v.top = 0
	}
	end := v.top + maxH
	if end > len(lines) {
		end = len(lines)
	}
	body := strings.Join(lines[v.top:end], "\n")

	if v.pendingFix != nil {
		body += "\n" + v.renderPreviewPanel("Fix: "+v.pendingFix.summary, v.previewBody,
			"Apply? y   Cancel? n / esc   j/k: scroll")
	} else if v.postReview != nil {
		body += "\n" + v.renderPreviewPanel("Applied: "+v.postReview.summary, v.previewBody,
			"Keep? y   Revert? u / n / esc   j/k: scroll")
	} else if v.llmResult != "" {
		// LLM review output renders sticky-below so it isn't lost off-screen
		// when the body is taller than the viewport.
		var rb strings.Builder
		rb.WriteString(styleDim.Render(strings.Repeat("─", maxInt(44, v.w-2))))
		rb.WriteString("\n")
		rb.WriteString(styleTitle.Render("LLM review — "+summarizeRow(v.llmFor)) + "\n")
		for _, ln := range strings.Split(strings.TrimRight(v.llmResult, "\n"), "\n") {
			rb.WriteString("  " + ln + "\n")
		}
		rb.WriteString(styleDim.Render("(press any other key to dismiss)"))
		body += "\n" + rb.String()
	}
	return body
}

// renderPreviewPanel mirrors doctorView.renderPreviewPanel but lives on the
// summary view so it can size against summary's own w/h.
func (v *summaryView) renderPreviewPanel(title, body, footer string) string {
	var sb strings.Builder
	sb.WriteString(styleDim.Render(strings.Repeat("─", maxInt(44, v.w-2))))
	sb.WriteString("\n")
	sb.WriteString(title + "\n")

	maxPanel := v.h / 2
	if maxPanel < 6 {
		maxPanel = 6
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if v.previewScroll > len(lines)-1 {
		v.previewScroll = maxInt(0, len(lines)-1)
	}
	view := lines[v.previewScroll:]
	if len(view) > maxPanel {
		view = view[:maxPanel]
	}
	for _, l := range view {
		styled := l
		switch {
		case strings.HasPrefix(l, "+"):
			styled = styleOK.Render(l)
		case strings.HasPrefix(l, "-"):
			styled = styleErr.Render(l)
		case strings.HasPrefix(l, "@@"):
			styled = styleDim.Render(l)
		}
		sb.WriteString(styled)
		sb.WriteString("\n")
	}
	if v.previewScroll+maxPanel < len(lines) {
		sb.WriteString(styleDim.Render(fmt.Sprintf("…%d more lines", len(lines)-(v.previewScroll+maxPanel))))
		sb.WriteString("\n")
	}
	sb.WriteString(styleWarn.Render(footer))
	return sb.String()
}

// buildRows assembles the full row list. Display rows (titles, plain stats,
// blank separators) carry cat=catNone; fixable rows carry their category +
// the key/project needed to construct a fix proposal.
func (v *summaryView) buildRows() []summaryRow {
	var rows []summaryRow

	add := func(text string) {
		rows = append(rows, summaryRow{text: text})
	}
	addFix := func(text string, cat summaryCat, key, project string) {
		rows = append(rows, summaryRow{text: text, cat: cat, key: key, project: project})
	}

	// --- Updates available ----------------------------------------------
	if v.st.updates != nil {
		mkt, plg, mcp := v.st.updates.CountOutdated()
		if mkt+plg+mcp > 0 {
			add(styleTitle.Render("Updates available"))
			parts := []string{}
			if plg > 0 {
				parts = append(parts, fmt.Sprintf("%d plugin(s)", plg))
			}
			if mkt > 0 {
				parts = append(parts, fmt.Sprintf("%d marketplace(s)", mkt))
			}
			if mcp > 0 {
				parts = append(parts, fmt.Sprintf("%d MCP launcher(s)", mcp))
			}
			add("  " + styleWarn.Render("↑ "+strings.Join(parts, ", ")))
			add("")
		}
	}

	// --- MCPs counts ----------------------------------------------------
	userMCPs := v.st.cj.UserMCPNames()
	projMCPs := v.st.cj.ProjectMCPNames(v.st.project)
	stashed := v.st.stash.Names()
	var mcpjsonNames []string
	if m, err := config.LoadMCPJson(v.st.project + "/.mcp.json"); err == nil {
		mcpjsonNames = m.Names()
	}

	add(styleTitle.Render("MCP servers"))
	add(formatRow("  user scope     ", len(userMCPs), truncateList(userMCPs, 6)))
	add(formatRow("  local scope    ", len(projMCPs), truncateList(projMCPs, 6)))
	add(formatRow("  .mcp.json      ", len(mcpjsonNames), truncateList(mcpjsonNames, 6)))
	pluginSources := make([]string, 0, len(v.st.pluginMCPs))
	for name, srcs := range v.st.pluginMCPs {
		for _, s := range srcs {
			if s.Enabled {
				pluginSources = append(pluginSources, name)
				break
			}
		}
	}
	sort.Strings(pluginSources)
	add(formatRow("  via plugins    ", len(pluginSources), truncateList(pluginSources, 6)))
	claudeAi := v.st.cj.ClaudeAiEverConnected()
	sort.Strings(claudeAi)
	add(formatRow("  claude.ai      ", len(claudeAi), truncateList(claudeAi, 6)))
	add(formatRow("  stash (parked) ", len(stashed), truncateList(stashed, 6)))
	add("")

	// --- Per-project overrides -----------------------------------------
	overrides := v.st.cj.ProjectDisabledMcpServers(v.st.project)
	classified := classify.Classify(overrides, userMCPs, projMCPs, claudeAi, stashed, v.st.pluginMCPs)
	add(styleTitle.Render("Per-project overrides (disabledMcpServers)"))
	if len(overrides) == 0 {
		add(styleDim.Render("  (none for " + v.st.project + ")"))
		add("")
	} else {
		add(formatRow("  plugin (active)    ", len(classified.PluginActive), truncateList(classified.PluginActive, 4)))
		add(formatRow("  plugin (disabled)  ", len(classified.PluginDisabled), truncateList(classified.PluginDisabled, 4)))
		add(formatRow("  claude.ai          ", len(classified.ClaudeAi), truncateList(classified.ClaudeAi, 4)))
		add(formatRow("  stdio (live)       ", len(classified.StdioLive), truncateList(classified.StdioLive, 4)))
		// Fixable buckets get one row per entry so each is selectable.
		for _, k := range classified.OrphanPlugin {
			addFix(fmt.Sprintf("  %s  orphan (plugin)  %s",
				styleErr.Render("✗"), k), catOrphanPlugin, k, v.st.project)
		}
		for _, k := range classified.OrphanStdio {
			addFix(fmt.Sprintf("  %s  orphan (stdio)   %s",
				styleErr.Render("✗"), k), catOrphanStdio, k, v.st.project)
		}
		for _, k := range classified.StashGhost {
			addFix(fmt.Sprintf("  %s  stash ghost      %s",
				styleWarn.Render("⚠"), k), catStashGhost, k, v.st.project)
		}
		add("")

		// Cleanup hint, preserving the legacy `p` workflow.
		recoverable := pruneOrphanCount(&classified)
		if recoverable > 0 || len(classified.StashGhost) > 0 {
			add(styleTitle.Render("Cleanup suggestions"))
			if recoverable > 0 {
				add(fmt.Sprintf("  %s  bulk-prune with %s, or f on a row above to fix one at a time",
					styleOK.Render("•"), styleBadge.Render("p")))
			}
			if len(classified.StashGhost) > 0 {
				add(fmt.Sprintf("  %s  %d stash-ghost entr%s — f to drop one at a time",
					styleDim.Render("•"),
					len(classified.StashGhost),
					classify.PluralY(len(classified.StashGhost))))
			}
			add("")
		}
	}

	// --- Plugins --------------------------------------------------------
	var enabled, disabled, unknown, installedOnly int
	installedIdx := map[string]config.InstalledPlugin{}
	for _, ip := range v.st.installed.List() {
		installedIdx[ip.ID] = ip
	}
	knownIDs := map[string]bool{}
	var unknownIDs, installedOnlyIDs []string
	for _, e := range v.st.settings.PluginEntries() {
		knownIDs[e.ID] = true
		if e.Enabled {
			enabled++
		} else {
			disabled++
		}
		if _, ok := installedIdx[e.ID]; !ok {
			unknown++
			unknownIDs = append(unknownIDs, e.ID)
		}
	}
	for id := range installedIdx {
		if !knownIDs[id] {
			installedOnly++
			installedOnlyIDs = append(installedOnlyIDs, id)
		}
	}
	sort.Strings(unknownIDs)
	sort.Strings(installedOnlyIDs)
	add(styleTitle.Render("Plugins"))
	add(fmt.Sprintf("  enabled               %d", enabled))
	add(fmt.Sprintf("  disabled (installed)  %d", disabled))
	add(fmt.Sprintf("  enabled but not installed   %s", warnNum(unknown)))
	for _, id := range unknownIDs {
		addFix(fmt.Sprintf("    %s  %s", styleWarn.Render("⚠"), id),
			catPluginEnabledNotInstalled, id, "")
	}
	add(fmt.Sprintf("  installed but not in settings %s", warnNum(installedOnly)))
	for _, id := range installedOnlyIDs {
		addFix(fmt.Sprintf("    %s  %s", styleWarn.Render("⚠"), id),
			catPluginInstalledNotEnabled, id, "")
	}
	add("")

	// --- Marketplaces ---------------------------------------------------
	extras := v.st.settings.ExtraMarketplaces()
	known, _ := config.LoadKnownMarketplaces(v.st.paths.KnownMarkets)
	var knownNames []string
	if known != nil {
		knownNames = known.Names()
	}
	add(styleTitle.Render("Marketplaces"))
	add(fmt.Sprintf("  system-known   %d  (%s)", len(knownNames), styleDim.Render(strings.Join(knownNames, ", "))))
	add(fmt.Sprintf("  extras         %d", len(extras)))
	add("")

	// --- Profiles -------------------------------------------------------
	names := v.st.profiles.Names()
	add(styleTitle.Render("Profiles"))
	add(fmt.Sprintf("  saved          %d  (%s)", len(names), styleDim.Render(strings.Join(names, ", "))))
	add("")

	// --- Skills / Agents / Commands ------------------------------------
	discoveredSkills := skills.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	discoveredAgents := agents.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	discoveredCmds := commands.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	conflicts := commands.FindConflicts(discoveredCmds, discoveredSkills)
	var skillEnabled, skillPlugin, skillUser int
	for _, s := range discoveredSkills {
		if s.Enabled {
			skillEnabled++
		}
		switch s.Scope {
		case skills.ScopePlugin:
			skillPlugin++
		case skills.ScopeUser:
			skillUser++
		}
	}
	var agentPlugin, agentUser int
	for _, a := range discoveredAgents {
		switch a.Scope {
		case agents.ScopePlugin:
			agentPlugin++
		case agents.ScopeUser:
			agentUser++
		}
	}
	var cmdPlugin, cmdUser int
	for _, c := range discoveredCmds {
		switch c.Scope {
		case commands.ScopePlugin:
			cmdPlugin++
		case commands.ScopeUser:
			cmdUser++
		}
	}
	add(styleTitle.Render("Skills / Agents / Commands"))
	add(fmt.Sprintf("  skills     %d enabled / %d total  (user %d, plugin %d)", skillEnabled, len(discoveredSkills), skillUser, skillPlugin))
	add(fmt.Sprintf("  agents     %d total  (user %d, plugin %d)", len(discoveredAgents), agentUser, agentPlugin))
	add(fmt.Sprintf("  commands   %d total  (user %d, plugin %d)", len(discoveredCmds), cmdUser, cmdPlugin))
	if len(conflicts) > 0 {
		add(fmt.Sprintf("  %s  %d slash-command conflict(s)", styleWarn.Render("⚠"), len(conflicts)))
		for _, c := range conflicts {
			addFix(fmt.Sprintf("    %s  /%s",
				styleDim.Render(string(c.Kind)), c.Effective),
				catSlashConflict, c.Effective, "")
		}
	}
	add("")

	// --- Redundancies / warnings ---------------------------------------
	// duplicate-load: same MCP in user + project scope
	projSet := map[string]bool{}
	for _, n := range projMCPs {
		projSet[n] = true
	}
	var dup []string
	for _, n := range userMCPs {
		if projSet[n] {
			dup = append(dup, n)
		}
	}
	sort.Strings(dup)
	// stash entry duplicated in user scope
	userSet := map[string]bool{}
	for _, n := range userMCPs {
		userSet[n] = true
	}
	var stashAndUser []string
	for _, n := range stashed {
		if userSet[n] {
			stashAndUser = append(stashAndUser, n)
		}
	}
	// stash entries also provided by an enabled plugin
	enabledPluginMCPs := map[string]bool{}
	for name, srcs := range v.st.pluginMCPs {
		for _, s := range srcs {
			if s.Enabled {
				enabledPluginMCPs[name] = true
				break
			}
		}
	}
	var stashedButPluginProvides []string
	for _, n := range stashed {
		if enabledPluginMCPs[n] {
			stashedButPluginProvides = append(stashedButPluginProvides, n)
		}
	}
	var userDupPlugin []string
	for _, n := range userMCPs {
		if enabledPluginMCPs[n] {
			userDupPlugin = append(userDupPlugin, n)
		}
	}
	// stale .mcp.json refs
	var stale []string
	if len(mcpjsonNames) > 0 {
		mcpjsonSet := map[string]bool{}
		for _, n := range mcpjsonNames {
			mcpjsonSet[n] = true
		}
		for _, n := range v.st.cj.ProjectMcpjsonEnabled(v.st.project) {
			if !mcpjsonSet[n] {
				stale = append(stale, n)
			}
		}
		for _, n := range v.st.cj.ProjectMcpjsonDisabled(v.st.project) {
			if !mcpjsonSet[n] {
				stale = append(stale, n)
			}
		}
	}

	hasWarn := len(dup)+len(stashAndUser)+len(stashedButPluginProvides)+len(userDupPlugin)+len(stale) > 0
	if !hasWarn {
		add(styleOK.Render("Redundancies: (none — everything looks clean)"))
	} else {
		add(styleWarn.Render("Redundancies:"))
		for _, n := range dup {
			addFix(fmt.Sprintf("  %s  active in BOTH user and project scope (will load twice): %s",
				styleWarn.Render("⚠"), n),
				catDuplicateLoad, n, v.st.project)
		}
		for _, n := range stashAndUser {
			addFix(fmt.Sprintf("  %s  stash redundant with user scope: %s", styleWarn.Render("⚠"), n),
				catStashRedundantWithUser, n, "")
		}
		for _, n := range stashedButPluginProvides {
			addFix(fmt.Sprintf("  %s  stash ghosted by enabled plugin: %s", styleWarn.Render("⚠"), n),
				catStashGhostedByPlugin, n, "")
		}
		for _, n := range userDupPlugin {
			addFix(fmt.Sprintf("  %s  user-scope duplicates plugin MCP: %s", styleWarn.Render("⚠"), n),
				catUserDupPlugin, n, "")
		}
		for _, n := range stale {
			addFix(fmt.Sprintf("  %s  stale .mcp.json ref: %s", styleWarn.Render("⚠"), n),
				catStaleMcpjson, n, v.st.project)
		}
	}

	return rows
}

func (v *summaryView) resize(w, h int) { v.w, v.h = w, h }

func (v *summaryView) helpText() string {
	if v.fixRunning {
		return "applying LLM fix — please wait"
	}
	if v.llmRunning {
		return "LLM review in progress…"
	}
	if v.postReview != nil {
		return "y: keep change  u/n/esc: revert from snapshot  j/k: scroll diff"
	}
	if v.pendingFix != nil {
		return "y: apply  n/esc: cancel  j/k: scroll preview"
	}
	suffix := ""
	if !v.claudeOnPath {
		suffix = "  " + styleDim.Render("(claude CLI missing)")
	}
	return "j/k: navigate  f: fix issue  l: LLM review  p: bulk prune  g: top" + suffix
}

func (v *summaryView) capturingInput() bool { return false }

// --- helpers ---------------------------------------------------------------

func formatRow(label string, count int, sample string) string {
	return fmt.Sprintf("%s %3d  %s", label, count, styleDim.Render(sample))
}

func truncateList(ss []string, max int) string {
	if len(ss) == 0 {
		return "(none)"
	}
	if len(ss) <= max {
		return strings.Join(ss, ", ")
	}
	return strings.Join(ss[:max], ", ") + fmt.Sprintf(", … +%d more", len(ss)-max)
}

func warnNum(n int) string {
	if n == 0 {
		return styleDim.Render("0")
	}
	return styleWarn.Render(fmt.Sprintf("%d", n))
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
