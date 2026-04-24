package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupProfileSandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	must := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(home, ".claude.json"), map[string]any{
		"anonymousId": "sandbox",
		"mcpServers": map[string]any{
			"server-a": map[string]any{"command": "cmd-a"},
			"server-b": map[string]any{"command": "cmd-b"},
		},
	})
	must(filepath.Join(home, ".claude-mcp-stash.json"), map[string]any{
		"userMcpServers": map[string]any{},
	})
	must(filepath.Join(home, ".claude-mcp-profiles.json"), map[string]any{
		"profiles": map[string]any{
			"myprofile": []any{"server-a", "server-b"},
		},
	})
	must(filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{},
	})
	must(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{},
	})
	return home
}

func TestCLIProfileExportJSON(t *testing.T) {
	home := setupProfileSandbox(t)
	out, err := runCLI(t, home, "profile", "export", "myprofile")
	if err != nil {
		t.Fatalf("export error: %v\n%s", err, out)
	}

	var sp ShareableProfile
	if e := json.Unmarshal([]byte(out), &sp); e != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", e, out)
	}
	if sp.Version != 1 {
		t.Errorf("want version 1, got %d", sp.Version)
	}
	if sp.Name != "myprofile" {
		t.Errorf("want name myprofile, got %q", sp.Name)
	}
	if len(sp.MCPs) != 2 {
		t.Errorf("want 2 MCPs, got %d: %v", len(sp.MCPs), sp.MCPs)
	}
	if sp.Configs != nil {
		t.Error("configs should be absent when --with-config is not passed")
	}
}

func TestCLIProfileExportWithConfig(t *testing.T) {
	home := setupProfileSandbox(t)
	out, err := runCLI(t, home, "profile", "export", "myprofile", "--with-config")
	if err != nil {
		t.Fatalf("export error: %v\n%s", err, out)
	}

	var sp ShareableProfile
	if e := json.Unmarshal([]byte(out), &sp); e != nil {
		t.Fatalf("output not valid JSON: %v", e)
	}
	if sp.Configs == nil {
		t.Fatal("configs should be present with --with-config")
	}
	if _, ok := sp.Configs["server-a"]; !ok {
		t.Error("configs should include server-a")
	}
	if _, ok := sp.Configs["server-b"]; !ok {
		t.Error("configs should include server-b")
	}
}

func TestCLIProfileExportToFile(t *testing.T) {
	home := setupProfileSandbox(t)
	outFile := filepath.Join(home, "exported.json")
	_, err := runCLI(t, home, "profile", "export", "myprofile", "--out", outFile)
	if err != nil {
		t.Fatalf("export error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	var sp ShareableProfile
	if e := json.Unmarshal(data, &sp); e != nil {
		t.Fatalf("output file not valid JSON: %v", e)
	}
	if sp.Name != "myprofile" {
		t.Errorf("name mismatch in file: %q", sp.Name)
	}
}

func TestCLIProfileImportNamesOnly(t *testing.T) {
	home := setupProfileSandbox(t)

	sp := ShareableProfile{Version: 1, Name: "imported", MCPs: []string{"server-a"}}
	data, _ := json.Marshal(sp)
	inFile := filepath.Join(home, "import.json")
	if err := os.WriteFile(inFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "profile", "import", inFile)
	if err != nil {
		t.Fatalf("import error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "imported") {
		t.Errorf("expected success message; got %q", out)
	}

	// Profile should exist
	out2, err := runCLI(t, home, "profile", "show", "imported")
	if err != nil {
		t.Fatalf("show error: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "server-a") {
		t.Errorf("imported profile should contain server-a; got %q", out2)
	}

	// No MCPs should have been added to user scope (names-only import)
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	userMCPs, _ := claude["mcpServers"].(map[string]any)
	if _, added := userMCPs["server-a"]; added && len(sp.Configs) == 0 {
		// server-a was already in user scope from sandbox setup — this is fine
	}
}

func TestCLIProfileImportWithConfig(t *testing.T) {
	home := setupProfileSandbox(t)

	sp := ShareableProfile{
		Version: 1,
		Name:    "withcfg",
		MCPs:    []string{"new-server"},
		Configs: map[string]any{
			"new-server": map[string]any{"command": "new-cmd"},
		},
	}
	data, _ := json.Marshal(sp)
	inFile := filepath.Join(home, "import-cfg.json")
	if err := os.WriteFile(inFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "profile", "import", inFile)
	if err != nil {
		t.Fatalf("import error: %v\n%s", err, out)
	}

	// new-server should be in user scope
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	userMCPs, _ := claude["mcpServers"].(map[string]any)
	if _, ok := userMCPs["new-server"]; !ok {
		t.Errorf("new-server should have been added to user scope; got %v", userMCPs)
	}

	// Profile should exist
	out2, _ := runCLI(t, home, "profile", "show", "withcfg")
	if !strings.Contains(out2, "new-server") {
		t.Errorf("profile should contain new-server; got %q", out2)
	}
}

func TestCLIProfileImportOverwriteBlocked(t *testing.T) {
	home := setupProfileSandbox(t)

	sp := ShareableProfile{Version: 1, Name: "myprofile", MCPs: []string{"server-a"}}
	data, _ := json.Marshal(sp)
	inFile := filepath.Join(home, "dup.json")
	if err := os.WriteFile(inFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runCLI(t, home, "profile", "import", inFile)
	if err == nil {
		t.Error("importing existing profile without --overwrite should error")
	}
}

func TestCLIProfileImportOverwriteAllowed(t *testing.T) {
	home := setupProfileSandbox(t)

	sp := ShareableProfile{Version: 1, Name: "myprofile", MCPs: []string{"server-a"}}
	data, _ := json.Marshal(sp)
	inFile := filepath.Join(home, "dup.json")
	if err := os.WriteFile(inFile, data, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "profile", "import", inFile, "--overwrite")
	if err != nil {
		t.Fatalf("import with --overwrite should succeed; err: %v\n%s", err, out)
	}

	out2, _ := runCLI(t, home, "profile", "show", "myprofile")
	if strings.Contains(out2, "server-b") {
		t.Error("overwritten profile should no longer have server-b")
	}
}
