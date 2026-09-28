package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestProjectNodeFindsALegacySpellingInsteadOfDuplicating pins that a write
// addressed with the canonical key must land on an existing key that names the
// same directory under an older spelling, and the project count must not
// change. The live ~/.claude.json on fancy-pc holds a `C:\` key beside
// `C:/...` keys, so this is a real file shape, not a hypothetical.
func TestProjectNodeFindsALegacySpellingInsteadOfDuplicating(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("legacy backslash keys only arise on Windows; Unix keys have one spelling")
	}
	path := filepath.Join(t.TempDir(), "claude.json")
	mustWriteJSON(t, path, map[string]any{
		"projects": map[string]any{
			`C:\Users\x\proj`: map[string]any{
				"mcpServers": map[string]any{"old": map[string]any{"command": "a"}},
			},
		},
	})
	cj, err := LoadClaudeJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cj.ProjectMCPs("C:/Users/x/proj"); len(got) != 1 {
		t.Fatalf("read through the canonical key found %d servers, want 1", len(got))
	}
	cj.SetProjectMCP("C:/Users/x/proj", "new", map[string]any{"command": "b"})
	if n := len(cj.ProjectPaths()); n != 1 {
		t.Fatalf("write created a duplicate project: %d keys, want 1: %v", n, cj.ProjectPaths())
	}
	if got := cj.ProjectMCPs(`C:\Users\x\proj`); len(got) != 2 {
		t.Fatalf("write did not land on the existing key: %d servers, want 2", len(got))
	}
}

// TestProjectNodeCreatesTheCanonicalKeyWhenNothingMatches: with no existing
// spelling, the new key is exactly the string passed in (the caller has
// already run paths.ProjectKey).
func TestProjectNodeCreatesTheCanonicalKeyWhenNothingMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	mustWriteJSON(t, path, map[string]any{"projects": map[string]any{}})
	cj, err := LoadClaudeJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	cj.SetProjectMCP("/Users/x/proj", "s", map[string]any{"command": "c"})
	keys := cj.ProjectPaths()
	if len(keys) != 1 || keys[0] != "/Users/x/proj" {
		t.Fatalf("keys = %v, want exactly [/Users/x/proj]", keys)
	}
}
