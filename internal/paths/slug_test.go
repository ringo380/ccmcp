package paths

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTranscriptSlug pins the encoding against directory names observed under
// a live ~/.claude/projects: every character outside [A-Za-z0-9] becomes "-",
// case is preserved, and adjacent separators are NOT collapsed.
func TestTranscriptSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/git/ccmcp", "-Users-x-git-ccmcp"},
		// Dotted path - the case that made calibration silently dead. Observed
		// live: /Users/ryanrobson/git/clubdeck.github.io is stored as
		// -Users-ryanrobson-git-clubdeck-github-io.
		{"/Users/ryanrobson/git/clubdeck.github.io", "-Users-ryanrobson-git-clubdeck-github-io"},
		// "/." yields "--": separators are not collapsed.
		{"/Users/ryanrobson/git/.claude", "-Users-ryanrobson-git--claude"},
		// Underscores and spaces are separators too; capitals survive.
		{"/Users/x/My Docs/CT_Orbits", "-Users-x-My-Docs-CT-Orbits"},
		{"/Users/x/git/Audacity-MCP", "-Users-x-git-Audacity-MCP"},
	}
	for _, c := range cases {
		if got := TranscriptSlug(c.in); got != c.want {
			t.Fatalf("TranscriptSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLegacyTranscriptSlug pins the pre-2026-04 encoding, which preserved "."
// and "_". Observed live:
// -Users-ryanrobson-git-personal-medical-records-imaging-studies-2024-01-15_HoustonMethodist_CT-Orbits-with-Contrast
func TestLegacyTranscriptSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/git/ccmcp", "-Users-x-git-ccmcp"},
		{"/Users/x/records/2024-01-15_HoustonMethodist_CT", "-Users-x-records-2024-01-15_HoustonMethodist_CT"},
		{"/Users/x/git/site.github.io", "-Users-x-git-site.github.io"},
		{"/Users/x/My Docs", "-Users-x-My-Docs"},
	}
	for _, c := range cases {
		if got := LegacyTranscriptSlug(c.in); got != c.want {
			t.Fatalf("LegacyTranscriptSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestProjectStateDirFallsBackToLegacySlug is the regression for the doctor
// lookup: a project whose directory was written under the legacy encoding must
// still resolve, or LintMemoryIndex stats a nonexistent path and reports
// MEM001 against real populated memory.
func TestProjectStateDirFallsBackToLegacySlug(t *testing.T) {
	cfg := t.TempDir()
	proj := "/Users/x/records/2024-01-15_Methodist_CT"

	legacy := filepath.Join(cfg, "projects", LegacyTranscriptSlug(proj))
	if err := os.MkdirAll(filepath.Join(legacy, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := ProjectStateDir(cfg, proj); got != legacy {
		t.Fatalf("ProjectStateDir = %q, want the existing legacy dir %q", got, legacy)
	}
	if got, want := ProjectMemoryDir(cfg, proj), filepath.Join(legacy, "memory"); got != want {
		t.Fatalf("ProjectMemoryDir = %q, want %q", got, want)
	}

	// Once the current-slug directory exists it wins, even with the legacy one
	// still present - new sessions write there.
	cur := filepath.Join(cfg, "projects", TranscriptSlug(proj))
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProjectStateDir(cfg, proj); got != cur {
		t.Fatalf("ProjectStateDir = %q, want the current-slug dir %q", got, cur)
	}
}

// TestProjectStateDirDefaultsToCurrentSlug: with neither directory present the
// answer must be the current slug, so a fresh project gets written in today's
// encoding rather than resurrecting the legacy one.
func TestProjectStateDirDefaultsToCurrentSlug(t *testing.T) {
	cfg := t.TempDir()
	proj := "/Users/x/records/a_b.c"
	want := filepath.Join(cfg, "projects", TranscriptSlug(proj))
	if got := ProjectStateDir(cfg, proj); got != want {
		t.Fatalf("ProjectStateDir = %q, want %q", got, want)
	}
}
