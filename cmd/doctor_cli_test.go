package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIDoctorMD_NoFile(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	// No CLAUDE.md in proj — should report MD001
	out, err := runCLI(t, home, "doctor", "md", "--path", proj)
	// Error is expected (lint error found)
	_ = err
	if !strings.Contains(out, "MD001") && !strings.Contains(out, "not found") {
		t.Errorf("expected MD001 or 'not found'; got:\n%s", out)
	}
}

func TestCLIDoctorMD_CleanFile(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	claudeMD := filepath.Join(proj, "CLAUDE.md")
	if err := os.WriteFile(claudeMD, []byte("# Project\n\nBuild: `go build ./...`\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, home, "doctor", "md", "--path", proj)
	if err != nil {
		// errors are expected only if lint errors found — a clean file + missing memory is OK
		// as long as it's not an error-level issue
		if strings.Contains(out, "MD001") || strings.Contains(out, "MD002") {
			t.Errorf("clean CLAUDE.md should not cause error-level issues; got:\n%s", out)
		}
	}
	if !strings.Contains(out, "CLAUDE.md") {
		t.Errorf("output should mention CLAUDE.md; got:\n%s", out)
	}
}

func TestCLIDoctorMD_BrokenLink(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	claudeMD := filepath.Join(proj, "CLAUDE.md")
	content := "# Project\n\nSee [notes](notes/missing.md) for gotchas.\n"
	if err := os.WriteFile(claudeMD, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := runCLI(t, home, "doctor", "md", "--path", proj)
	if !strings.Contains(out, "MD004") {
		t.Errorf("expected MD004 for broken link; got:\n%s", out)
	}
}

func TestCLIDoctorMD_UserFlag(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	// Write a valid project CLAUDE.md
	if err := os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("# Project\n\ncontent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Write a user-level CLAUDE.md
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("# User\n\ncontent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := runCLI(t, home, "doctor", "md", "--path", proj, "--user")
	if !strings.Contains(out, "user") {
		t.Errorf("--user flag should include user-level CLAUDE.md in output; got:\n%s", out)
	}
}

func TestCLIDoctorMD_WithValidMemory(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("# Project\n\ncontent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a valid memory directory
	memDir := t.TempDir()
	memFile := filepath.Join(memDir, "feedback.md")
	if err := os.WriteFile(memFile, []byte("---\nname: feedback\ndescription: test feedback\ntype: feedback\n---\n\nContent.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("- [Feedback](feedback.md) — test feedback\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "doctor", "md", "--path", proj, "--memory-dir", memDir)
	_ = err
	if strings.Contains(out, "MEM005") || strings.Contains(out, "MEM004") {
		t.Errorf("valid memory should not trigger frontmatter errors; got:\n%s", out)
	}
}

func TestCLIDoctorMD_BrokenMemoryLink(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("# Project\n\ncontent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	memDir := t.TempDir()
	// Index points to a file that doesn't exist
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("- [Ghost](ghost.md) — gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _ := runCLI(t, home, "doctor", "md", "--path", proj, "--memory-dir", memDir)
	if !strings.Contains(out, "MEM002") {
		t.Errorf("expected MEM002 for broken memory link; got:\n%s", out)
	}
}
