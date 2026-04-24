package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assetsSandbox extends setupSandbox with skill/agent/command fixtures under
// user scope so the list commands have something to find.
func assetsSandbox(t *testing.T) string {
	t.Helper()
	home := setupSandbox(t)
	mkfile := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mkfile(filepath.Join(home, ".claude", "skills", "alpha", "SKILL.md"),
		"---\nname: alpha\ndescription: user alpha skill\n---\n")
	mkfile(filepath.Join(home, ".claude", "skills", "off-me", "SKILL.md"),
		"---\nname: off-me\ndescription: disabled via override\n---\n")
	// Wire the override
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	b, _ := os.ReadFile(settingsPath)
	var s map[string]any
	_ = json.Unmarshal(b, &s)
	s["skillOverrides"] = map[string]any{"off-me": "off"}
	nb, _ := json.Marshal(s)
	_ = os.WriteFile(settingsPath, nb, 0o600)

	mkfile(filepath.Join(home, ".claude", "agents", "reviewer.md"),
		"---\nname: reviewer\ndescription: code review\nmodel: sonnet\n---\n")
	mkfile(filepath.Join(home, ".claude", "commands", "mycmd.md"),
		"---\ndescription: user-defined slash command\n---\n")
	return home
}

func TestCLISkillList(t *testing.T) {
	home := assetsSandbox(t)
	out, err := runCLI(t, home, "skill", "list")
	if err != nil {
		t.Fatalf("skill list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected 'alpha' in output:\n%s", out)
	}
	if !strings.Contains(out, "off-me") {
		t.Errorf("disabled skill should still list:\n%s", out)
	}
	// Enabled-only filter should drop off-me
	out, err = runCLI(t, home, "skill", "list", "--enabled")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "off-me") {
		t.Errorf("--enabled should exclude off-me:\n%s", out)
	}
}

func TestCLISkillListJSON(t *testing.T) {
	home := assetsSandbox(t)
	out, err := runCLI(t, home, "skill", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rows); err != nil {
		t.Fatalf("json decode: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Errorf("want 2 rows, got %d", len(rows))
	}
}

func TestCLIAgentList(t *testing.T) {
	home := assetsSandbox(t)
	out, err := runCLI(t, home, "agent", "list")
	if err != nil {
		t.Fatalf("agent list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reviewer") {
		t.Errorf("expected 'reviewer' in output:\n%s", out)
	}
	if !strings.Contains(out, "sonnet") {
		t.Errorf("expected model 'sonnet' in output:\n%s", out)
	}
}

func TestCLICommandList(t *testing.T) {
	home := assetsSandbox(t)
	out, err := runCLI(t, home, "command", "list")
	if err != nil {
		t.Fatalf("command list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "/mycmd") {
		t.Errorf("expected '/mycmd' in output:\n%s", out)
	}
}

func TestCLISkillScopeFilter(t *testing.T) {
	home := assetsSandbox(t)
	// plugin scope has no skills in sandbox, so scope=plugin should be empty
	out, err := runCLI(t, home, "skill", "list", "--scope", "plugin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no skills match") {
		t.Errorf("expected no matches:\n%s", out)
	}
}

func TestCLISkillEnableDisableRoundtrip(t *testing.T) {
	home := assetsSandbox(t)
	// off-me starts disabled via override; re-enabling should remove the entry.
	if _, err := runCLI(t, home, "skill", "enable", "off-me"); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	if _, ok := settings["skillOverrides"]; ok {
		// If present, it should be empty — SetSkillOverride deletes when last key goes.
		m, _ := settings["skillOverrides"].(map[string]any)
		if _, still := m["off-me"]; still {
			t.Errorf("off-me should be removed from skillOverrides, got %v", m)
		}
	}
	// Now disable alpha — should add an override entry.
	if _, err := runCLI(t, home, "skill", "disable", "alpha"); err != nil {
		t.Fatal(err)
	}
	settings = readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	m, _ := settings["skillOverrides"].(map[string]any)
	if m["alpha"] != "off" {
		t.Errorf("alpha should be off, got %v", m)
	}
}

func TestCLICommandConflicts(t *testing.T) {
	home := assetsSandbox(t)
	// Seed a skill with the same name as a command ("mycmd") to force a skill-vs-command conflict.
	sp := filepath.Join(home, ".claude", "skills", "mycmd", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp, []byte("---\nname: mycmd\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "command", "conflicts")
	if err != nil {
		t.Fatalf("conflicts: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skill-vs-command") {
		t.Errorf("expected skill-vs-command in output:\n%s", out)
	}
	if !strings.Contains(out, "/mycmd") {
		t.Errorf("expected /mycmd in output:\n%s", out)
	}
}
