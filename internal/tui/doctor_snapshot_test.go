package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotForFixCopiesBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "MEMORY.md")
	if err := os.WriteFile(src, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapDir := filepath.Join(dir, "snapshots")
	out, err := snapshotForFix(src, snapDir)
	if err != nil {
		t.Fatalf("snapshotForFix: %v", err)
	}
	if out == "" {
		t.Fatal("expected snapshot path")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\nworld\n" {
		t.Fatalf("snapshot bytes mismatch: %q", string(got))
	}
	if filepath.Ext(out) != ".md" {
		t.Fatalf("expected .md extension, got %q", out)
	}
}

func TestSnapshotForFixMissingSrc(t *testing.T) {
	snapDir := t.TempDir()
	out, err := snapshotForFix(filepath.Join(snapDir, "nope.md"), snapDir)
	if err != nil {
		t.Fatalf("expected nil error for missing src, got %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty path, got %q", out)
	}
}

func TestSnapshotForFixSameSecondCollision(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.md")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapDir := filepath.Join(dir, "snaps")
	a, err := snapshotForFix(src, snapDir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := snapshotForFix(src, snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("expected distinct snapshot paths, got %q twice", a)
	}
}

func TestGCDoctorSnapshotsKeepsRecent(t *testing.T) {
	dir := t.TempDir()
	// Seed 25 snapshots for one source file with strictly increasing mtimes.
	base := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 25; i++ {
		p := filepath.Join(dir, fmt.Sprintf("MEMORY-%d-%d.md", base.Add(time.Duration(i)*time.Second).Unix(), 0))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("%d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	if err := gcDoctorSnapshots(dir, 20, 30*24*time.Hour); err != nil {
		t.Fatalf("gc: %v", err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 20 {
		t.Fatalf("expected 20 survivors, got %d", len(left))
	}
}

func TestGCDoctorSnapshotsExpiresOld(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-60 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("CLAUDE-%d-%d.md", old.Unix()+int64(i), 0))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := gcDoctorSnapshots(dir, 20, 30*24*time.Hour); err != nil {
		t.Fatalf("gc: %v", err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 0 {
		t.Fatalf("expected 0 survivors, got %d", len(left))
	}
}

func TestGCDoctorSnapshotsGroupsBySourceFile(t *testing.T) {
	dir := t.TempDir()
	// 12 of file A, 12 of file B. Both groups under keep=20 — all should survive.
	base := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 12; i++ {
		for _, name := range []string{"A", "B"} {
			p := filepath.Join(dir, fmt.Sprintf("%s-%d-%d.md", name, base.Unix()+int64(i), 0))
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			mt := base.Add(time.Duration(i) * time.Minute)
			os.Chtimes(p, mt, mt)
		}
	}
	if err := gcDoctorSnapshots(dir, 20, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 24 {
		t.Fatalf("expected 24 survivors (12 per group), got %d", len(left))
	}
}

func TestGroupStem(t *testing.T) {
	cases := map[string]string{
		"MEMORY-1715500000-0.md":  "MEMORY.md",
		"CLAUDE-1700000000-42.md": "CLAUDE.md",
		"weird":                   "weird",
		".hidden":                 ".hidden",
	}
	for in, want := range cases {
		if got := groupStem(in); got != want {
			t.Errorf("groupStem(%q) = %q, want %q", in, got, want)
		}
	}
}
