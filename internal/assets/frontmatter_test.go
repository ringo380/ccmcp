package assets

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadFrontmatterBasic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "SKILL.md")
	writeFile(t, p, `---
name: my-skill
description: "Does a thing"
model: sonnet
triggers:
  - foo
  - bar
---

body goes here
`)
	fm, err := ReadFrontmatter(p)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "my-skill" {
		t.Errorf("name=%q", fm.Name)
	}
	if fm.Description != "Does a thing" {
		t.Errorf("desc=%q", fm.Description)
	}
	if fm.Model != "sonnet" {
		t.Errorf("model=%q", fm.Model)
	}
	if _, ok := fm.Raw["triggers"]; ok {
		t.Error("triggers is a list — should not land in Raw")
	}
}

func TestReadFrontmatterNoBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.md")
	writeFile(t, p, "# Just a markdown file\n\nNo frontmatter here.\n")
	fm, err := ReadFrontmatter(p)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "" || fm.Raw != nil {
		t.Errorf("expected zero value, got %+v", fm)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello world", 5); got != "hell…" {
		t.Errorf("Truncate=%q", got)
	}
	if got := Truncate("short", 20); got != "short" {
		t.Errorf("Truncate no-op=%q", got)
	}
}
