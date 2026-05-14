package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	body := "# MEMORY\n\n- [Good](good.md) — kept\n- [Broken](missing.md) — broken target\n"
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
	// Doctor tab is "0" (key dispatch in model.go — Discover sits at 9).
	out := drive(m, "0")
	if !strings.Contains(stripANSI(out), "MEMORY.md") {
		t.Fatalf("doctor tab did not render MEMORY.md group:\n%s", out)
	}

	// Navigate to the MEM002 issue and press 'f' to open the preview panel.
	// allIssues is filled in render(), so re-issue any key to be safe.
	out = drive(m, "0", "G", "f")
	clean := stripANSI(out)
	if !strings.Contains(clean, "Apply? y") {
		t.Fatalf("expected pre-apply panel, got:\n%s", clean)
	}
	if !strings.Contains(clean, "-- [Broken]") && !strings.Contains(clean, "-Broken") && !strings.Contains(clean, "Broken") {
		t.Fatalf("expected the broken line in the diff, got:\n%s", clean)
	}

	// Press 'n' — file must be unchanged.
	out = drive(m, "0", "G", "f", "n")
	got, _ := os.ReadFile(mem)
	if string(got) != string(origBytes) {
		t.Fatalf("file changed after 'n': before=%q after=%q", origBytes, got)
	}
	if strings.Contains(stripANSI(out), "Apply? y") {
		t.Fatalf("panel should be gone after 'n'")
	}

	// Press 'f' then 'y' — file must be modified and snapshot must exist.
	out = drive(m, "0", "G", "f", "y")
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
	m.doctor.claudeOnPath = true

	// Forge a CLI-kind issue by overriding the fix proposal source: easiest path is to
	// build a proposal manually and assign it to pendingFix, then simulate 'y'.
	prop := &fixProposal{
		summary:   "stub CLI fix",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "rewrite this file",
		cliArgs:   []string{"--print", "x"},
	}
	m.doctor.pendingFix = prop
	m.doctor.previewDiff = buildCLIPromptPreview(prop)

	// Drive into the doctor tab and approve.
	drive(m, "0")
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
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub",
		kind:      fixClaudeCLI,
		target:    filepath.Join(st.project, "CLAUDE.md"),
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	_ = os.WriteFile(prop.target, []byte("# placeholder\n"), 0o644)
	m.doctor.pendingFix = prop

	drive(m, "0")
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
	m.doctor.claudeOnPath = true
	m.doctor.showLLM = true
	m.doctor.llmResults = []llmReviewResult{
		{path: mem, content: "- Shorten the broken-link line.\n- Verdict: minor cleanup needed.\n"},
	}

	drive(m, "0")
	var im tea.Model = m
	im, _ = im.Update(key("a"))

	dv := m.doctor
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

	// Cancel with 'n' — review must NOT be marked applied, showLLM must restore,
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
	m.doctor.claudeOnPath = true
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
	m.doctor.showLLM = true
	m.doctor.postReview = nil
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

// TestDoctorCLINoChangeCleansSnapshot — when claude exits 0 but writes the same
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
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub no-op",
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
	if !strings.Contains(view, "no changes") {
		t.Fatalf("expected 'no changes' flash, got:\n%s", view)
	}
	if got := snapshotCount(t, doctorSnapshotDir(st.paths.BackupsDir)); got != 0 {
		t.Fatalf("expected snapshot dir to be empty after no-change, got %d entries", got)
	}
}

// TestDoctorRevertFallsBackToBeforeBytes — when the on-disk snapshot is missing,
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
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:      "stub fallback",
		kind:         fixClaudeCLI,
		target:       mem,
		snapshotPath: bogusSnap, // unreadable — forces beforeBytes fallback
		beforeBytes:  origBytes,
	}
	m.doctor.postReview = prop
	m.doctor.previewDiff = "@@\n-old\n+new\n"

	drive(m, "0")
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

// TestDoctorRevertDeletesSnapshot — after successful revert, the snapshot is
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
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub for revert-cleanup",
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

// TestDoctorInTUIWriteFailureCleansSnapshot — when the in-TUI write fails after
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
	drive(m, "0", "G")
	out := drive(m, "0", "G", "f", "y")
	clean := stripANSI(out)
	if !strings.Contains(clean, "fix failed") {
		t.Fatalf("expected 'fix failed' flash, got:\n%s", clean)
	}
	if got := snapshotCount(t, doctorSnapshotDir(st.paths.BackupsDir)); got != 0 {
		t.Fatalf("expected snapshot cleaned after write failure, got %d entries", got)
	}
}

// TestDoctorReLintClearsStaleState — pressing 'r' must clear flash, lastFix,
// lastFixErr, and appliedReviewIdx so old state doesn't bleed into the new lint.
func TestDoctorReLintClearsStaleState(t *testing.T) {
	st, _ := buildState(t)
	seedBadMemory(t, st)
	m := newModel(st)
	drive(m, "0")

	dv := m.doctor
	dv.flash = "stale"
	dv.lastFix = &fixProposal{summary: "stale"}
	dv.lastFixErr = errStale{}
	dv.appliedReviewIdx = 0

	drive(m, "0", "r")

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
