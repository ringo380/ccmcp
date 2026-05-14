package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// seedOrphanOverride installs a per-project disabledMcpServers entry that
// classify.Classify will bucket as OrphanStdio (plain name, no source on disk).
func seedOrphanOverride(t *testing.T, st *state, key string) {
	t.Helper()
	st.cj.AddProjectDisabledMcpServer(st.project, key)
}

// TestSummaryCursorLandsOnFixableRow verifies that after switching to the
// Summary tab with one orphan override seeded, the cursor (▶) renders next
// to the fixable row and the help footer mentions the f/l keys.
func TestSummaryCursorLandsOnFixableRow(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-orphan-row")

	m := newModel(st)
	out := drive(m, "9")

	clean := stripANSI(out)
	if !strings.Contains(clean, "1 fixable issue(s)") {
		t.Fatalf("expected fixable count, got:\n%s", clean)
	}
	if !strings.Contains(clean, "▶") {
		t.Fatalf("expected cursor marker, got:\n%s", clean)
	}
	if !strings.Contains(clean, "ghost-orphan-row") {
		t.Fatalf("expected the orphan key in output:\n%s", clean)
	}
	if !strings.Contains(clean, "f: fix issue") {
		t.Fatalf("expected help footer to mention f, got:\n%s", clean)
	}
}

// TestSummaryFixOrphanOverride drives `9 f y` and asserts the orphan override
// is removed from the in-memory ClaudeJSON and dirtyClaude is set.
func TestSummaryFixOrphanOverride(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-to-prune")

	m := newModel(st)
	// 9 = Summary tab, f = open confirm, y = apply
	drive(m, "9", "f", "y")

	overrides := st.cj.ProjectDisabledMcpServers(st.project)
	for _, k := range overrides {
		if k == "ghost-to-prune" {
			t.Fatalf("orphan key still present in overrides: %v", overrides)
		}
	}
	if !st.dirtyClaude {
		t.Fatalf("expected dirtyClaude to be set after in-memory fix")
	}
}

// TestSummaryFixCancelLeavesStateUnchanged: pressing `n` at the confirm gate
// must not modify state.
func TestSummaryFixCancelLeavesStateUnchanged(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-cancel")
	before := append([]string{}, st.cj.ProjectDisabledMcpServers(st.project)...)

	m := newModel(st)
	drive(m, "9", "f", "n")

	after := st.cj.ProjectDisabledMcpServers(st.project)
	if len(before) != len(after) {
		t.Fatalf("cancel mutated overrides: before=%v after=%v", before, after)
	}
	if st.dirtyClaude {
		t.Fatalf("dirtyClaude should not be set on cancel")
	}
}

// TestSummaryFixWithoutFixableRowsNoOps: when there are no actionable issues,
// `f` flashes a friendly message instead of crashing the cursor lookup.
func TestSummaryFixWithoutFixableRowsNoOps(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	out := drive(m, "9", "f")
	if !strings.Contains(stripANSI(out), "no fixable issues") {
		t.Fatalf("expected friendly flash, got:\n%s", out)
	}
}

// TestSummaryLLMReviewRendersResponse stubs claudeReviewCmd and asserts the
// response text lands in the rendered output.
func TestSummaryLLMReviewRendersResponse(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-llm-target")

	orig := claudeReviewCmd
	t.Cleanup(func() { claudeReviewCmd = orig })
	claudeReviewCmd = func(workdir, prompt string) (string, error) {
		return "Verdict: yes, the orphan override is safe to remove.\nReason: classify bucket is OrphanStdio.", nil
	}

	m := newModel(st)
	m.summary.claudeOnPath = true
	drive(m, "9")

	var im tea.Model = m
	im, cmd := im.Update(key("l"))
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}
	view := stripANSI(im.View())
	if !strings.Contains(view, "LLM review — Orphan stdio override") {
		t.Fatalf("expected review header in output:\n%s", view)
	}
	if !strings.Contains(view, "Verdict: yes") {
		t.Fatalf("expected review content in output:\n%s", view)
	}
}

// TestDoctorFixRunsInTUIWithSpinner asserts that a CLI fix sets fixRunning,
// renders the spinner panel, and the result handler clears the flag.
func TestDoctorFixRunsInTUIWithSpinner(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)

	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })

	// Stub returns a tea.Cmd that we can manually invoke to simulate completion.
	var captured *fixProposal
	var capturedOrigin tabID
	execFixCmd = func(cmd *exec.Cmd, p *fixProposal, origin tabID) tea.Cmd {
		captured = p
		capturedOrigin = origin
		// Simulate claude rewriting the file.
		_ = os.WriteFile(p.target, []byte("# rewritten\n"), 0o644)
		return func() tea.Msg {
			return fixDoneMsg{err: nil, proposal: p, origin: origin}
		}
	}

	m := newModel(st)
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub for spinner test",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	m.doctor.pendingFix = prop

	drive(m, "0") // Doctor tab

	// Press 'y'. executeFix sets fixRunning=true and returns a tea.Cmd.
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if !m.doctor.fixRunning {
		t.Fatalf("expected fixRunning=true after executeFix")
	}
	if capturedOrigin != tabDoctor {
		t.Fatalf("expected origin=tabDoctor, got %d", capturedOrigin)
	}
	if captured == nil {
		t.Fatalf("execFixCmd was not called")
	}
	// Render reflects in-flight state.
	view := stripANSI(im.View())
	if !strings.Contains(view, "Applying LLM fix to MEMORY.md") {
		t.Fatalf("expected in-flight progress panel, got:\n%s", view)
	}

	// Drive completion message.
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}
	if m.doctor.fixRunning {
		t.Fatalf("expected fixRunning=false after fixDoneMsg")
	}
	view = stripANSI(im.View())
	if !strings.Contains(view, "Keep? y") {
		t.Fatalf("expected post-review gate, got:\n%s", view)
	}
}

// TestDoctorFixErrorSurfacesOutputInline: when the stub returns an error +
// output, the error flash should include a tail of the captured output —
// no terminal handoff means stderr no longer prints "above" the TUI.
func TestDoctorFixErrorSurfacesOutputInline(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)

	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })
	execFixCmd = func(cmd *exec.Cmd, p *fixProposal, origin tabID) tea.Cmd {
		return func() tea.Msg {
			return fixDoneMsg{
				err:      &exec.ExitError{}, // any non-nil
				proposal: p,
				output:   []byte("Error: missing API key\nclaude: see --help for usage"),
				origin:   origin,
			}
		}
	}

	m := newModel(st)
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub failure",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	m.doctor.pendingFix = prop

	drive(m, "0")
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}
	view := stripANSI(im.View())
	if !strings.Contains(view, "fix failed") {
		t.Fatalf("expected fail flash, got:\n%s", view)
	}
	if !strings.Contains(view, "missing API key") {
		t.Fatalf("expected captured stderr tail in flash, got:\n%s", view)
	}
}

// TestSummaryStashGhostFix asserts a stash ghost can be removed via cursor+f.
func TestSummaryStashGhostFix(t *testing.T) {
	st, _ := buildState(t)
	// Put a stash entry that doesn't exist anywhere else.
	st.stash.Put("ghost-stash-only", map[string]any{"command": "echo"})
	// And add it as an override so it becomes a StashGhost bucket member.
	st.cj.AddProjectDisabledMcpServer(st.project, "ghost-stash-only")

	m := newModel(st)
	out := drive(m, "9")
	if !strings.Contains(stripANSI(out), "stash ghost") {
		t.Fatalf("expected stash-ghost row, got:\n%s", out)
	}

	// Apply the fix.
	drive(m, "9", "f", "y")

	overrides := st.cj.ProjectDisabledMcpServers(st.project)
	for _, k := range overrides {
		if k == "ghost-stash-only" {
			t.Fatalf("stash ghost override not removed: %v", overrides)
		}
	}
}
