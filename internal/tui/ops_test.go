package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// toTweaksOps navigates to the Tweaks tab and switches to the Maintenance sub-view.
func toTweaksOps(t *testing.T) *model {
	t.Helper()
	st, _ := buildState(t)
	m := newModel(st)
	m.width, m.height = 100, 40
	pressKey(m, "t")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")}) // -> Maintenance
	return m
}

func TestOpsListsActions(t *testing.T) {
	m := toTweaksOps(t)
	out := m.tweaks.ops.render()
	for _, name := range []string{"Prune orphans", "Plugin cache GC", "Snapshot", "health check"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(name)) {
			t.Fatalf("ops list missing %q\n%s", name, out)
		}
	}
}

func TestOpsSnapshotWritesBackup(t *testing.T) {
	m := toTweaksOps(t)
	ov := m.tweaks.ops
	for i, a := range ov.actions {
		if strings.Contains(strings.ToLower(a.label), "snapshot") {
			ov.cursor = i
			break
		}
	}
	// Snapshot is non-destructive: runs immediately without confirm.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Backup dir should now contain at least one claude-* backup.
	// config.Backup strips leading dot: ~/.claude.json -> claude-YYYYMMDD-HHMMSS.json
	entries, _ := readDirNames(m.st.paths.BackupsDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e, "claude-") {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot did not write a backup into %s: %v", m.st.paths.BackupsDir, entries)
	}
}

func TestOpsPruneRemovesOrphans(t *testing.T) {
	st, _ := buildState(t)
	// Inject an orphan disabledMcpServers key into the project.
	st.cj.SetProjectDisabledMcpServers(st.project, []string{"orphan-mcp-that-does-not-exist"})

	m := newModel(st)
	m.width, m.height = 100, 40
	pressKey(m, "t")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")}) // -> Maintenance

	ov := m.tweaks.ops
	// Find Prune action.
	for i, a := range ov.actions {
		if strings.Contains(strings.ToLower(a.label), "prune") {
			ov.cursor = i
			break
		}
	}
	// Prune is destructive. ConfirmBeforeApply defaults to true, so Enter shows prompt.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !ov.pendingConfirm {
		// ConfirmBeforeApply may be off - prune already ran.
		// Fall through to check result below.
	} else {
		// Confirm with y.
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	}

	remaining := m.st.cj.ProjectDisabledMcpServers(m.st.project)
	for _, k := range remaining {
		if k == "orphan-mcp-that-does-not-exist" {
			t.Fatalf("prune did not remove orphan key; remaining: %v", remaining)
		}
	}
}

func TestOpsConfirmGateDestructive(t *testing.T) {
	st, _ := buildState(t)
	// ConfirmBeforeApply defaults to true, but set explicitly for clarity.
	st.appcfg.SetBool("confirmBeforeApply", true)

	m := newModel(st)
	m.width, m.height = 100, 40
	pressKey(m, "t")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})

	ov := m.tweaks.ops
	for i, a := range ov.actions {
		if strings.Contains(strings.ToLower(a.label), "prune") {
			ov.cursor = i
			break
		}
	}
	// First Enter should raise the confirm prompt, not run.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	out := ov.render()
	if !strings.Contains(strings.ToLower(out), "apply") {
		t.Fatalf("expected Apply? confirm prompt, got:\n%s", out)
	}
	if !ov.pendingConfirm {
		t.Fatal("pendingConfirm should be true after Enter on a destructive action with ConfirmBeforeApply on")
	}
	// 'n' should cancel without running.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if ov.pendingConfirm {
		t.Fatal("pendingConfirm should be cleared after n")
	}
}

func TestOpsHealthCheckReturnsResult(t *testing.T) {
	m := toTweaksOps(t)
	ov := m.tweaks.ops
	for i, a := range ov.actions {
		if strings.Contains(strings.ToLower(a.label), "health") {
			ov.cursor = i
			break
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	out := ov.render()
	// Should contain pass/fail counts or a summary word.
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "pass") && !strings.Contains(lower, "fail") && !strings.Contains(lower, "issue") && !strings.Contains(lower, "ok") {
		t.Fatalf("health check result missing expected keywords:\n%s", out)
	}
}

// readDirNames returns entry names from a directory.
func readDirNames(dir string) ([]string, error) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out, nil
}
