package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupMissingSrcIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := Backup(filepath.Join(dir, "does-not-exist.json"), filepath.Join(dir, "backups")); err != nil {
		t.Fatalf("missing src should be no-op, got: %v", err)
	}
}

func TestBackupStripsLeadingDot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(src, []byte(`{"ok":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(dir, "backups")
	if err := Backup(src, backups); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 backup, got %d", len(entries))
	}
	name := entries[0].Name()
	if strings.HasPrefix(name, ".") {
		t.Fatalf("backup filename should not start with dot, got %q", name)
	}
	if !strings.HasPrefix(name, "claude-") {
		t.Fatalf("backup filename should start with 'claude-', got %q", name)
	}
}

func TestBackupSameSecondCollision(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config.json")
	if err := os.WriteFile(src, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(dir, "backups")
	// Call several times in a tight loop — all should succeed.
	for i := 0; i < 5; i++ {
		if err := Backup(src, backups); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("want 5 backups (collision counter), got %d: %v", len(entries), entries)
	}
}
