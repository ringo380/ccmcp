package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

// seedBadMemory writes a MEMORY.md with one broken index entry that will trigger MEM002.
// Returns the file path.
func seedBadMemory(t *testing.T, st *state) string {
	t.Helper()
	memDir := tuiMemoryPath(st.paths.ClaudeConfigDir, st.project)
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mem := filepath.Join(memDir, "MEMORY.md")
	body := "# MEMORY\n\n- [Good](good.md) - kept\n- [Broken](missing.md) - broken target\n"
	if err := os.WriteFile(mem, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed referenced memory files so the "Good" link resolves but "missing.md" doesn't.
	if err := os.WriteFile(filepath.Join(memDir, "good.md"), []byte("---\nname: good\ndescription: ok\nmetadata:\n  type: project\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return mem
}

func TestDoctorInTUIFixShowsDiffAndRequiresApproval(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)
	origBytes, _ := os.ReadFile(mem)

	m := newModel(st)
	// Doctor tab is "0" (key dispatch in model.go - Discover sits at 9).
	out := drive(m, "t", "]", "]", "]")
	if !strings.Contains(stripANSI(out), "MEMORY.md") {
		t.Fatalf("doctor tab did not render MEMORY.md group:\n%s", out)
	}

	// Navigate to the MEM002 issue and press 'f' to open the preview panel.
	// allIssues is filled in render(), so re-issue any key to be safe.
	out = drive(m, "t", "]", "]", "]", "G", "f")
	clean := stripANSI(out)
	if !strings.Contains(clean, "Apply? y") {
		t.Fatalf("expected pre-apply panel, got:\n%s", clean)
	}
	if !strings.Contains(clean, "-- [Broken]") && !strings.Contains(clean, "-Broken") && !strings.Contains(clean, "Broken") {
		t.Fatalf("expected the broken line in the diff, got:\n%s", clean)
	}

	// Press 'n' - file must be unchanged.
	out = drive(m, "t", "]", "]", "]", "G", "f", "n")
	got, _ := os.ReadFile(mem)
	if string(got) != string(origBytes) {
		t.Fatalf("file changed after 'n': before=%q after=%q", origBytes, got)
	}
	if strings.Contains(stripANSI(out), "Apply? y") {
		t.Fatalf("panel should be gone after 'n'")
	}

	// Press 'f' then 'y' - file must be modified and snapshot must exist.
	out = drive(m, "t", "]", "]", "]", "G", "f", "y")
	got, _ = os.ReadFile(mem)
	if string(got) == string(origBytes) {
		t.Fatalf("file unchanged after 'y'; expected line removed")
	}
	if strings.Contains(string(got), "missing.md") {
		t.Fatalf("broken link line not removed:\n%s", got)
	}

	// Snapshot exists under ~/.claude-mcp-backups/doctor/.
	snapDir := doctorSnapshotDir(st.paths.BackupsDir)
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		t.Fatalf("snapshot dir missing: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected at least one snapshot in %s", snapDir)
	}
	snapBytes, _ := os.ReadFile(filepath.Join(snapDir, entries[0].Name()))
	if string(snapBytes) != string(origBytes) {
		t.Fatalf("snapshot does not match pre-fix bytes:\n  snap=%q\n  orig=%q", snapBytes, origBytes)
	}
	if !strings.Contains(stripANSI(out), "fixed") {
		t.Fatalf("expected 'fixed' flash, got:\n%s", out)
	}
}

func TestDoctorCLIFlowSimulatedPostReviewRevert(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)
	origBytes, _ := os.ReadFile(mem)

	// Substitute execFixCmd with a stub that simulates Claude rewriting the file.
	// Restore the original on test exit.
	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })

	modified := []byte("# MEMORY (rewritten by stub)\n")
	execFixCmd = func(_cmd *exec.Cmd, p *fixProposal, _ tabID) tea.Cmd {
		_ = os.WriteFile(p.target, modified, 0o644)
		return func() tea.Msg {
			return fixDoneMsg{err: nil, proposal: p, origin: tabDoctor}
		}
	}

	// Force claudeOnPath true via direct field access on the view to bypass PATH lookup.
	m := newModel(st)
	m.tweaks.doctor.claudeOnPath = true

	// Forge a CLI-kind issue by overriding the fix proposal source: easiest path is to
	// build a proposal manually and assign it to pendingFix, then simulate 'y'.
	prop := &fixProposal{
		summary:   "stub CLI fix",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "rewrite this file",
		cliArgs:   []string{"--print", "x"},
	}
	m.tweaks.doctor.pendingFix = prop
	m.tweaks.doctor.previewDiff = buildCLIPromptPreview(prop)

	// Drive into the doctor tab and approve.
	drive(m, "t", "]", "]", "]")
	// 'y' triggers executeFix, which our stub runs synchronously.
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if cmd != nil {
		// Run the returned command (it returns fixDoneMsg synchronously in our stub).
		msg := cmd()
		im, _ = im.Update(msg)
	}

	// Post-review gate should now be active.
	view := stripANSI(im.View())
	if !strings.Contains(view, "Keep? y") {
		t.Fatalf("expected post-review panel, got:\n%s", view)
	}

	// File on disk is the modified bytes right now.
	got, _ := os.ReadFile(mem)
	if string(got) != string(modified) {
		t.Fatalf("expected stub modification on disk, got:\n%s", got)
	}

	// Press 'u' to revert.
	im, _ = im.Update(key("u"))
	got, _ = os.ReadFile(mem)
	if string(got) != string(origBytes) {
		t.Fatalf("revert did not restore original bytes:\n  got=%q\n  want=%q", got, origBytes)
	}
	if !strings.Contains(stripANSI(im.View()), "reverted") {
		t.Fatalf("expected 'reverted' flash")
	}
}

func TestDoctorCLIFixRunsFromProjectRoot(t *testing.T) {
	st, _ := buildState(t)
	seedBadMemory(t, st)

	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })

	var capturedDir string
	execFixCmd = func(cmd *exec.Cmd, p *fixProposal, _ tabID) tea.Cmd {
		capturedDir = cmd.Dir
		return func() tea.Msg { return fixDoneMsg{err: nil, proposal: p, origin: tabDoctor} }
	}

	m := newModel(st)
	m.tweaks.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub",
		kind:      fixClaudeCLI,
		target:    filepath.Join(st.project, "CLAUDE.md"),
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	_ = os.WriteFile(prop.target, []byte("# placeholder\n"), 0o644)
	m.tweaks.doctor.pendingFix = prop

	drive(m, "t", "]", "]", "]")
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if cmd != nil {
		_ = cmd()
	}
	_ = im

	if capturedDir != st.project {
		t.Fatalf("cmd.Dir not set to project root: got %q want %q", capturedDir, st.project)
	}
}

func TestDoctorApplyReviewBuildsCLIProposal(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)

	m := newModel(st)
	m.tweaks.doctor.claudeOnPath = true
	m.tweaks.doctor.showLLM = true
	m.tweaks.doctor.llmResults = []llmReviewResult{
		{path: mem, content: "- Shorten the broken-link line.\n- Verdict: minor cleanup needed.\n"},
	}

	drive(m, "t", "]", "]", "]")
	var im tea.Model = m
	im, _ = im.Update(key("a"))

	dv := m.tweaks.doctor
	if dv.pendingFix == nil {
		t.Fatalf("expected pendingFix to be set after 'a'")
	}
	if dv.pendingFix.kind != fixClaudeCLI {
		t.Fatalf("expected fixClaudeCLI kind, got %v", dv.pendingFix.kind)
	}
	if dv.pendingFix.target != mem {
		t.Fatalf("target mismatch: got %q want %q", dv.pendingFix.target, mem)
	}
	if !strings.Contains(dv.pendingFix.cliPrompt, "Apply the actionable suggestions") {
		t.Fatalf("prompt missing apply instruction:\n%s", dv.pendingFix.cliPrompt)
	}
	if !strings.Contains(dv.pendingFix.cliPrompt, "CLAUDE.md") {
		t.Fatalf("prompt missing project-context hint:\n%s", dv.pendingFix.cliPrompt)
	}
	if dv.llmResults[0].applied {
		t.Fatalf("expected applied=false after 'a' (must defer until approval)")
	}
	if dv.appliedReviewIdx != 0 {
		t.Fatalf("expected appliedReviewIdx=0 after 'a', got %d", dv.appliedReviewIdx)
	}
	if dv.showLLM {
		t.Fatalf("expected showLLM to flip off so the diff panel renders")
	}
	view := stripANSI(im.View())
	if !strings.Contains(view, "Apply? y") {
		t.Fatalf("expected pre-apply panel in view:\n%s", view)
	}

	// Cancel with 'n' - review must NOT be marked applied, showLLM must restore,
	// and 'a' must reopen the same review.
	im, _ = im.Update(key("n"))
	if dv.llmResults[0].applied {
		t.Fatalf("expected applied=false after canceling with 'n'")
	}
	if !dv.showLLM {
		t.Fatalf("expected showLLM=true after canceling 'a' to return user to review")
	}
	if dv.appliedReviewIdx != -1 {
		t.Fatalf("expected appliedReviewIdx=-1 after cancel, got %d", dv.appliedReviewIdx)
	}
	im, _ = im.Update(key("a"))
	if dv.pendingFix == nil {
		t.Fatalf("expected 'a' to reopen the same review after cancel")
	}

	// Now confirm with 'y' but stub execFixCmd so we don't actually launch claude.
	origExec := execFixCmd
	t.Cleanup(func() { execFixCmd = origExec })
	execFixCmd = func(_ *exec.Cmd, p *fixProposal, _ tabID) tea.Cmd {
		return func() tea.Msg { return fixDoneMsg{err: nil, proposal: p, origin: tabDoctor} }
	}
	m.tweaks.doctor.claudeOnPath = true
	im, cmd := im.Update(key("y"))
	if cmd != nil {
		_ = cmd()
	}
	if !dv.llmResults[0].applied {
		t.Fatalf("expected applied=true after confirming with 'y'")
	}
	if dv.appliedReviewIdx != -1 {
		t.Fatalf("expected appliedReviewIdx reset after 'y', got %d", dv.appliedReviewIdx)
	}

	// Second 'a' with all reviews applied should flash and noop.
	m.tweaks.doctor.showLLM = true
	m.tweaks.doctor.postReview = nil
	im, _ = im.Update(key("a"))
	if dv.pendingFix != nil {
		t.Fatalf("expected no pendingFix when all reviews applied")
	}
	if !strings.Contains(stripANSI(im.View()), "no remaining") {
		t.Fatalf("expected 'no remaining reviews' flash, got:\n%s", stripANSI(im.View()))
	}
}


func snapshotCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return len(entries)
}

// TestDoctorCLINoChangeCleansSnapshot - when claude exits 0 but writes the same
// bytes back, the pre-fix snapshot is orphaned and must be cleaned.
func TestDoctorCLINoChangeCleansSnapshot(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)
	origBytes, _ := os.ReadFile(mem)

	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })
	execFixCmd = func(_ *exec.Cmd, p *fixProposal, _ tabID) tea.Cmd {
		// Stub: rewrite the file with identical bytes (no real change).
		_ = os.WriteFile(p.target, origBytes, 0o644)
		return func() tea.Msg { return fixDoneMsg{err: nil, proposal: p, origin: tabDoctor} }
	}

	m := newModel(st)
	m.tweaks.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub no-op",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	m.tweaks.doctor.pendingFix = prop

	drive(m, "t", "]", "]", "]")
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}

	view := stripANSI(im.View())
	if !strings.Contains(view, "no edits") {
		t.Fatalf("expected 'no edits' flash, got:\n%s", view)
	}
	if got := snapshotCount(t, doctorSnapshotDir(st.paths.BackupsDir)); got != 0 {
		t.Fatalf("expected snapshot dir to be empty after no-change, got %d entries", got)
	}
}

// TestDoctorRevertFallsBackToBeforeBytes - when the on-disk snapshot is missing,
// revert must still succeed using the in-memory beforeBytes copy.
func TestDoctorRevertFallsBackToBeforeBytes(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)
	origBytes, _ := os.ReadFile(mem)

	// Simulate the post-fix state directly: file has been mutated, postReview is
	// active, snapshot path points at a now-deleted file.
	modified := []byte("# rewritten by stub\n")
	if err := os.WriteFile(mem, modified, 0o644); err != nil {
		t.Fatal(err)
	}
	bogusSnap := filepath.Join(doctorSnapshotDir(st.paths.BackupsDir), "does-not-exist.md")

	m := newModel(st)
	m.tweaks.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:      "stub fallback",
		kind:         fixClaudeCLI,
		target:       mem,
		snapshotPath: bogusSnap, // unreadable - forces beforeBytes fallback
		beforeBytes:  origBytes,
	}
	m.tweaks.doctor.postReview = prop
	m.tweaks.doctor.previewDiff = "@@\n-old\n+new\n"

	drive(m, "t", "]", "]", "]")
	var im tea.Model = m
	im, _ = im.Update(key("u"))

	got, _ := os.ReadFile(mem)
	if string(got) != string(origBytes) {
		t.Fatalf("expected revert via beforeBytes; got=%q want=%q", got, origBytes)
	}
	if !strings.Contains(stripANSI(im.View()), "reverted") {
		t.Fatalf("expected 'reverted' flash")
	}
}

// TestDoctorRevertDeletesSnapshot - after successful revert, the snapshot is
// redundant and must be removed.
func TestDoctorRevertDeletesSnapshot(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)
	origBytes, _ := os.ReadFile(mem)

	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })
	modified := []byte("# rewritten\n")
	execFixCmd = func(_ *exec.Cmd, p *fixProposal, _ tabID) tea.Cmd {
		_ = os.WriteFile(p.target, modified, 0o644)
		return func() tea.Msg { return fixDoneMsg{err: nil, proposal: p, origin: tabDoctor} }
	}

	m := newModel(st)
	m.tweaks.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub for revert-cleanup",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	m.tweaks.doctor.pendingFix = prop

	drive(m, "t", "]", "]", "]")
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}
	// Snapshot exists at this point.
	if got := snapshotCount(t, doctorSnapshotDir(st.paths.BackupsDir)); got != 1 {
		t.Fatalf("expected 1 snapshot before revert, got %d", got)
	}
	// Revert and confirm cleanup.
	im, _ = im.Update(key("u"))
	got, _ := os.ReadFile(mem)
	if string(got) != string(origBytes) {
		t.Fatalf("revert did not restore original")
	}
	if got := snapshotCount(t, doctorSnapshotDir(st.paths.BackupsDir)); got != 0 {
		t.Fatalf("expected snapshot deleted after revert, got %d entries", got)
	}
}

// TestDoctorInTUIWriteFailureCleansSnapshot - when the in-TUI write fails after
// a snapshot was taken, the orphan snapshot must be cleaned.
func TestDoctorInTUIWriteFailureCleansSnapshot(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)

	// Make the target read-only so the WriteFile fails. Restore mode on cleanup so
	// the temp dir tear-down can run.
	if err := os.Chmod(mem, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(mem, 0o644) })

	m := newModel(st)
	drive(m, "t", "]", "]", "]", "G")
	out := drive(m, "t", "]", "]", "]", "G", "f", "y")
	clean := stripANSI(out)
	if !strings.Contains(clean, "fix failed") {
		t.Fatalf("expected 'fix failed' flash, got:\n%s", clean)
	}
	if got := snapshotCount(t, doctorSnapshotDir(st.paths.BackupsDir)); got != 0 {
		t.Fatalf("expected snapshot cleaned after write failure, got %d entries", got)
	}
}

// TestDoctorReLintClearsStaleState - pressing 'r' must clear flash, lastFix,
// lastFixErr, and appliedReviewIdx so old state doesn't bleed into the new lint.
func TestDoctorReLintClearsStaleState(t *testing.T) {
	st, _ := buildState(t)
	seedBadMemory(t, st)
	m := newModel(st)
	drive(m, "t", "]", "]", "]")

	dv := m.tweaks.doctor
	dv.flash = "stale"
	dv.lastFix = &fixProposal{summary: "stale"}
	dv.lastFixErr = errStale{}
	dv.appliedReviewIdx = 0

	drive(m, "t", "]", "]", "]", "r")

	if dv.flash != "" {
		t.Errorf("flash not cleared: %q", dv.flash)
	}
	if dv.lastFix != nil {
		t.Errorf("lastFix not cleared: %+v", dv.lastFix)
	}
	if dv.lastFixErr != nil {
		t.Errorf("lastFixErr not cleared: %v", dv.lastFixErr)
	}
	if dv.appliedReviewIdx != -1 {
		t.Errorf("appliedReviewIdx not reset: %d", dv.appliedReviewIdx)
	}
}

type errStale struct{}

func (errStale) Error() string { return "stale" }

// TestDoctorBulkFixProgrammaticAppliesEveryFile asserts that pressing F on a
// category with multiple programmatic-fixable issues applies them all in one
// keystroke without spawning the CLI. Seeded with two broken MEM002 links in
// the same MEMORY.md plus a missing-frontmatter MEM004 in a sibling file -
// only the MEM002s share a code, so F on the MEM002 row must clean both
// broken-link lines (one disk write per line, line numbers handled correctly).
func TestDoctorBulkFixProgrammaticAppliesEveryFile(t *testing.T) {
	st, _ := buildState(t)
	memDir := tuiMemoryPath(st.paths.ClaudeConfigDir, st.project)
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mem := filepath.Join(memDir, "MEMORY.md")
	body := "# MEMORY\n\n- [Good](good.md) - kept\n- [Broken1](missing1.md) - first dead\n- [Broken2](missing2.md) - second dead\n"
	if err := os.WriteFile(mem, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "good.md"), []byte("---\nname: good\ndescription: ok\nmetadata:\n  type: project\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newModel(st)
	// Lint runs lazily on first render; switching tab does the trick.
	out := drive(m, "t", "]", "]", "]")
	if !strings.Contains(stripANSI(out), "MEM002") {
		t.Fatalf("expected MEM002 issues to render, got:\n%s", out)
	}
	// Find a cursor index on a MEM002 issue. Simpler: navigate to first
	// MEM002 with 'g' (which puts cursor on first issue - depends on layout).
	// For determinism, set cursor by scanning v.allIssues.
	dv := m.tweaks.doctor
	cursorIdx := -1
	for i, iss := range dv.allIssues {
		if iss.Code == "MEM002" {
			cursorIdx = i
			break
		}
	}
	if cursorIdx < 0 {
		t.Fatalf("no MEM002 issue found in allIssues: %+v", dv.allIssues)
	}
	dv.cursor = cursorIdx
	out = drive(m, "F", "y")

	got, _ := os.ReadFile(mem)
	if strings.Contains(string(got), "missing1.md") || strings.Contains(string(got), "missing2.md") {
		t.Fatalf("expected both broken-link lines removed, got:\n%s", got)
	}
	if !strings.Contains(string(got), "good.md") {
		t.Fatalf("expected the good link line preserved, got:\n%s", got)
	}
	if !strings.Contains(stripANSI(out), "bulk-fixed") {
		t.Fatalf("expected 'bulk-fixed' flash in view, got:\n%s", out)
	}

	// After bulk apply, the postReview gate must be open so the user can `u`
	// to revert. The CHANGELOG promises this behaviour for the whole batch.
	if m.tweaks.doctor.postReview == nil {
		t.Fatalf("expected postReview to be set after successful bulk apply")
	}
	out = drive(m, "u")
	got, _ = os.ReadFile(mem)
	if !strings.Contains(string(got), "missing1.md") || !strings.Contains(string(got), "missing2.md") {
		t.Fatalf("expected both broken-link lines restored after revert, got:\n%s", got)
	}
	if !strings.Contains(stripANSI(out), "reverted") {
		t.Fatalf("expected 'reverted' flash, got:\n%s", out)
	}
}

// TestDoctorBulkCLIPromptIsNotDoubleWrapped: the bundled CLI prompt must use
// each per-issue body once (cliPromptRaw), not the already-envelope-wrapped
// cliPrompt - otherwise every fix carries a redundant inner "You MUST use the
// Edit tool" preamble inside the outer envelope, wasting tokens and giving
// the model contradictory target instructions.
func TestDoctorBulkCLIPromptIsNotDoubleWrapped(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)
	cur := doctor.Issue{Code: "MEM003", File: mem, Line: 1, Message: "line 1 too long"}
	other := doctor.Issue{Code: "MEM003", File: mem, Line: 2, Message: "line 2 too long"}
	prop, ok := buildDoctorBulkFixProposal(cur, []doctor.Issue{cur, other}, st.project)
	if !ok {
		t.Fatalf("expected bulk-fix proposal")
	}
	// The imperative envelope's lead phrase should appear exactly once
	// (the outer bundle wrapper), not twice (once outer + once per item).
	hits := strings.Count(prop.cliPrompt, "You MUST use the Edit (or Write, for new files) tool")
	if hits != 1 {
		t.Fatalf("expected exactly 1 imperative preamble in bundled prompt, got %d:\n%s", hits, prop.cliPrompt)
	}
}

// TestCommandViewCapturingInputIncludesResolveActive guards against the
// global q/esc handler swallowing keys destined for the resolve picker.
// CLAUDE.md mandates this for every TUI sub-view mode.
func TestCommandViewCapturingInputIncludesResolveActive(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	drive(m, "t", "]", "]", "]") // ensure model is initialised
	cv := m.commands
	if cv == nil {
		t.Fatalf("commandView nil")
	}
	cv.resolveActive = true
	if !cv.capturingInput() {
		t.Fatalf("capturingInput() must return true while resolveActive - otherwise global q/esc consumes the key first")
	}
	cv.resolveActive = false
	if cv.capturingInput() {
		t.Fatalf("capturingInput() must return false when no sub-mode is active")
	}
}

// TestDoctorBulkFixRefusesSingletonCategory: F is a no-op when only one issue
// shares the cursor's code. The plan-time decision was "no point bulk-fixing
// one issue" - confirm that path surfaces the explanatory flash.
func TestDoctorBulkFixRefusesSingletonCategory(t *testing.T) {
	st, _ := buildState(t)
	seedBadMemory(t, st) // single MEM002 only
	m := newModel(st)
	drive(m, "t", "]", "]", "]")
	dv := m.tweaks.doctor
	for i, iss := range dv.allIssues {
		if iss.Code == "MEM002" {
			dv.cursor = i
			break
		}
	}
	out := drive(m, "F")
	if !strings.Contains(stripANSI(out), "no bulk-fix available") {
		t.Fatalf("expected 'no bulk-fix available' flash in view, got:\n%s", out)
	}
}

// TestDoctorBulkFixCLIBundlesPrompt asserts that for an all-CLI category, F
// builds a single bundled prompt naming every target. We exercise the proposal
// builder directly with fabricated MEM003 issues.
func TestDoctorBulkFixCLIBundlesPrompt(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)
	cur := doctor.Issue{Code: "MEM003", File: mem, Line: 1, Message: "line 1 too long (170 chars)"}
	other := doctor.Issue{Code: "MEM003", File: mem, Line: 2, Message: "line 2 too long (180 chars)"}
	prop, ok := buildDoctorBulkFixProposal(cur, []doctor.Issue{cur, other}, st.project)
	if !ok {
		t.Fatalf("expected bulk-fix proposal for two MEM003 issues")
	}
	if prop.kind != fixClaudeCLI {
		t.Fatalf("expected fixClaudeCLI, got %v", prop.kind)
	}
	if !strings.Contains(prop.cliPrompt, "Fix 1/2") || !strings.Contains(prop.cliPrompt, "Fix 2/2") {
		t.Fatalf("expected bundled prompt to enumerate both fixes, got:\n%s", prop.cliPrompt)
	}
}

// TestStreamLineRingTrimsToCap asserts the ring buffer caps at streamRingMax
// so a long-running fix doesn't grow memory without bound.
func TestStreamLineRingTrimsToCap(t *testing.T) {
	var ring []string
	for i := 0; i < streamRingMax*3; i++ {
		ring = appendStreamLine(ring, "noise", false)
	}
	if len(ring) != streamRingMax {
		t.Fatalf("ring exceeded cap: got %d, want %d", len(ring), streamRingMax)
	}
}

// TestStreamLineMarksStderr ensures stderr lines render distinctly in the
// log panel (renderStreamPanel keys off the "!" prefix appendStreamLine adds).
func TestStreamLineMarksStderr(t *testing.T) {
	ring := appendStreamLine(nil, "out-line", false)
	ring = appendStreamLine(ring, "err-line", true)
	if !strings.HasPrefix(ring[1], "!") {
		t.Fatalf("expected stderr entry to carry \"!\" prefix, got %q", ring[1])
	}
	if strings.HasPrefix(ring[0], "!") {
		t.Fatalf("expected stdout entry NOT to carry \"!\" prefix, got %q", ring[0])
	}
}

// TestChatDoneMsgUnknownOriginSurfacesError asserts the default arm on the
// chatDoneMsg switch flashes an error rather than silently dropping the
// message. Mirrors the same property held for fixDoneMsg and cliStreamLineMsg.
func TestChatDoneMsgUnknownOriginSurfacesError(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	drive(m, "t", "]", "]", "]") // ensure model is initialised
	im, _ := m.Update(chatDoneMsg{err: nil, origin: tabID(99)})
	mod := im.(*model)
	if !strings.Contains(stripANSI(mod.message), "chatDoneMsg with unhandled origin") {
		t.Fatalf("expected unhandled-origin error flash, got %q", mod.message)
	}
}

// TestTabChangeClearsStaleMessage drives the model to put text in m.message,
// then switches tabs and asserts the status line resets.
func TestTabChangeClearsStaleMessage(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	drive(m, "t", "]", "]", "]") // start on Doctor
	m.message = "leftover from doctor"
	drive(m, "1") // switch to MCPs
	if m.message != "" {
		t.Fatalf("expected m.message cleared on tab change, got %q", m.message)
	}
}
