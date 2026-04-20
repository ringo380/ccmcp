package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestClaudeJSONPreservesUnknownFields is the safety-critical test: ccmcp must never
// drop telemetry, onboarding, or other fields it doesn't understand.
func TestClaudeJSONPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.json")
	original := map[string]any{
		"anonymousId":         "abc123",
		"numStartups":         float64(42),
		"hasCompletedOnboarding": true,
		"mcpServers": map[string]any{
			"existing": map[string]any{"command": "old"},
		},
		"projects": map[string]any{
			"/tmp/x": map[string]any{
				"lastDuration":  float64(1000),
				"hasTrustDialogAccepted": true,
				"mcpServers": map[string]any{
					"alreadyHere": map[string]any{"command": "npx"},
				},
			},
		},
	}
	mustWriteJSON(t, path, original)

	cj, err := LoadClaudeJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	// Add a new MCP in the project scope; delete the user-scope existing one.
	cj.SetProjectMCP("/tmp/x", "newOne", map[string]any{"command": "node"})
	cj.DeleteUserMCP("existing")
	if err := cj.Save(); err != nil {
		t.Fatal(err)
	}

	// Reload and confirm unknown fields are intact.
	var reloaded map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded["anonymousId"] != "abc123" {
		t.Errorf("anonymousId lost: %#v", reloaded["anonymousId"])
	}
	if reloaded["numStartups"] != float64(42) {
		t.Errorf("numStartups lost: %#v", reloaded["numStartups"])
	}
	if reloaded["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding lost")
	}
	project, ok := reloaded["projects"].(map[string]any)
	if !ok {
		t.Fatal("projects missing")
	}
	x, ok := project["/tmp/x"].(map[string]any)
	if !ok {
		t.Fatal("project /tmp/x missing")
	}
	if x["hasTrustDialogAccepted"] != true {
		t.Error("hasTrustDialogAccepted lost inside project node")
	}
	if x["lastDuration"] != float64(1000) {
		t.Error("lastDuration lost inside project node")
	}
	servers, _ := x["mcpServers"].(map[string]any)
	if _, ok := servers["newOne"]; !ok {
		t.Error("newOne not added")
	}
	if _, ok := servers["alreadyHere"]; !ok {
		t.Error("alreadyHere was clobbered (should be preserved)")
	}
	// user-scope mcpServers should now be empty and we dropped the key entirely.
	if _, ok := reloaded["mcpServers"]; ok {
		t.Errorf("mcpServers key should be deleted when last entry removed; got %#v", reloaded["mcpServers"])
	}
}

func TestClaudeJSONUserMCPFlow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.json")
	mustWriteJSON(t, path, map[string]any{})

	cj, err := LoadClaudeJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	cj.SetUserMCP("a", map[string]any{"command": "a"})
	cj.SetUserMCP("b", map[string]any{"command": "b"})
	names := cj.UserMCPNames()
	if !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Errorf("want [a b], got %v", names)
	}
	cfg, ok := cj.DeleteUserMCP("a")
	if !ok {
		t.Fatal("delete should succeed")
	}
	if cfg.(map[string]any)["command"] != "a" {
		t.Fatalf("delete returned wrong config: %#v", cfg)
	}
	if _, ok := cj.DeleteUserMCP("missing"); ok {
		t.Fatal("delete of missing should return false")
	}
}

func TestClaudeJSONClearUserMCPs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.json")
	mustWriteJSON(t, path, map[string]any{
		"mcpServers": map[string]any{
			"one": map[string]any{"command": "1"},
			"two": map[string]any{"command": "2"},
		},
	})
	cj, _ := LoadClaudeJSON(path)
	removed := cj.ClearUserMCPs()
	if len(removed) != 2 {
		t.Fatalf("want 2 removed, got %d", len(removed))
	}
	if len(cj.UserMCPs()) != 0 {
		t.Fatal("UserMCPs should be empty after clear")
	}
}

func TestClaudeJSONMcpjsonLists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.json")
	mustWriteJSON(t, path, map[string]any{})
	cj, _ := LoadClaudeJSON(path)

	cj.SetProjectMcpjsonEnabled("/p", []string{"a", "b"})
	cj.SetProjectMcpjsonDisabled("/p", []string{"c"})
	if got := cj.ProjectMcpjsonEnabled("/p"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("enabled: %v", got)
	}
	if got := cj.ProjectMcpjsonDisabled("/p"); !reflect.DeepEqual(got, []string{"c"}) {
		t.Errorf("disabled: %v", got)
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
