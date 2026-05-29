package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

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

	// In-flight CLI fix state. When fixRunning is true, the view renders a
	// spinner+elapsed panel instead of the lint list so the user has live
	// feedback while claude --print works. fixOutput captures combined
	// stdout/stderr for error display on failure. cliLog buffers the streamed
	// stdout/stderr lines so `L` can reveal them in a bordered panel.
	fixRunning   bool
	fixStartedAt time.Time
	fixTarget    string
	fixOutput    []byte
	fixCmd       *exec.Cmd // active subprocess; nil between runs. Used by the model's quit path to kill an in-flight `claude --print` instead of orphaning it.
	cliLog       []string
	showLog      bool

	// claudeOnPath is cached at view init: when false, LLM review and fix-via-CLI
	// are unavailable and the keys 'l' / 'f' / 'a' / 'F' surface a friendly
	// hint instead (the bulk F path gates only on fixClaudeCLI proposals — an
	// all-programmatic bulk works without the CLI).
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
	if line, ok := msg.(cliStreamLineMsg); ok {
		v.cliLog = appendStreamLine(v.cliLog, line.line, line.stderr)
		return nil
	}
	if done, ok := msg.(chatDoneMsg); ok && done.origin == tabDoctor {
		if done.err != nil {
			v.flash = styleErr.Render("chat session ended: " + done.err.Error())
		} else {
			v.flash = styleOK.Render("chat session ended — re-linting")
		}
		v.loaded = false
		return nil
	}
	if result, ok := msg.(doctorLLMResultMsg); ok {
		v.llmRunning = false
		v.llmResults = result.results
		v.showLLM = true
		v.top = 0
		v.flash = ""
		return nil
	}
	if done, ok := msg.(fixDoneMsg); ok && done.origin == tabDoctor {
		v.fixRunning = false
		v.fixCmd = nil
		v.fixOutput = done.output
		v.lastFixErr = done.err
		if done.err != nil {
			if reason := classifyClaudeFailure(done.output, done.err); reason != "" {
				v.flash = styleErr.Render("fix failed: " + reason)
			} else {
				v.flash = styleErr.Render("fix failed: " + enrichExitStatus(done.err.Error()))
				if tail := tailOutput(done.output, 12); tail != "" {
					v.flash += "\n" + styleDim.Render(tail)
				}
			}
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
			// For bulk CLI fixes, also check every other target — Claude can
			// reorder or skip files, so a clean primary doesn't mean the
			// whole batch was a no-op. If ANY target changed, treat the run
			// as successful and surface the primary diff (the other targets
			// are still listed in bulkTargets for revert coverage).
			bulkChanged := false
			if diff == "" && len(done.proposal.bulkTargets) > 0 {
				for _, t := range done.proposal.bulkTargets {
					if t == "" || t == done.proposal.target {
						continue
					}
					snap, ok := done.proposal.bulkSnapshots[t]
					if !ok || snap == "" {
						continue
					}
					before, err := os.ReadFile(snap)
					if err != nil {
						continue
					}
					afterT, err := os.ReadFile(t)
					if err != nil {
						continue
					}
					if d := unifiedDiff(string(before), string(afterT), 3); d != "" {
						bulkChanged = true
						diff = fmt.Sprintf("@@ primary %s unchanged — secondary %s edited @@\n", filepath.Base(done.proposal.target), filepath.Base(t)) + d
						break
					}
				}
			}
			if diff == "" && !bulkChanged {
				// Claude exited 0 but didn't change anything — costly no-op.
				// Surface the tail of stdout/stderr so the user can see WHY
				// (model declined, prompt mis-shaped, etc.) instead of getting
				// a silent shrug. Also drop bulk snapshots so we don't leak.
				deleteSnapshot(done.proposal.snapshotPath)
				for _, snap := range done.proposal.bulkSnapshots {
					if snap != "" {
						deleteSnapshot(snap)
					}
				}
				flash := styleErr.Render("claude CLI exited 0 but made no edits — token spend wasted")
				if tail := tailOutput(done.output, 8); tail != "" {
					flash += "\n" + styleDim.Render(tail)
				}
				v.flash = flash
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
			// Keep the change. The primary snapshot is preserved by default
			// so the user can still copy back from disk if they reconsider
			// later (GC sweeps 30-day-old entries). Bulk snapshots are also
			// kept on the same retention policy — listing them in the flash
			// would be noisy, so just count them.
			path := v.postReview.snapshotPath
			extra := len(v.postReview.bulkSnapshots)
			v.flash = styleOK.Render("kept: " + v.postReview.summary)
			if path != "" {
				v.flash += "  " + styleDim.Render("(snapshot: "+path+")")
			}
			if extra > 0 {
				v.flash += "  " + styleDim.Render(fmt.Sprintf("(+%d bulk snapshot(s))", extra))
			}
			v.postReview = nil
			v.previewDiff = ""
			v.previewScroll = 0
			v.lastFix = nil
			v.loaded = false
		case "u", "n", "esc":
			// Revert from disk snapshot (falls back to in-memory beforeBytes).
			// For bulk fixes, revertFromSnapshot walks bulkSnapshots too.
			bulkCount := len(v.postReview.bulkSnapshots)
			if err := v.revertFromSnapshot(v.postReview); err != nil {
				v.flash = styleErr.Render("revert failed: " + err.Error())
			} else {
				// Files are restored — snapshots are redundant, drop them all so GC has less to sweep.
				deleteSnapshot(v.postReview.snapshotPath)
				for _, snap := range v.postReview.bulkSnapshots {
					if snap != "" {
						deleteSnapshot(snap)
					}
				}
				if bulkCount > 0 {
					v.flash = styleOK.Render(fmt.Sprintf("reverted %d file(s): %s", bulkCount+1, v.postReview.summary))
				} else {
					v.flash = styleOK.Render("reverted: " + v.postReview.summary)
				}
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
	case "L":
		v.showLog = !v.showLog
		if v.showLog && len(v.cliLog) == 0 && !v.fixRunning {
			v.flash = styleDim.Render("(no CLI activity yet — log will populate once a fix runs)")
		}
	case "c":
		// Drop into an interactive `claude` session in the project root so
		// the user can talk through what just happened (or didn't), ask the
		// model questions, and apply manual follow-up edits. The TUI
		// suspends; on exit chatDoneMsg re-lints.
		if !v.claudeOnPath {
			v.flash = styleWarn.Render("chat follow-up unavailable — claude CLI not found in PATH")
			return nil
		}
		ctx := v.chatContextPrompt()
		return execChatCmd(v.st.project, ctx, tabDoctor)
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
			// Bundle every reviewable file into one Claude call. Per-file
			// iteration used to multiply the token bill for no marginal
			// benefit — a combined prompt asks Haiku to scan everything at
			// once and emit one structured response with per-file sections.
			opts := doctor.ReviewOptions{}
			var entries []doctor.BundleEntry
			var paths []string
			for _, p := range []string{claudePath, memPath} {
				data, err := os.ReadFile(p)
				if err != nil {
					// Missing file is not a hard error — include a stub so
					// the model can comment on its absence if relevant.
					continue
				}
				entries = append(entries, doctor.BundleEntry{Path: p, Content: string(data)})
				paths = append(paths, p)
			}
			if len(entries) == 0 {
				return doctorLLMResultMsg{results: []llmReviewResult{{
					path: claudePath,
					err:  fmt.Errorf("no reviewable files found at %s or %s", claudePath, memPath),
				}}}
			}
			content, err := doctor.ReviewBundle(entries, opts)
			label := strings.Join(paths, " + ")
			if err != nil {
				return doctorLLMResultMsg{results: []llmReviewResult{{path: label, err: err}}}
			}
			return doctorLLMResultMsg{results: []llmReviewResult{{path: label, content: content}}}
		}
	case "F":
		if v.llmRunning || numIssues == 0 {
			break
		}
		cur := v.allIssues[v.cursor]
		proposal, ok := buildDoctorBulkFixProposal(cur, v.allIssues, v.st.project)
		if !ok {
			v.flash = styleDim.Render("no bulk-fix available for " + cur.Code + " (need 2+ siblings, all programmatic or all CLI)")
			return nil
		}
		if proposal.kind == fixClaudeCLI && !v.claudeOnPath {
			v.flash = styleWarn.Render("bulk-fix unavailable — claude CLI not found in PATH")
			return nil
		}
		if v.showLLM {
			v.showLLM = false
		}
		if proposal.kind == fixClaudeCLI {
			v.previewDiff = buildCLIPromptPreview(proposal)
		} else {
			v.previewDiff = strings.Join(proposal.previewLines, "\n")
		}
		v.previewScroll = 0
		v.pendingFix = proposal
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
	// Bulk fixes snapshot every additional target so revert restores the
	// whole batch. A failure mid-snapshot aborts the run — partial coverage
	// would silently lose the ability to revert one of the files.
	if len(p.bulkTargets) > 0 {
		p.bulkSnapshots = make(map[string]string, len(p.bulkTargets))
		for _, t := range p.bulkTargets {
			if t == "" || t == p.target {
				continue
			}
			snap, err := snapshotForFix(t, snapDir)
			if err != nil {
				v.lastFixErr = err
				v.flash = styleErr.Render("snapshot " + filepath.Base(t) + ": " + err.Error())
				return nil
			}
			p.bulkSnapshots[t] = snap
		}
	}

	if p.kind == fixInTUI {
		// Programmatic bulk: hand off to the closure that walks every same-code
		// proposal. The closure takes its own snapshots and returns the path
		// map, which we fold into bulkSnapshots so the 'u' revert key works
		// on all files. The primary single-file snapshot taken above stays —
		// applyDoctorBulkProgrammaticFix will re-snapshot the primary target
		// (harmless, second snap just gets a sibling counter suffix); we
		// prefer that over teaching the closure to skip the primary because
		// it keeps the bulk path's snapshot logic self-contained.
		if p.bulkApplyFn != nil {
			snapDir := doctorSnapshotDir(v.st.paths.BackupsDir)
			applied, snaps, err := p.bulkApplyFn(snapDir)
			if err != nil {
				v.lastFixErr = err
				if applied == 0 {
					deleteSnapshot(p.snapshotPath)
					p.snapshotPath = ""
				}
				v.flash = styleErr.Render(fmt.Sprintf("bulk-fix failed after %d file(s): %s", applied, err.Error()))
				return nil
			}
			if p.bulkSnapshots == nil {
				p.bulkSnapshots = map[string]string{}
			}
			for path, snap := range snaps {
				p.bulkSnapshots[path] = snap
			}
			// Enter the postReview gate so the user can still 'u' to revert
			// the whole batch. Without this, the CHANGELOG's revert claim is
			// false for programmatic bulks — snapshots exist on disk but
			// have no in-TUI undo path. Synthesise a textual preview body
			// since there's no single-file diff to show.
			var preview strings.Builder
			fmt.Fprintf(&preview, "Bulk-applied %d file(s):\n\n", applied)
			for path := range p.bulkSnapshots {
				fmt.Fprintf(&preview, "  %s\n", path)
			}
			v.postReview = p
			v.previewDiff = preview.String()
			v.previewScroll = 0
			v.flash = styleOK.Render(fmt.Sprintf("bulk-fixed %d file(s) — y keep / u revert", applied))
			v.loaded = false
			return nil
		}
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
	// Trust the cached init state: if claudeOnPath was true at startup, hand
	// the command off even when LookPath now fails. exec.Command stores the
	// bare name and resolves it at process-start (inside CombinedOutput), and
	// tests stub execFixCmd so no real spawn happens in unit tests.
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
	// Mark in-flight so render() shows the spinner panel until fixDoneMsg arrives.
	// The global spinner in model.go is already self-ticking, which drives the
	// per-second elapsed counter without any extra scheduling here.
	v.fixRunning = true
	v.fixStartedAt = time.Now()
	v.fixTarget = p.target
	v.fixOutput = nil
	v.fixCmd = cmd
	v.cliLog = v.cliLog[:0]
	return execFixCmd(cmd, p, tabDoctor)
}

// revertFromSnapshot restores p.target to its pre-fix state. Prefers the on-disk snapshot;
// falls back to in-memory beforeBytes if the snapshot is unreadable (or empty).
func (v *doctorView) revertFromSnapshot(p *fixProposal) error {
	if p == nil || p.target == "" {
		return fmt.Errorf("nothing to revert")
	}
	primary := func() error {
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
	if err := primary(); err != nil {
		return err
	}
	// Restore every bulk target. Errors are aggregated so the user sees which
	// files couldn't be reverted; a single failure doesn't leave the rest dirty.
	var failed []string
	for path, snap := range p.bulkSnapshots {
		if snap == "" {
			// snapshotForFix returns "" when the target didn't exist at
			// snapshot time (e.g. MEM001 bulk on a missing MEMORY.md). No
			// pre-state to restore — the right "revert" is to delete the
			// file we wrote. Best-effort; missing files are fine.
			_ = os.Remove(path)
			continue
		}
		data, err := os.ReadFile(snap)
		if err != nil {
			failed = append(failed, filepath.Base(path)+" (snapshot read: "+err.Error()+")")
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			failed = append(failed, filepath.Base(path)+" (write: "+err.Error()+")")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("bulk revert failed for: %s", strings.Join(failed, ", "))
	}
	return nil
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

// enrichExitStatus rewrites bare "exit status N" messages into something the
// user can act on. Stderr is now captured (see execFixCmd) and surfaced as a
// trailing dim line, so we just clarify what exit code we got.
func enrichExitStatus(msg string) string {
	re := regexp.MustCompile(`^exit status (\d+)$`)
	if m := re.FindStringSubmatch(strings.TrimSpace(msg)); m != nil {
		return fmt.Sprintf("claude CLI exit %s", m[1])
	}
	return msg
}

// tailOutput returns the last `maxLines` non-empty trimmed lines of out,
// joined with newlines, suitable for inline rendering under an error flash.
// Empty input returns "".
func tailOutput(out []byte, maxLines int) string {
	if len(out) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
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
	if !v.loaded && !v.llmRunning && !v.fixRunning {
		v.runLint()
	}

	if v.fixRunning {
		target := filepath.Base(v.fixTarget)
		if target == "" || target == "." {
			target = "selected file"
		}
		head := "Doctor — " + v.st.spinnerFrame +
			styleProgress.Render(fmt.Sprintf("Applying LLM fix to %s… (%s)", target, fixElapsed(v.fixStartedAt))) +
			"\n" + styleDim.Render("running claude --print non-interactively; L: toggle live log, q to quit") +
			"\n" + v.flash
		if v.showLog {
			head += "\n" + renderStreamPanel("Live CLI activity (last 10 lines):", v.cliLog, 10, v.w)
		}
		return head
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

	// Build any sticky-below panel first, capped to a budget so the panel — and
	// crucially its action-prompt footer — always fits. The model-level clamp
	// trims from the BOTTOM, so an un-capped panel would lose its confirm prompt
	// on a short terminal. The budget leaves room for the header chrome that
	// pageHeight() already accounts for plus a few list rows.
	panelBudget := v.pageHeight() - 3
	if panelBudget < 4 {
		panelBudget = 4
	}
	var panel string
	if v.pendingFix != nil {
		panel = v.renderPreviewPanel("Fix: "+v.pendingFix.summary, v.previewDiff, "Apply? y   Cancel? n / esc   j/k: scroll", panelBudget)
	} else if v.postReview != nil {
		panel = v.renderPreviewPanel(
			"Applied: "+v.postReview.summary,
			v.previewDiff,
			"Keep? y   Revert? u / n / esc   j/k: scroll",
			panelBudget,
		)
	}

	// Scroll to keep cursor visible. Size the list window from the panel's ACTUAL
	// height (already capped to panelBudget) so list + panel fit the body.
	pageH := v.pageHeight()
	if panel != "" {
		pageH -= strings.Count(panel, "\n") + 1
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

	if panel != "" {
		b.WriteString(panel)
	}

	return b.String()
}

// renderPreviewPanel renders the bordered diff/prompt panel used by both pre-apply
// (pendingFix) and post-apply (postReview) gates. body is a diff or prompt block;
// it is split, scrolled by v.previewScroll, and colorized by leading char (+/-/@@).
func (v *doctorView) renderPreviewPanel(title, body, footer string, maxLines int) string {
	var sb strings.Builder
	sb.WriteString(styleDim.Render(strings.Repeat("─", maxInt(44, v.w-2))))
	sb.WriteString("\n")
	sb.WriteString(title + "\n")

	// Reserve 4 lines of chrome: border, title, the "…N more" line, and footer.
	maxPanel := maxLines - 4
	if maxPanel < 1 {
		maxPanel = 1
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
	if v.showLLM {
		return "r: re-run lint  l: LLM review  a: apply review  f: fix  F: bulk-fix  L: toggle log  c: chat  j/k: scroll  g/G: top/bottom" + suffix
	}
	return "r: re-run lint  l: LLM review  j/k: navigate  f: fix  F: bulk-fix  L: toggle log  c: chat  g/G: top/bottom" + suffix
}

func (v *doctorView) capturingInput() bool { return false }

// chatContextPrompt builds a short context block appended to the system
// prompt of the follow-up interactive claude session, so the model knows
// what the user was just doing in ccmcp. Best-effort — when there's no
// fresh fix context, an empty string is returned and the chat session
// starts unprimed.
func (v *doctorView) chatContextPrompt() string {
	var b strings.Builder
	b.WriteString("The user just dropped out of the ccmcp TUI Doctor tab. ")
	switch {
	case v.lastFix != nil && v.lastFixErr != nil:
		fmt.Fprintf(&b, "Their last fix attempt was %q on %s and it failed: %s. ",
			v.lastFix.summary, v.lastFix.target, v.lastFixErr.Error())
	case v.postReview != nil:
		fmt.Fprintf(&b, "They just applied a fix (%q on %s) and are reviewing the diff. ",
			v.postReview.summary, v.postReview.target)
	case len(v.allIssues) > 0 && v.cursor < len(v.allIssues):
		iss := v.allIssues[v.cursor]
		fmt.Fprintf(&b, "They had cursor on lint issue %s (%s:%d): %s. ",
			iss.Code, iss.File, iss.Line, iss.Message)
	default:
		b.WriteString("They want to discuss the current project's CLAUDE.md / MEMORY.md / agent files. ")
	}
	b.WriteString("Help them act on whatever they want to do next. The cwd is the project root; you have full Edit access.")
	return b.String()
}

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
		wrapped := wrapImperativeFixPrompt(target, prompt)
		return &fixProposal{
			summary:      summary,
			kind:         fixClaudeCLI,
			target:       target,
			cliPrompt:    wrapped,
			cliPromptRaw: prompt,
			cliArgs:      claudeFixArgs(wrapped),
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
		// Programmatic: when the broken link's line is a self-contained list
		// entry whose only purpose is to point at the missing file (matches
		// `^\s*-\s+\[.*]\(.*\)\s*(—.*)?$`), drop the line. Anything else (inline
		// reference inside a paragraph, multi-link line) falls back to the CLI.
		if line := readLineContent(issue.File, issue.Line); isStandaloneLinkLine(line) {
			proposed, err := removeFileLineBytes(issue.File, issue.Line)
			if err == nil {
				return &fixProposal{
					summary:  fmt.Sprintf("Remove broken-link line %d in %s", issue.Line, filepath.Base(issue.File)),
					kind:     fixInTUI,
					target:   issue.File,
					proposed: proposed,
				}, true
			}
		}
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
		// Programmatic — the lint accepts any non-empty MEMORY.md, so writing
		// a single-heading skeleton is enough to clear it. No LLM judgment
		// needed; the file genuinely starts empty.
		return &fixProposal{
			summary:  "Initialise MEMORY.md (empty index)",
			kind:     fixInTUI,
			target:   issue.File,
			proposed: []byte("# Memory\n\n_No entries yet._\n"),
		}, true

	case "MEM003":
		content := readLineContent(issue.File, issue.Line)
		prompt := contextPreamble("./MEMORY.md") + fmt.Sprintf(
			"In MEMORY.md at line %d, shorten this index entry to ≤150 characters without losing the key information. Keep the link target and any disambiguating noun phrase; trim adjectives and filler:\n\n%s",
			issue.Line, content,
		)
		return cli(fmt.Sprintf("Shorten MEMORY.md entry at line %d", issue.Line), issue.File, prompt), true

	case "MEM004":
		// Programmatic when the lint reports "missing frontmatter (expected
		// --- at line 1)" — we can prepend a minimal block derived from the
		// filename. Other MEM004 messages ("cannot read", "frontmatter block
		// not closed") need human judgment.
		if strings.Contains(issue.Message, "missing frontmatter") {
			proposed, err := prependMinimalFrontmatterBytes(issue.File)
			if err == nil {
				return &fixProposal{
					summary:  fmt.Sprintf("Prepend minimal frontmatter to %s", filepath.Base(issue.File)),
					kind:     fixInTUI,
					target:   issue.File,
					proposed: proposed,
				}, true
			}
		}
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

// buildDoctorBulkFixProposal collects every issue sharing `cursor.Code` and
// returns a proposal that resolves the whole category in one keystroke. Two
// flavours:
//
//   - When the per-issue fixes are programmatic (fixInTUI), apply each fix's
//     proposed bytes in order, after taking a snapshot per file. The proposal
//     carries no `proposed` payload itself; bulk apply is handled by the F
//     handler directly (see applyDoctorBulkProgrammaticFix).
//   - When the per-issue fixes are fixClaudeCLI, build ONE bundled prompt that
//     instructs Claude to edit every target file in sequence. The proposal's
//     `target` is the first file; `bulkTargets` carries the rest for
//     snapshot/revert coverage.
//
// Returns (nil, false) when fewer than 2 issues share the code (no point
// bulk-fixing one), when no per-issue fix is available, or when the per-issue
// fixes are a mix of programmatic + CLI (refuse rather than guess).
func buildDoctorBulkFixProposal(cursor doctor.Issue, allIssues []doctor.Issue, projectPath string) (*fixProposal, bool) {
	var siblings []doctor.Issue
	for _, iss := range allIssues {
		if iss.Code == cursor.Code {
			siblings = append(siblings, iss)
		}
	}
	if len(siblings) < 2 {
		return nil, false
	}

	// Build per-issue proposals to discover homogeneity + collect targets.
	var proposals []*fixProposal
	for _, iss := range siblings {
		p, ok := buildFixProposal(iss, projectPath)
		if !ok {
			return nil, false
		}
		proposals = append(proposals, p)
	}

	allInTUI := true
	allCLI := true
	for _, p := range proposals {
		if p.kind != fixInTUI {
			allInTUI = false
		}
		if p.kind != fixClaudeCLI {
			allCLI = false
		}
	}

	switch {
	case allInTUI:
		// Programmatic bulk: stage all writes onto a closure so the same
		// y/n approval pipeline as single fixes can gate them, then apply in
		// one shot when the user confirms.
		preview := []string{
			fmt.Sprintf("Bulk-fix %d %s issues programmatically:", len(proposals), cursor.Code),
			"",
		}
		var targets []string
		seenT := map[string]bool{}
		for _, p := range proposals {
			preview = append(preview, "  "+p.summary)
			if !seenT[p.target] {
				seenT[p.target] = true
				targets = append(targets, p.target)
			}
		}
		preview = append(preview, "", "Apply all? y / n / esc")
		code := cursor.Code
		issuesCopy := append([]doctor.Issue(nil), allIssues...)
		return &fixProposal{
			summary:      fmt.Sprintf("Bulk-fix %d %s issues", len(proposals), cursor.Code),
			kind:         fixInTUI,
			target:       proposals[0].target,
			bulkTargets:  targets,
			previewLines: preview,
			bulkApplyFn: func(snapDir string) (int, map[string]string, error) {
				return applyDoctorBulkProgrammaticFix(code, issuesCopy, projectPath, snapDir)
			},
		}, true
	case allCLI:
		// CLI bulk: bundle every per-issue prompt body (unwrapped) into one
		// outer envelope so the imperative preamble is not repeated per fix.
		// Using cliPromptRaw avoids the double-wrap that contradicted the
		// outer multi-target instructions.
		var body strings.Builder
		fmt.Fprintf(&body, "Apply the following %d fixes for code %s. Each item names a file path and the change to make:\n\n", len(proposals), cursor.Code)
		var files []string
		seen := map[string]bool{}
		for i, p := range proposals {
			raw := p.cliPromptRaw
			if raw == "" {
				raw = p.cliPrompt
			}
			fmt.Fprintf(&body, "── Fix %d/%d (target: %s) ──\n%s\n\n", i+1, len(proposals), p.target, raw)
			if !seen[p.target] {
				seen[p.target] = true
				files = append(files, p.target)
			}
		}
		wrapped := wrapImperativeFixPrompt("", body.String())
		extras := files[1:]
		preview := []string{
			fmt.Sprintf("Bundle %d %s fixes into a single Claude invocation:", len(proposals), cursor.Code),
			"",
		}
		for _, f := range files {
			preview = append(preview, "  "+f)
		}
		preview = append(preview, "", fmt.Sprintf("Model: %s   Max-turns: 4", doctor.DefaultAnthropicModel), "", "Apply? y / n / esc")
		return &fixProposal{
			summary:      fmt.Sprintf("Bulk-fix %d %s issues via Claude", len(proposals), cursor.Code),
			kind:         fixClaudeCLI,
			target:       files[0],
			bulkTargets:  extras,
			cliPrompt:    wrapped,
			cliArgs:      claudeFixArgs(wrapped),
			previewLines: preview,
		}, true
	}
	// Mixed (some programmatic, some CLI) — refuse rather than silently
	// degrade. The user can press `f` per row to walk through them.
	return nil, false
}

// applyDoctorBulkProgrammaticFix applies every same-code programmatic proposal
// to disk, taking one snapshot per target file. Issues are sorted by line
// descending within each target so line-removal operations (MEM002) don't
// invalidate later line numbers, and the per-issue proposal is rebuilt
// against current disk state after each write so cumulative edits stack
// correctly. Errors stop the run mid-batch.
func applyDoctorBulkProgrammaticFix(code string, allIssues []doctor.Issue, projectPath, snapDir string) (applied int, snapshots map[string]string, err error) {
	snapshots = map[string]string{}

	// Filter + group by target.
	byTarget := map[string][]doctor.Issue{}
	var order []string
	for _, iss := range allIssues {
		if iss.Code != code {
			continue
		}
		if _, ok := byTarget[iss.File]; !ok {
			order = append(order, iss.File)
		}
		byTarget[iss.File] = append(byTarget[iss.File], iss)
	}

	for _, target := range order {
		// Snapshot once per target, before any of its writes.
		snap, sErr := snapshotForFix(target, snapDir)
		if sErr != nil {
			return applied, snapshots, fmt.Errorf("snapshot %s: %w", filepath.Base(target), sErr)
		}
		snapshots[target] = snap

		// Sort issues within this target by Line descending so removals
		// don't shift later line numbers. Issues without a meaningful line
		// (Line==0, e.g. MEM005 frontmatter fixes) sort last.
		issues := byTarget[target]
		sort.SliceStable(issues, func(i, j int) bool {
			return issues[i].Line > issues[j].Line
		})

		for _, iss := range issues {
			// Rebuild against current disk state so the previous write is
			// honoured by `removeFileLineBytes` / `addFrontmatterFieldBytes`.
			p, ok := buildFixProposal(iss, projectPath)
			if !ok || p.kind != fixInTUI {
				continue
			}
			if wErr := os.WriteFile(p.target, p.proposed, 0o644); wErr != nil {
				return applied, snapshots, fmt.Errorf("write %s: %w", filepath.Base(p.target), wErr)
			}
			applied++
		}
	}
	return applied, snapshots, nil
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
	wrapped := wrapImperativeFixPrompt(r.path, prompt)
	return &fixProposal{
		summary:      "Apply LLM review to " + filepath.Base(r.path),
		kind:         fixClaudeCLI,
		target:       r.path,
		cliPrompt:    wrapped,
		cliPromptRaw: prompt,
		cliArgs:      claudeFixArgs(wrapped),
	}
}

func claudeFixArgs(prompt string) []string {
	args := []string{"--allowedTools", "Edit,Write,Read", "--permission-mode", "acceptEdits"}
	args = append(args, claudeFixModelArgs()...)
	args = append(args, "--print", prompt)
	return args
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

// isStandaloneLinkLine reports whether a markdown line is "just a link entry"
// safe to delete on broken-link cleanup — a list-item line whose interesting
// content is one `[text](target)` reference, optionally followed by a short
// dash-separated description. Defensively requires the line to start with
// list-bullet whitespace so we don't nuke prose paragraphs.
func isStandaloneLinkLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Must look like a list bullet.
	if !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ") {
		return false
	}
	// Must contain exactly one markdown link.
	linkRe := regexp.MustCompile(`\[[^\]]+\]\([^)]+\)`)
	if matches := linkRe.FindAllString(trimmed, -1); len(matches) != 1 {
		return false
	}
	return true
}

// prependMinimalFrontmatterBytes returns `path`'s contents with a minimal YAML
// frontmatter block prepended. `name` is derived from the basename (sans
// extension), `description` is empty, and metadata.type is left empty for the
// user to fill in. The body is preserved verbatim. Refuses to prepend when
// the file already starts with `---` so a stale MEM004 issue can't produce
// double-frontmatter that breaks the next lint pass.
func prependMinimalFrontmatterBytes(path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(bytes.TrimLeft(body, " \t\r\n"), []byte("---")) {
		return nil, fmt.Errorf("file already has frontmatter")
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	fm := fmt.Sprintf("---\nname: %s\ndescription:\nmetadata:\n  type:\n---\n\n", name)
	return append([]byte(fm), body...), nil
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
