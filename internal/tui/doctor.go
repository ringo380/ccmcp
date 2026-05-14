package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

type fixKind int

const (
	fixInTUI fixKind = iota
	fixClaudeCLI
)

type fixProposal struct {
	summary   string
	kind      fixKind
	target    string   // primary file being modified
	proposed  []byte   // pre-computed post-state bytes (fixInTUI only); nil for CLI
	cliArgs   []string // args for exec.Command("claude", cliArgs...)
	cliPrompt string   // full prompt text (CLI only) — shown verbatim in confirm panel

	// runtime-populated
	snapshotPath string // disk path of pre-fix snapshot, set by executeFix
	beforeBytes  []byte // in-memory copy of target file pre-fix (for CLI revert)
}

type doctorFixDoneMsg struct {
	err      error
	proposal *fixProposal // the proposal that just finished — used to drive postReview
}

// doctorView runs structural lint checks on CLAUDE.md and MEMORY.md and
// displays the results. Press 'r' to re-run lint; 'l' to run an LLM review;
// 'j/k' to navigate issues; 'f' to fix the selected issue.
type doctorView struct {
	st     *state
	groups []docGroup
	w, h   int
	top    int
	loaded bool // false until first lint run; set false again on 'r'

	allIssues  []doctor.Issue // flattened after runLint, for cursor indexing
	cursor     int
	pendingFix *fixProposal
	postReview *fixProposal // non-nil while showing post-apply diff for keep/revert
	lastFix    *fixProposal // last attempted fix, for retry hint
	lastFixErr error        // result of last fix run

	previewDiff   string // unified-diff body shown in pendingFix/postReview panel
	previewScroll int    // scroll offset within the preview panel

	// LLM review state
	llmRunning bool
	llmResults []llmReviewResult
	showLLM    bool

	// claudeOnPath is cached at view init: when false, LLM review and fix-via-CLI
	// are unavailable and the keys 'l' / 'f' / 'a' surface a friendly hint instead.
	claudeOnPath bool

	// appliedReviewIdx is the index into llmResults of the review that the current
	// pendingFix proposal originated from (set when 'a' opens the gate). -1 means
	// the pending proposal didn't come from the apply-review path. We defer setting
	// `applied=true` on the review until the user confirms with 'y', so canceling
	// with 'n' leaves the review re-applyable.
	appliedReviewIdx int

	flash string
}

type docGroup struct {
	label  string
	issues []doctor.Issue
}

type llmReviewResult struct {
	path    string
	content string
	err     error
	applied bool // true once the user has run `a` to apply this review's suggestions
}

type doctorLLMResultMsg struct {
	results []llmReviewResult
}

func newDoctorView(st *state) *doctorView {
	v := &doctorView{st: st, appliedReviewIdx: -1}
	if _, err := exec.LookPath("claude"); err == nil {
		v.claudeOnPath = true
	}
	return v
}

func (v *doctorView) runLint() {
	v.groups = v.groups[:0]

	// Project CLAUDE.md
	claudePath := filepath.Join(v.st.project, "CLAUDE.md")
	v.groups = append(v.groups, docGroup{
		label:  "CLAUDE.md (project)",
		issues: doctor.LintClaudeMD(claudePath),
	})

	// MEMORY.md for this project
	memDir := tuiMemoryPath(v.st.paths.ClaudeConfigDir, v.st.project)
	v.groups = append(v.groups, docGroup{
		label:  "MEMORY.md",
		issues: doctor.LintMemoryIndex(memDir),
	})

	v.loaded = true
	v.top = 0

	// Fire-and-forget snapshot GC. Errors are silent — they affect cleanup,
	// not correctness of the lint or any fix.
	go gcDoctorSnapshots(doctorSnapshotDir(v.st.paths.BackupsDir), doctorSnapshotKeep, doctorSnapshotMaxAge)

	// Rebuild flat issue list for cursor navigation.
	v.allIssues = v.allIssues[:0]
	for _, g := range v.groups {
		v.allIssues = append(v.allIssues, g.issues...)
	}
	v.cursor = 0
}

// tuiMemoryPath derives the memory directory path using the same slug logic as cmd/doctor.go.
func tuiMemoryPath(claudeConfigDir, projectPath string) string {
	slug := strings.ReplaceAll(projectPath, "/", "-")
	return filepath.Join(claudeConfigDir, "projects", slug, "memory")
}

func (v *doctorView) update(msg tea.Msg) tea.Cmd {
	if result, ok := msg.(doctorLLMResultMsg); ok {
		v.llmRunning = false
		v.llmResults = result.results
		v.showLLM = true
		v.top = 0
		v.flash = ""
		return nil
	}
	if done, ok := msg.(doctorFixDoneMsg); ok {
		v.lastFixErr = done.err
		if done.err != nil {
			v.flash = styleErr.Render("fix failed: " + enrichExitStatus(done.err.Error()))
			v.loaded = false // re-lint to show current state
			return nil
		}
		// CLI fix landed — compute post-apply diff and enter postReview gate.
		if done.proposal != nil && done.proposal.kind == fixClaudeCLI {
			after, err := os.ReadFile(done.proposal.target)
			if err != nil {
				v.flash = styleErr.Render(fmt.Sprintf(
					"read post-fix %s: %s — snapshot kept at %s",
					filepath.Base(done.proposal.target), err.Error(), done.proposal.snapshotPath,
				))
				v.loaded = false
				return nil
			}
			diff := unifiedDiff(string(done.proposal.beforeBytes), string(after), 3)
			if diff == "" {
				// Claude exited 0 but didn't change anything — nothing to review.
				// Drop the snapshot we took preemptively; nothing to revert to.
				deleteSnapshot(done.proposal.snapshotPath)
				v.flash = styleWarn.Render("claude CLI made no changes")
				v.lastFix = nil
				v.loaded = false
				return nil
			}
			v.postReview = done.proposal
			v.previewDiff = diff
			v.previewScroll = 0
			v.flash = ""
			return nil
		}
		v.flash = styleOK.Render("fix applied")
		v.lastFix = nil
		v.loaded = false // trigger re-lint on next render
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	// Post-apply review (CLI fix) intercepts all keys.
	if v.postReview != nil {
		switch key.String() {
		case "y":
			// Keep the change.
			path := v.postReview.snapshotPath
			v.flash = styleOK.Render("kept: " + v.postReview.summary)
			if path != "" {
				v.flash += "  " + styleDim.Render("(snapshot: "+path+")")
			}
			v.postReview = nil
			v.previewDiff = ""
			v.previewScroll = 0
			v.lastFix = nil
			v.loaded = false
		case "u", "n", "esc":
			// Revert from disk snapshot (falls back to in-memory beforeBytes).
			if err := v.revertFromSnapshot(v.postReview); err != nil {
				v.flash = styleErr.Render("revert failed: " + err.Error())
			} else {
				// File is restored — snapshot is redundant, drop it so GC has less to sweep.
				deleteSnapshot(v.postReview.snapshotPath)
				v.flash = styleOK.Render("reverted: " + v.postReview.summary)
			}
			v.postReview = nil
			v.previewDiff = ""
			v.previewScroll = 0
			v.loaded = false
		case "j", "down":
			v.previewScroll++
		case "k", "up":
			if v.previewScroll > 0 {
				v.previewScroll--
			}
		}
		return nil
	}

	// Confirm dialog intercepts all keys.
	if v.pendingFix != nil {
		switch key.String() {
		case "y":
			// If this proposal originated from the apply-review path, the user is now
			// committing — mark the source review applied so it isn't re-offered.
			if v.appliedReviewIdx >= 0 && v.appliedReviewIdx < len(v.llmResults) {
				v.llmResults[v.appliedReviewIdx].applied = true
				v.appliedReviewIdx = -1
			}
			return v.executeFix()
		case "n", "esc":
			cameFromReview := v.appliedReviewIdx >= 0
			v.pendingFix = nil
			v.previewDiff = ""
			v.previewScroll = 0
			v.appliedReviewIdx = -1
			if cameFromReview {
				// Return to the LLM review the user was considering, so they can
				// re-read it or try again with 'a'.
				v.showLLM = true
				v.top = 0
			}
		case "j", "down":
			v.previewScroll++
		case "k", "up":
			if v.previewScroll > 0 {
				v.previewScroll--
			}
		}
		return nil
	}

	pageH := v.pageHeight()
	numIssues := len(v.allIssues)

	switch key.String() {
	case "a":
		// Apply the next unapplied, error-free review by handing the feedback
		// back to claude-cli. Same machinery as a per-issue fix: snapshot,
		// approval, post-apply diff, keep/revert.
		if !v.showLLM {
			break
		}
		idx := nextApplicableReview(v.llmResults)
		if idx < 0 {
			v.flash = styleDim.Render("no remaining reviews to apply")
			return nil
		}
		if !v.claudeOnPath {
			v.flash = styleWarn.Render("apply review unavailable — claude CLI not found in PATH")
			return nil
		}
		proposal := buildReviewApplyProposal(v.llmResults[idx])
		v.previewDiff = buildCLIPromptPreview(proposal)
		v.previewScroll = 0
		v.pendingFix = proposal
		// Remember which review this proposal came from so we can mark it applied
		// only on approval ('y'), and restore the LLM view on cancel ('n'/'esc').
		v.appliedReviewIdx = idx
		// Drop back to lint view so the post-review diff panel renders cleanly
		// over it rather than fighting the review text for screen space.
		v.showLLM = false
	case "r":
		v.loaded = false // render() will re-run lint on the next frame
		v.showLLM = false
		v.llmResults = nil
		v.flash = ""
		v.lastFix = nil
		v.lastFixErr = nil
		v.appliedReviewIdx = -1
	case "l":
		if v.llmRunning {
			return nil
		}
		if !v.canRunLLM() {
			v.flash = styleWarn.Render("LLM review unavailable — install the claude CLI or set ANTHROPIC_API_KEY/OPENAI_API_KEY")
			return nil
		}
		v.llmRunning = true
		v.showLLM = false
		v.flash = styleProgress.Render("running LLM review…")
		claudePath := filepath.Join(v.st.project, "CLAUDE.md")
		memDir := tuiMemoryPath(v.st.paths.ClaudeConfigDir, v.st.project)
		memPath := filepath.Join(memDir, "MEMORY.md")
		return func() tea.Msg {
			// Zero-value Provider: doctor.Review auto-selects the best available
			// backend (claude-cli when no API keys are set, anthropic/openai otherwise).
			opts := doctor.ReviewOptions{}
			var results []llmReviewResult
			if content, err := doctor.Review(claudePath, opts); err != nil {
				results = append(results, llmReviewResult{path: claudePath, err: err})
			} else {
				results = append(results, llmReviewResult{path: claudePath, content: content})
			}
			if content, err := doctor.Review(memPath, opts); err != nil {
				results = append(results, llmReviewResult{path: memPath, err: err})
			} else {
				results = append(results, llmReviewResult{path: memPath, content: content})
			}
			return doctorLLMResultMsg{results: results}
		}
	case "f":
		if !v.llmRunning && numIssues > 0 {
			// 'f' fixes the issue under the cursor — allowed from both lint view
			// and the LLM-results view (since the cursor still tracks the lint set).
			if v.showLLM {
				// The cursor reflects the lint list, not the review list — drop back
				// so the diff panel renders cleanly and the user sees what they're fixing.
				v.showLLM = false
			}
			proposal, ok := buildFixProposal(v.allIssues[v.cursor], v.st.project)
			if !ok {
				v.flash = styleDim.Render("no automatic fix for " + v.allIssues[v.cursor].Code)
				return nil
			}
			if proposal.kind == fixClaudeCLI && !v.claudeOnPath {
				v.flash = styleWarn.Render("auto-fix unavailable — claude CLI not found in PATH")
				return nil
			}
			// Build the preview content shown before approval.
			if proposal.kind == fixInTUI {
				orig, err := os.ReadFile(proposal.target)
				if err != nil && !os.IsNotExist(err) {
					v.flash = styleErr.Render("read target: " + err.Error())
					return nil
				}
				v.previewDiff = unifiedDiff(string(orig), string(proposal.proposed), 3)
				if v.previewDiff == "" {
					v.flash = styleWarn.Render("fix would not change " + filepath.Base(proposal.target))
					return nil
				}
			} else {
				// CLI pre-prompt preview: show the prompt + a short excerpt of the
				// target file (when present). previewDiff doubles as the panel body.
				v.previewDiff = buildCLIPromptPreview(proposal)
			}
			v.previewScroll = 0
			v.pendingFix = proposal
		}
	case "j", "down":
		if v.showLLM {
			totalLines := v.totalLines()
			if v.top < totalLines-pageH {
				v.top++
			}
		} else if numIssues > 0 && v.cursor < numIssues-1 {
			v.cursor++
		}
	case "k", "up":
		if v.showLLM {
			if v.top > 0 {
				v.top--
			}
		} else if v.cursor > 0 {
			v.cursor--
		}
	case "g", "home":
		v.top = 0
		v.cursor = 0
	case "G", "end":
		if v.showLLM {
			totalLines := v.totalLines()
			if totalLines > pageH {
				v.top = totalLines - pageH
			}
		} else if numIssues > 0 {
			v.cursor = numIssues - 1
		}
	case "pgdn":
		if v.showLLM {
			totalLines := v.totalLines()
			v.top += pageH
			if v.top > totalLines-pageH {
				v.top = totalLines - pageH
			}
			if v.top < 0 {
				v.top = 0
			}
		} else {
			v.cursor += pageH
			if v.cursor >= numIssues {
				v.cursor = numIssues - 1
			}
			if v.cursor < 0 {
				v.cursor = 0
			}
		}
	case "pgup":
		if v.showLLM {
			v.top -= pageH
			if v.top < 0 {
				v.top = 0
			}
		} else {
			v.cursor -= pageH
			if v.cursor < 0 {
				v.cursor = 0
			}
		}
	}
	return nil
}

// execFixCmd is the indirection used to run the claude CLI. Tests replace it.
var execFixCmd = func(cmd *exec.Cmd, p *fixProposal) tea.Cmd {
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return doctorFixDoneMsg{err: err, proposal: p}
	})
}

func (v *doctorView) executeFix() tea.Cmd {
	p := v.pendingFix
	v.pendingFix = nil
	v.previewDiff = ""
	v.previewScroll = 0
	v.lastFix = p
	v.lastFixErr = nil

	// Snapshot before any write. snapshotForFix is a no-op if target doesn't exist.
	snapDir := doctorSnapshotDir(v.st.paths.BackupsDir)
	if p.target != "" {
		if path, err := snapshotForFix(p.target, snapDir); err != nil {
			v.lastFixErr = err
			v.flash = styleErr.Render("snapshot: " + err.Error())
			return nil
		} else {
			p.snapshotPath = path
		}
	}

	if p.kind == fixInTUI {
		if err := os.WriteFile(p.target, p.proposed, 0o644); err != nil {
			v.lastFixErr = err
			// Write failed — the file is unchanged, so the snapshot we took is orphan junk.
			deleteSnapshot(p.snapshotPath)
			p.snapshotPath = ""
			v.flash = styleErr.Render("fix failed: " + err.Error())
			return nil
		}
		msg := styleOK.Render("fixed: " + p.summary)
		if p.snapshotPath != "" {
			msg += "  " + styleDim.Render("(snapshot: "+p.snapshotPath+")")
		}
		v.flash = msg
		v.loaded = false
		v.lastFix = nil
		return nil
	}

	// Claude CLI fix: keep an in-memory copy too so revert works even if disk snapshot disappears.
	if b, err := os.ReadFile(p.target); err == nil {
		p.beforeBytes = b
	}
	// Trust the cached init state: if claudeOnPath was true at startup, hand the
	// command off even when LookPath now fails — exec.Command resolves PATH
	// lazily at Start() time and tests stub execFixCmd before any real spawn.
	cliPath, err := exec.LookPath("claude")
	if err != nil {
		if !v.claudeOnPath {
			v.lastFixErr = doctor.ErrClaudeCLINotFound
			v.flash = styleErr.Render("claude CLI not found in PATH — install it or run the fix manually")
			return nil
		}
		cliPath = "claude"
	}
	cmd := exec.Command(cliPath, p.cliArgs...)
	// Run from the project root so Claude inherits the project's CLAUDE.md and
	// .claude/ ambient context. Without this, claude resolves cwd to wherever
	// ccmcp was launched and the project's instructions never load.
	cmd.Dir = v.st.project
	return execFixCmd(cmd, p)
}

// revertFromSnapshot restores p.target to its pre-fix state. Prefers the on-disk snapshot;
// falls back to in-memory beforeBytes if the snapshot is unreadable (or empty).
func (v *doctorView) revertFromSnapshot(p *fixProposal) error {
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

// canRunLLM reports whether at least one provider backend is currently available:
// the claude CLI on PATH, or an Anthropic/OpenAI API key in the environment.
func (v *doctorView) canRunLLM() bool {
	if v.claudeOnPath {
		return true
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	return false
}

// enrichExitStatus rewrites bare "exit status N" messages from tea.ExecProcess
// (which hands the TTY to the subprocess and so loses stderr capture) into a
// hint that points the user to the output that scrolled by above.
func enrichExitStatus(msg string) string {
	re := regexp.MustCompile(`^exit status (\d+)$`)
	if m := re.FindStringSubmatch(strings.TrimSpace(msg)); m != nil {
		return fmt.Sprintf("claude CLI exit %s — see output above", m[1])
	}
	return msg
}

// wrapStyled hard-wraps s on whitespace to fit width-2 columns, returning one
// styled line per wrapped row. Embedded newlines are preserved as separators.
func wrapStyled(s string, width int, style lipgloss.Style) []string {
	if width < 8 {
		width = 80
	}
	max := width - 2
	var out []string
	for _, segment := range strings.Split(s, "\n") {
		if segment == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(segment)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		var line strings.Builder
		for _, w := range words {
			if line.Len() == 0 {
				line.WriteString(w)
				continue
			}
			if line.Len()+1+len(w) > max {
				out = append(out, style.Render(line.String()))
				line.Reset()
				line.WriteString(w)
				continue
			}
			line.WriteByte(' ')
			line.WriteString(w)
		}
		if line.Len() > 0 {
			out = append(out, style.Render(line.String()))
		}
	}
	return out
}

func (v *doctorView) render() string {
	if !v.loaded && !v.llmRunning {
		v.runLint()
	}

	if v.llmRunning {
		return "Doctor — " + v.st.spinnerFrame + styleProgress.Render("LLM review in progress…") + "\n" + v.flash
	}

	if v.showLLM {
		return v.renderLLM()
	}

	total := 0
	for _, g := range v.groups {
		total += len(g.issues)
	}

	var b strings.Builder
	if !v.claudeOnPath {
		b.WriteString(styleWarn.Render("claude CLI not found in PATH — LLM review and auto-fix unavailable"))
		b.WriteString("\n")
	}
	if total == 0 {
		fmt.Fprintf(&b, "Doctor — ")
		b.WriteString(styleOK.Render("all clear"))
	} else {
		fmt.Fprintf(&b, "Doctor — ")
		b.WriteString(styleWarn.Render(fmt.Sprintf("%d issue(s)", total)))
	}
	b.WriteString("\n")

	lines, issueLineIndices := v.buildLintLines()

	// Scroll to keep cursor visible.
	pageH := v.pageHeight()
	if v.pendingFix != nil {
		pageH -= 4 // reserve space for confirm banner
		if pageH < 1 {
			pageH = 1
		}
	}
	if len(issueLineIndices) > 0 && v.cursor < len(issueLineIndices) {
		cursorLine := issueLineIndices[v.cursor]
		if cursorLine < v.top {
			v.top = cursorLine
		} else if cursorLine >= v.top+pageH {
			v.top = cursorLine - pageH + 1
		}
	}
	if v.top < 0 {
		v.top = 0
	}
	end := v.top + pageH
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[v.top:end] {
		b.WriteString(l)
		b.WriteString("\n")
	}

	if v.pendingFix != nil {
		b.WriteString(v.renderPreviewPanel("Fix: "+v.pendingFix.summary, v.previewDiff, "Apply? y   Cancel? n / esc   j/k: scroll"))
	} else if v.postReview != nil {
		b.WriteString(v.renderPreviewPanel(
			"Applied: "+v.postReview.summary,
			v.previewDiff,
			"Keep? y   Revert? u / n / esc   j/k: scroll",
		))
	}

	return b.String()
}

// renderPreviewPanel renders the bordered diff/prompt panel used by both pre-apply
// (pendingFix) and post-apply (postReview) gates. body is a diff or prompt block;
// it is split, scrolled by v.previewScroll, and colorized by leading char (+/-/@@).
func (v *doctorView) renderPreviewPanel(title, body, footer string) string {
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// buildCLIPromptPreview formats the body shown in the pre-apply confirm panel for CLI fixes:
// the full Claude prompt, plus a hint about the target file.
func buildCLIPromptPreview(p *fixProposal) string {
	var sb strings.Builder
	sb.WriteString("@@ target: " + p.target + " @@\n")
	for _, line := range strings.Split(p.cliPrompt, "\n") {
		sb.WriteString(" " + line + "\n")
	}
	return sb.String()
}

// buildLintLines returns the render lines plus the line-index of each issue (for cursor scroll).
func (v *doctorView) buildLintLines() (lines []string, issueLineIndices []int) {
	issueIdx := 0
	for _, g := range v.groups {
		lines = append(lines, styleDim.Render("── "+g.label+" ──"))
		if len(g.issues) == 0 {
			lines = append(lines, "  "+styleOK.Render("✓")+" no issues")
		} else {
			for _, iss := range g.issues {
				icon := styleDim.Render("·")
				switch iss.Severity {
				case doctor.SeverityError:
					icon = styleErr.Render("✗")
				case doctor.SeverityWarning:
					icon = styleWarn.Render("⚠")
				}
				loc := iss.File
				if iss.Line > 0 {
					loc = fmt.Sprintf("%s:%d", iss.File, iss.Line)
				}
				cursor := "  "
				if issueIdx == v.cursor {
					cursor = styleOK.Render("▶ ")
				}
				issueLineIndices = append(issueLineIndices, len(lines))
				lines = append(lines, fmt.Sprintf("%s%s [%s] %s — %s",
					cursor,
					icon,
					styleDim.Render(iss.Code),
					styleDim.Render(loc),
					iss.Message,
				))
				issueIdx++
			}
		}
		lines = append(lines, "")
	}
	return lines, issueLineIndices
}

func (v *doctorView) renderLLM() string {
	var b strings.Builder
	b.WriteString("Doctor — LLM review\n")

	var lines []string
	for _, r := range v.llmResults {
		header := "── " + r.path + " ──"
		if r.applied {
			header += "  " + styleOK.Render("(applied)")
		}
		lines = append(lines, styleDim.Render(header))
		if r.err != nil {
			for _, wrapped := range wrapStyled("error: "+r.err.Error(), v.w-2, styleErr) {
				lines = append(lines, "  "+wrapped)
			}
			if errors.Is(r.err, doctor.ErrClaudeCLINotFound) {
				lines = append(lines, "  "+styleDim.Render("hint: install the claude CLI or set ANTHROPIC_API_KEY/OPENAI_API_KEY"))
			}
			var apiErr *doctor.APIError
			if errors.As(r.err, &apiErr) && apiErr.Status == 401 {
				lines = append(lines, "  "+styleDim.Render("hint: run `claude /login` or rerun with --provider claude-cli"))
			}
		} else {
			for _, l := range strings.Split(r.content, "\n") {
				lines = append(lines, "  "+l)
			}
		}
		lines = append(lines, "")
	}

	pageH := v.pageHeight()
	if v.top < 0 {
		v.top = 0
	}
	end := v.top + pageH
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[v.top:end] {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

func (v *doctorView) resize(w, h int) { v.w, v.h = w, h }

func (v *doctorView) helpText() string {
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
	if v.showLLM {
		return "r: re-run lint  l: LLM review  a: apply review  f: fix issue  j/k: scroll  g/G: top/bottom" + suffix
	}
	return "r: re-run lint  l: LLM review  j/k: navigate  f: fix issue  g/G: top/bottom" + suffix
}

func (v *doctorView) capturingInput() bool { return false }

func (v *doctorView) pageHeight() int {
	h := v.h - 4
	if h < 4 {
		h = 4
	}
	return h
}

func (v *doctorView) totalLines() int {
	if v.showLLM {
		n := 0
		for _, r := range v.llmResults {
			n++ // group label
			if r.err != nil {
				n++ // error line
			} else {
				n += len(strings.Split(r.content, "\n"))
			}
			n++ // blank separator
		}
		return n
	}
	n := 0
	for _, g := range v.groups {
		n++ // group label
		if len(g.issues) == 0 {
			n++ // "no issues" line
		} else {
			n += len(g.issues)
		}
		n++ // blank separator
	}
	return n
}

// buildFixProposal returns a fix proposal for the given issue, or nil/false if no fix is available.
// In-TUI proposals carry pre-computed `proposed` bytes so the caller can render a diff against the
// current target file before writing. CLI proposals carry the full prompt text in `cliPrompt`.
func buildFixProposal(issue doctor.Issue, projectPath string) (*fixProposal, bool) {
	// contextPreamble nudges Claude to load the project's ambient docs before
	// editing, so tone/scope decisions match what the rest of the project does.
	// The CLI fix is launched with cwd = project root (see executeFix), so these
	// paths resolve relative to the project.
	contextPreamble := func(extra ...string) string {
		paths := []string{"./CLAUDE.md"}
		paths = append(paths, extra...)
		return fmt.Sprintf(
			"Before editing, read %s to match the project's existing tone, scope, and conventions. Do not invent content that contradicts what's already there.\n\n",
			strings.Join(paths, " and "),
		)
	}
	cli := func(summary, target, prompt string) *fixProposal {
		return &fixProposal{
			summary:   summary,
			kind:      fixClaudeCLI,
			target:    target,
			cliPrompt: prompt,
			cliArgs:   claudeFixArgs(prompt),
		}
	}

	switch issue.Code {
	case "MEM002":
		proposed, err := removeFileLineBytes(issue.File, issue.Line)
		if err != nil {
			return nil, false
		}
		return &fixProposal{
			summary:  "Remove broken index entry from MEMORY.md",
			kind:     fixInTUI,
			target:   issue.File,
			proposed: proposed,
		}, true

	case "MEM005":
		field := extractQuotedWord(issue.Message)
		if field == "" {
			return nil, false
		}
		proposed, err := addFrontmatterFieldBytes(issue.File, field)
		if err != nil {
			return nil, false
		}
		return &fixProposal{
			summary:  fmt.Sprintf("Add missing frontmatter field %q to %s", field, filepath.Base(issue.File)),
			kind:     fixInTUI,
			target:   issue.File,
			proposed: proposed,
		}, true

	case "MD003":
		content := readLineContent(issue.File, issue.Line)
		prompt := contextPreamble() + fmt.Sprintf(
			"In %s at line %d, the line is too long (%s). Shorten it without losing meaning. Preserve any links, code spans, and project-specific terminology:\n\n%s",
			issue.File, issue.Line, issue.Message, content,
		)
		return cli(fmt.Sprintf("Shorten line %d in %s", issue.Line, filepath.Base(issue.File)), issue.File, prompt), true

	case "MD004":
		prompt := contextPreamble() + fmt.Sprintf(
			"In %s at line %d, there is a broken markdown link (%s). Determine the correct target by reading the surrounding section and project layout, then either fix the link or remove it if the target no longer exists.",
			issue.File, issue.Line, issue.Message,
		)
		return cli(fmt.Sprintf("Fix broken link at line %d in %s", issue.Line, filepath.Base(issue.File)), issue.File, prompt), true

	case "MD005":
		prompt := contextPreamble() + fmt.Sprintf(
			"%s is too long (%s). Trim it to under 500 lines while preserving all critical content — build/test/release commands, gotchas, project-specific conventions, and any 'never do X' rules. When in doubt about whether a section is load-bearing, check whether it's referenced elsewhere in the project before removing it. Prefer tightening prose and consolidating duplicated guidance over deleting whole topics.",
			issue.File, issue.Message,
		)
		return cli(fmt.Sprintf("Trim %s to under 500 lines", filepath.Base(issue.File)), issue.File, prompt), true

	case "MD002":
		claudePath := filepath.Join(projectPath, "CLAUDE.md")
		prompt := fmt.Sprintf(
			"%s is empty. Inspect this project (README.md, package.json/go.mod/pyproject.toml, top-level directory layout, recent git history) and write a CLAUDE.md that captures: a one-paragraph project overview, the build/test/run commands, the project layout, and any conventions a contributor would need on day one. Keep it terse and accurate — do not invent features or guidelines that aren't supported by what's in the repo.",
			claudePath,
		)
		return cli("Populate empty CLAUDE.md", claudePath, prompt), true

	case "MEM001":
		prompt := fmt.Sprintf(
			"Create a MEMORY.md index file at %s for this project. It should be an empty memory index with just a level-1 heading — no entries yet.",
			issue.File,
		)
		return cli("Initialise MEMORY.md", issue.File, prompt), true

	case "MEM003":
		content := readLineContent(issue.File, issue.Line)
		prompt := contextPreamble("./MEMORY.md") + fmt.Sprintf(
			"In MEMORY.md at line %d, shorten this index entry to ≤150 characters without losing the key information. Keep the link target and any disambiguating noun phrase; trim adjectives and filler:\n\n%s",
			issue.Line, content,
		)
		return cli(fmt.Sprintf("Shorten MEMORY.md entry at line %d", issue.Line), issue.File, prompt), true

	case "MEM004":
		prompt := contextPreamble("./MEMORY.md") + fmt.Sprintf(
			"Fix the frontmatter in memory file %s: %s. Required fields: name, description, metadata.type. Preserve the body content unchanged.",
			issue.File, issue.Message,
		)
		return cli(fmt.Sprintf("Fix frontmatter in %s", filepath.Base(issue.File)), issue.File, prompt), true

	case "MEM006":
		prompt := contextPreamble("./MEMORY.md") + fmt.Sprintf(
			"Fix the 'type' field in %s — must be one of: user, feedback, project, reference. %s. Pick the value that best matches the memory's actual content; don't change the body.",
			issue.File, issue.Message,
		)
		return cli(fmt.Sprintf("Fix invalid type in %s", filepath.Base(issue.File)), issue.File, prompt), true
	}

	return nil, false
}

// nextApplicableReview returns the index of the first review result that has
// no error and hasn't been applied yet, or -1 if none qualify.
func nextApplicableReview(results []llmReviewResult) int {
	for i, r := range results {
		if r.err != nil || r.applied {
			continue
		}
		if strings.TrimSpace(r.content) == "" {
			continue
		}
		return i
	}
	return -1
}

// buildReviewApplyProposal turns an LLM review result into a fixClaudeCLI
// proposal that hands the feedback back to claude with instructions to apply
// the actionable suggestions, using the same approval + post-review cycle as
// per-issue fixes.
func buildReviewApplyProposal(r llmReviewResult) *fixProposal {
	prompt := fmt.Sprintf(
		"You previously produced this review of %s. Apply the actionable suggestions while preserving meaning and the file's existing structure.\n\nBefore editing, read ./CLAUDE.md (and ./MEMORY.md if the target is a memory file) so tone, scope, and conventions match the rest of the project. Do not delete content that's load-bearing for the project just because a suggestion is terse — when in doubt, tighten rather than remove.\n\nReview feedback:\n\n%s",
		r.path, r.content,
	)
	return &fixProposal{
		summary:   "Apply LLM review to " + filepath.Base(r.path),
		kind:      fixClaudeCLI,
		target:    r.path,
		cliPrompt: prompt,
		cliArgs:   claudeFixArgs(prompt),
	}
}

func claudeFixArgs(prompt string) []string {
	return []string{"--allowedTools", "Edit,Write,Read", "--permission-mode", "acceptEdits", "--print", prompt}
}

// removeFileLineBytes returns the file contents with the given 1-based line removed.
// Does not write to disk.
func removeFileLineBytes(path string, lineNum int) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return nil, fmt.Errorf("line %d out of range (file has %d lines)", lineNum, len(lines))
	}
	lines = append(lines[:lineNum-1], lines[lineNum:]...)
	return []byte(strings.Join(lines, "\n")), nil
}

// addFrontmatterFieldBytes returns the file contents with `field: ` inserted before the
// closing `---` of the YAML frontmatter block. Does not write to disk.
func addFrontmatterFieldBytes(path, field string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("no frontmatter found in %s", path)
	}
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return nil, fmt.Errorf("frontmatter block not closed in %s", path)
	}
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:closeIdx]...)
	newLines = append(newLines, field+": ")
	newLines = append(newLines, lines[closeIdx:]...)
	return []byte(strings.Join(newLines, "\n")), nil
}

func readLineContent(path string, lineNum int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return ""
	}
	return lines[lineNum-1]
}

// extractQuotedWord pulls the first double-quoted word from s (used to parse field names from lint messages).
func extractQuotedWord(s string) string {
	re := regexp.MustCompile(`"([^"]+)"`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}
