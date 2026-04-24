package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLISkillNewAndRm(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "skill", "new", "shiny", "--description", "a shiny skill")
	if err != nil {
		t.Fatalf("skill new: %v\n%s", err, out)
	}
	path := filepath.Join(home, ".claude", "skills", "shiny", "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scaffolded file missing: %v", err)
	}
	if !strings.Contains(string(b), "name: shiny") {
		t.Errorf("frontmatter wrong:\n%s", b)
	}
	// new on existing should error
	if _, err := runCLI(t, home, "skill", "new", "shiny"); err == nil {
		t.Error("expected error on duplicate scaffold")
	}
	// rm deletes it
	if _, err := runCLI(t, home, "skill", "rm", "shiny"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone: %v", err)
	}
}

func TestCLISkillMove(t *testing.T) {
	home := setupSandbox(t)
	// create user-scope skill
	if _, err := runCLI(t, home, "skill", "new", "wanderer"); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	// move to project scope
	if _, err := runCLI(t, home, "skill", "move", "wanderer", "--to", "project", "--path", proj); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(home, ".claude", "skills", "wanderer")
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should be gone: %v", err)
	}
	dst := filepath.Join(proj, ".claude", "skills", "wanderer", "SKILL.md")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("destination missing: %v", err)
	}
}

func TestCLIAgentNewRmShow(t *testing.T) {
	home := setupSandbox(t)
	if _, err := runCLI(t, home, "agent", "new", "reviewer", "--model", "opus", "--description", "reviews"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "agents", "reviewer.md")
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "model: opus") {
		t.Errorf("model frontmatter wrong:\n%s", b)
	}
	out, err := runCLI(t, home, "agent", "show", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "opus") || !strings.Contains(out, "reviewer") {
		t.Errorf("show output incomplete:\n%s", out)
	}
	if _, err := runCLI(t, home, "agent", "rm", "reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone: %v", err)
	}
}

func TestCLICommandResolveIgnore(t *testing.T) {
	home := assetsSandbox(t)
	// force a skill-vs-command conflict on "mycmd"
	sp := filepath.Join(home, ".claude", "skills", "mycmd", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(sp, []byte("---\nname: mycmd\n---\n"), 0o600)

	// Pre-check: conflicts reports 1
	out, err := runCLI(t, home, "command", "conflicts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 conflict") {
		t.Errorf("expected 1 conflict, got:\n%s", out)
	}
	// ignore it
	if _, err := runCLI(t, home, "command", "resolve", "mycmd", "--strategy", "ignore"); err != nil {
		t.Fatal(err)
	}
	// Now conflicts should show none
	out, err = runCLI(t, home, "command", "conflicts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no conflicts") {
		t.Errorf("expected no conflicts after ignore, got:\n%s", out)
	}
	// --include-ignored brings it back
	out, err = runCLI(t, home, "command", "conflicts", "--include-ignored")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mycmd") {
		t.Errorf("expected conflict to reappear with --include-ignored:\n%s", out)
	}
}

func TestCLICommandResolveDisableSkill(t *testing.T) {
	home := assetsSandbox(t)
	sp := filepath.Join(home, ".claude", "skills", "mycmd", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(sp, []byte("---\nname: mycmd\n---\n"), 0o600)

	if _, err := runCLI(t, home, "command", "resolve", "mycmd", "--strategy", "disable-skill"); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	m, _ := settings["skillOverrides"].(map[string]any)
	if m["mycmd"] != "off" {
		t.Errorf("expected skillOverrides[mycmd]=off, got %v", m)
	}
}
