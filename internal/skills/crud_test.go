package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaffoldCreatesFile(t *testing.T) {
	home := t.TempDir()
	path, err := Scaffold("foo", "desc", ScopeUser, filepath.Join(home, ".claude"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if len(b) == 0 {
		t.Error("expected content")
	}
}

func TestScaffoldRefusesExisting(t *testing.T) {
	home := t.TempDir()
	if _, err := Scaffold("foo", "", ScopeUser, filepath.Join(home, ".claude"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold("foo", "", ScopeUser, filepath.Join(home, ".claude"), ""); err == nil {
		t.Error("expected error on duplicate")
	}
}

func TestRemoveReportsMissing(t *testing.T) {
	home := t.TempDir()
	_, existed, err := Remove("nope", ScopeUser, filepath.Join(home, ".claude"), "")
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Error("existed should be false")
	}
}

func TestMoveUserToProject(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	if _, err := Scaffold("wanderer", "", ScopeUser, filepath.Join(home, ".claude"), ""); err != nil {
		t.Fatal(err)
	}
	_, dst, err := Move("wanderer", ScopeUser, ScopeProject, filepath.Join(home, ".claude"), proj)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Errorf("dest missing: %v", err)
	}
}
