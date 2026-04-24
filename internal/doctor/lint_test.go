package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/doctor"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasCode(issues []doctor.Issue, code string) bool {
	for _, iss := range issues {
		if iss.Code == code {
			return true
		}
	}
	return false
}

func issueCount(issues []doctor.Issue, sev doctor.Severity) int {
	n := 0
	for _, iss := range issues {
		if iss.Severity == sev {
			n++
		}
	}
	return n
}

// ── CLAUDE.md lint ────────────────────────────────────────────────────────────

func TestLintClaudeMD_Missing(t *testing.T) {
	issues := doctor.LintClaudeMD("/nonexistent/CLAUDE.md")
	if !hasCode(issues, "MD001") {
		t.Errorf("expected MD001 for missing file; got %v", issues)
	}
}

func TestLintClaudeMD_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, path, "")
	issues := doctor.LintClaudeMD(path)
	if !hasCode(issues, "MD002") {
		t.Errorf("expected MD002 for empty file; got %v", issues)
	}
}

func TestLintClaudeMD_LongLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	longLine := strings.Repeat("x", 250)
	writeFile(t, path, "# Header\n\n"+longLine+"\n")
	issues := doctor.LintClaudeMD(path)
	if !hasCode(issues, "MD003") {
		t.Errorf("expected MD003 for long line; got %v", issues)
	}
}

func TestLintClaudeMD_BrokenLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, path, "# Header\n\nSee [notes](notes/missing.md) for details.\n")
	issues := doctor.LintClaudeMD(path)
	if !hasCode(issues, "MD004") {
		t.Errorf("expected MD004 for broken link; got %v", issues)
	}
}

func TestLintClaudeMD_ValidLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	notesDir := filepath.Join(dir, "notes")
	writeFile(t, filepath.Join(notesDir, "present.md"), "exists")
	writeFile(t, path, "# Header\n\nSee [notes](notes/present.md) for details.\n")
	issues := doctor.LintClaudeMD(path)
	if hasCode(issues, "MD004") {
		t.Errorf("valid link should not trigger MD004; got %v", issues)
	}
}

func TestLintClaudeMD_URLLinksSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, path, "# Header\n\nSee [docs](https://example.com/docs) and [anchor](#section).\n")
	issues := doctor.LintClaudeMD(path)
	if hasCode(issues, "MD004") {
		t.Errorf("URL and anchor links should not trigger MD004; got %v", issues)
	}
}

func TestLintClaudeMD_Clean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, path, "# Project\n\nBuild: `go build ./...`\n\nTest: `go test ./...`\n")
	issues := doctor.LintClaudeMD(path)
	if issueCount(issues, doctor.SeverityError) > 0 {
		t.Errorf("clean file should have no errors; got %v", issues)
	}
}

// ── MEMORY.md lint ────────────────────────────────────────────────────────────

func TestLintMemoryIndex_Missing(t *testing.T) {
	issues := doctor.LintMemoryIndex("/nonexistent/memory")
	if !hasCode(issues, "MEM001") {
		t.Errorf("expected MEM001 for missing dir; got %v", issues)
	}
	// Should be info, not error (memory is optional)
	if issueCount(issues, doctor.SeverityError) > 0 {
		t.Errorf("missing memory should be Info, not Error; got %v", issues)
	}
}

func TestLintMemoryIndex_MissingFile(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "MEMORY.md")
	writeFile(t, index, "- [My Memory](missing.md) — some memory\n")
	issues := doctor.LintMemoryIndex(dir)
	if !hasCode(issues, "MEM002") {
		t.Errorf("expected MEM002 for missing memory file; got %v", issues)
	}
}

func TestLintMemoryIndex_ValidFile(t *testing.T) {
	dir := t.TempDir()
	memFile := filepath.Join(dir, "user_role.md")
	writeFile(t, memFile, "---\nname: user role\ndescription: user is a senior engineer\ntype: user\n---\n\nContent here.\n")
	writeFile(t, filepath.Join(dir, "MEMORY.md"), "- [User Role](user_role.md) — user is a senior engineer\n")
	issues := doctor.LintMemoryIndex(dir)
	if issueCount(issues, doctor.SeverityError) > 0 {
		t.Errorf("valid memory should have no errors; got %v", issues)
	}
}

func TestLintMemoryIndex_MissingFrontmatterField(t *testing.T) {
	dir := t.TempDir()
	memFile := filepath.Join(dir, "feedback.md")
	writeFile(t, memFile, "---\nname: some feedback\ntype: feedback\n---\n\nContent.\n")
	writeFile(t, filepath.Join(dir, "MEMORY.md"), "- [Feedback](feedback.md) — feedback\n")
	issues := doctor.LintMemoryIndex(dir)
	if !hasCode(issues, "MEM005") {
		t.Errorf("expected MEM005 for missing description; got %v", issues)
	}
}

func TestLintMemoryIndex_InvalidType(t *testing.T) {
	dir := t.TempDir()
	memFile := filepath.Join(dir, "bad.md")
	writeFile(t, memFile, "---\nname: bad type\ndescription: something\ntype: random\n---\n\nContent.\n")
	writeFile(t, filepath.Join(dir, "MEMORY.md"), "- [Bad](bad.md) — bad type example\n")
	issues := doctor.LintMemoryIndex(dir)
	if !hasCode(issues, "MEM006") {
		t.Errorf("expected MEM006 for invalid type; got %v", issues)
	}
}

func TestLintMemoryIndex_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	memFile := filepath.Join(dir, "nofm.md")
	writeFile(t, memFile, "This file has no frontmatter at all.\n")
	writeFile(t, filepath.Join(dir, "MEMORY.md"), "- [NoFM](nofm.md) — no frontmatter\n")
	issues := doctor.LintMemoryIndex(dir)
	if !hasCode(issues, "MEM004") {
		t.Errorf("expected MEM004 for missing frontmatter block; got %v", issues)
	}
}

func TestLintMemoryIndex_LongEntry(t *testing.T) {
	dir := t.TempDir()
	memFile := filepath.Join(dir, "long.md")
	writeFile(t, memFile, "---\nname: long\ndescription: long entry\ntype: project\n---\n\nContent.\n")
	longLine := "- [Long](" + "long.md" + ") — " + strings.Repeat("y", 160)
	writeFile(t, filepath.Join(dir, "MEMORY.md"), longLine+"\n")
	issues := doctor.LintMemoryIndex(dir)
	if !hasCode(issues, "MEM003") {
		t.Errorf("expected MEM003 for long index entry; got %v", issues)
	}
}

func TestIssueString(t *testing.T) {
	iss := doctor.Issue{
		File:     "/path/to/CLAUDE.md",
		Line:     42,
		Severity: doctor.SeverityWarning,
		Code:     "MD003",
		Message:  "line too long",
	}
	s := iss.String()
	if !strings.Contains(s, "MD003") || !strings.Contains(s, "42") {
		t.Errorf("Issue.String() missing expected content: %s", s)
	}
}
