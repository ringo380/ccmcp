package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestProjectNodeFindsALegacySpellingInsteadOfDuplicating pins how a legacy
// spelling of a project key is handled. The live ~/.claude.json on fancy-pc
// holds a `C:\` key beside `C:/...` keys, so this is a real file shape.
//
// Reads through the canonical key must find the legacy node. Writes must NOT
// land on it: Claude Code does an exact-string lookup (verified 2026-09-27
// with `claude mcp list` against a re-keyed copy of the live file - a
// `C:\...` or `c:/...` key is invisible to it), so a write to the legacy node
// would report success and have no effect. Instead the canonical key is
// created, seeded with a copy of the legacy node so nothing the user had is
// lost from Claude Code's point of view, and the legacy key is left in place
// untouched.
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
	keys := cj.ProjectPaths()
	if len(keys) != 2 {
		t.Fatalf("want the legacy key plus the canonical one, got %v", keys)
	}
	projects := cj.Raw["projects"].(map[string]any)
	canon, ok := projects["C:/Users/x/proj"].(map[string]any)
	if !ok {
		t.Fatalf("write did not create the canonical key Claude Code reads: %v", keys)
	}
	if got := canon["mcpServers"].(map[string]any); len(got) != 2 {
		t.Fatalf("canonical node should carry the legacy servers plus the new one, got %v", got)
	}
	legacy := projects[`C:\Users\x\proj`].(map[string]any)
	if got := legacy["mcpServers"].(map[string]any); len(got) != 1 {
		t.Fatalf("legacy node must be left untouched, got %v", got)
	}
	// The seed is a copy, not shared storage: a later write through the
	// canonical key must not reach into the legacy node.
	cj.DeleteProjectMCP("C:/Users/x/proj", "old")
	if got := legacy["mcpServers"].(map[string]any); len(got) != 1 {
		t.Fatalf("canonical and legacy nodes share storage; legacy lost a server: %v", got)
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

// TestProjectNodeWritesThroughACaseVariantOfTheLiveKey: a forward-slash key
// that differs only in letter case is the key Claude Code wrote, so a write
// typed in another case must land on it, not on a new copy Claude Code
// never reads.
func TestProjectNodeWritesThroughACaseVariantOfTheLiveKey(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive key matching is Windows-only")
	}
	path := filepath.Join(t.TempDir(), "claude.json")
	mustWriteJSON(t, path, map[string]any{
		"projects": map[string]any{
			"C:/Users/x/proj": map[string]any{
				"mcpServers": map[string]any{"old": map[string]any{"command": "a"}},
			},
		},
	})
	cj, err := LoadClaudeJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	cj.SetProjectMCP("C:/users/x/proj", "new", map[string]any{"command": "b"})
	if keys := cj.ProjectPaths(); len(keys) != 1 || keys[0] != "C:/Users/x/proj" {
		t.Fatalf("keys = %v, want only the live key C:/Users/x/proj", keys)
	}
	if got := cj.ProjectMCPs("C:/Users/x/proj"); len(got) != 2 {
		t.Fatalf("live key holds %d servers, want 2", len(got))
	}
}

// TestRemovalsThroughALegacyKeyLeaveItUntouched: deletes follow the same
// rule as writes. The change lands on the exact key, seeded from the legacy
// node, and the legacy node is not edited.
func TestRemovalsThroughALegacyKeyLeaveItUntouched(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("legacy backslash keys only arise on Windows")
	}
	load := func() *ClaudeJSON {
		path := filepath.Join(t.TempDir(), "claude.json")
		mustWriteJSON(t, path, map[string]any{
			"projects": map[string]any{
				`C:\Users\x\proj`: map[string]any{
					"mcpServers": map[string]any{
						"old":  map[string]any{"command": "a"},
						"keep": map[string]any{"command": "k"},
					},
				},
			},
		})
		cj, err := LoadClaudeJSON(path)
		if err != nil {
			t.Fatal(err)
		}
		return cj
	}
	legacyCount := func(cj *ClaudeJSON) int {
		node := cj.Raw["projects"].(map[string]any)[`C:\Users\x\proj`].(map[string]any)
		return len(node["mcpServers"].(map[string]any))
	}

	cj := load()
	if !cj.DeleteProjectMCP("C:/Users/x/proj", "old") {
		t.Fatal("delete through the legacy spelling reported nothing removed")
	}
	if n := legacyCount(cj); n != 2 {
		t.Fatalf("legacy node was edited by a delete: %d servers left, want 2", n)
	}
	canon, ok := cj.Raw["projects"].(map[string]any)["C:/Users/x/proj"].(map[string]any)
	if !ok {
		t.Fatal("delete did not create the exact key Claude Code reads")
	}
	if got := canon["mcpServers"].(map[string]any); len(got) != 1 || got["keep"] == nil {
		t.Fatalf("exact key should hold only keep, got %v", got)
	}

	cj = load()
	if n := cj.ClearProjectMCPs("C:/Users/x/proj"); n != 2 {
		t.Fatalf("clear removed %d, want 2", n)
	}
	if n := legacyCount(cj); n != 2 {
		t.Fatalf("legacy node was edited by a clear: %d servers left, want 2", n)
	}
	if _, ok := cj.Raw["projects"].(map[string]any)["C:/Users/x/proj"]; !ok {
		t.Fatal("clear did not create the exact key Claude Code reads")
	}
	if got := cj.ProjectMCPs("C:/Users/x/proj"); len(got) != 0 {
		t.Fatalf("exact key still lists servers after clear: %v", got)
	}
}
