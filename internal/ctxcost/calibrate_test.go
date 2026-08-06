package ctxcost

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
		// "/." yields "--": separators are not collapsed. Observed live as
		// -Users-ryanrobson-git--claude.
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

// TestCalibrateFindsTranscriptForDottedProjectPath is the end-to-end form of
// the same bug: with the old "/"-only slug, Calibrate looked for a directory
// that Claude Code never writes and returned ok=false for every dotted path.
func TestCalibrateFindsTranscriptForDottedProjectPath(t *testing.T) {
	cfg := t.TempDir()
	proj := "/Users/x/git/site.github.io"
	// Name the directory the way Claude Code does, without going through the
	// helper under test.
	dir := filepath.Join(cfg, "projects", "-Users-x-git-site-github-io")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(
		`{"message":{"usage":{"cache_creation_input_tokens":10,"cache_read_input_tokens":5}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, ok := Calibrate(cfg, proj)
	if !ok {
		t.Fatal("Calibrate must find the transcript for a project path containing dots")
	}
	if m.PrefixTokens != 15 {
		t.Fatalf("PrefixTokens = %d, want 15", m.PrefixTokens)
	}
}

func TestCalibrateReadsFirstUsageRecordOfNewestTranscript(t *testing.T) {
	cfg := t.TempDir()
	proj := "/Users/x/git/demo"
	dir := filepath.Join(cfg, "projects", TranscriptSlug(proj))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// An older transcript that must be ignored.
	old := filepath.Join(dir, "aaa-old.jsonl")
	if err := os.WriteFile(old, []byte(
		`{"message":{"usage":{"cache_creation_input_tokens":1,"cache_read_input_tokens":1}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The newest transcript: a non-usage line first, then the real record.
	newest := filepath.Join(dir, "bbb-new.jsonl")
	body := `{"type":"user","message":{"role":"user"}}` + "\n" +
		`{"type":"assistant","message":{"usage":{"cache_creation_input_tokens":61113,"cache_read_input_tokens":20729,"output_tokens":217}}}` + "\n"
	if err := os.WriteFile(newest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make mtime ordering unambiguous.
	bump(t, newest)

	m, ok := Calibrate(cfg, proj)
	if !ok {
		t.Fatal("Calibrate returned ok=false, want a measurement")
	}
	if m.PrefixTokens != 61113+20729 {
		t.Fatalf("PrefixTokens = %d, want %d", m.PrefixTokens, 61113+20729)
	}
	if m.SessionID != "bbb-new" {
		t.Fatalf("SessionID = %q, want %q", m.SessionID, "bbb-new")
	}
}

func TestCalibrateMissingOrUnreadableReportsNotOK(t *testing.T) {
	cfg := t.TempDir()

	if _, ok := Calibrate(cfg, "/Users/x/git/never-opened"); ok {
		t.Fatal("no transcript dir must yield ok=false, not a fabricated zero")
	}

	// An existing dir with no usage record anywhere.
	proj := "/Users/x/git/empty"
	dir := filepath.Join(cfg, "projects", TranscriptSlug(proj))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Calibrate(cfg, proj); ok {
		t.Fatal("a transcript with no usage record must yield ok=false")
	}
}

func bump(t *testing.T, path string) {
	t.Helper()
	now := time.Now()
	if err := os.Chtimes(path, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}
