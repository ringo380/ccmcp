package ctxcost

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTranscriptSlug(t *testing.T) {
	got := TranscriptSlug("/Users/x/git/ccmcp")
	want := "-Users-x-git-ccmcp"
	if got != want {
		t.Fatalf("TranscriptSlug = %q, want %q", got, want)
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
