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
	if !strings.Contains(clean, "Apply? y / n") {
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
	if strings.Contains(stripANSI(out), "Apply? y / n") {
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
	execFixCmd = func(_cmd *exec.Cmd, p *fixProposal) tea.Cmd {
		_ = os.WriteFile(p.target, modified, 0o644)
		return func() tea.Msg {
			return doctorFixDoneMsg{err: nil, proposal: p}
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
		// Run the returned command (it returns doctorFixDoneMsg synchronously in our stub).
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

