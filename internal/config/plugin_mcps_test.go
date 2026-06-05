package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestScanAllVsEnabled locks in the contract: ScanAll returns every installed plugin's
// MCPs (each tagged with .Enabled), and ScanEnabled is strictly a filter down to
// Enabled=true.
func TestScanAllVsEnabled(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")

	mkPlugin := func(mkt, name, version string, servers map[string]any) string {
		pdir := filepath.Join(pluginsDir, "cache", mkt, name, version)
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(servers)
		if err := os.WriteFile(filepath.Join(pdir, ".mcp.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		return pdir
	}
	// Two plugins, same marketplace: one enabled, one installed-but-disabled.
	enabledPath := mkPlugin("mkt", "alpha", "1.0", map[string]any{
		"alpha-srv": map[string]any{"command": "npx"},
	})
	disabledPath := mkPlugin("mkt", "beta", "1.0", map[string]any{
		"beta-srv": map[string]any{"command": "node"},
	})

	settings := &Settings{Path: "<mem>", Raw: map[string]any{
		"enabledPlugins": map[string]any{
			"alpha@mkt": true,
			"beta@mkt":  false,
		},
	}}
	installed := &InstalledPlugins{Path: "<mem>", Raw: map[string]any{
		"plugins": map[string]any{
			"alpha@mkt": []any{map[string]any{"installPath": enabledPath}},
			"beta@mkt":  []any{map[string]any{"installPath": disabledPath}},
		},
	}}

	all := ScanAllInstalledPluginMCPs(settings, installed, pluginsDir)
	if len(all) != 2 {
		t.Fatalf("ScanAll: want 2 entries, got %d (%v)", len(all), keysOf(all))
	}
	if srcs := all["alpha-srv"]; len(srcs) != 1 || !srcs[0].Enabled {
		t.Errorf("alpha-srv should be enabled: %+v", srcs)
	}
	if srcs := all["beta-srv"]; len(srcs) != 1 || srcs[0].Enabled {
		t.Errorf("beta-srv should be disabled: %+v", srcs)
	}

	enabledOnly := ScanEnabledPluginMCPs(settings, installed, pluginsDir)
	if _, ok := enabledOnly["alpha-srv"]; !ok {
		t.Error("enabled-only: alpha-srv missing")
	}
	if _, ok := enabledOnly["beta-srv"]; ok {
		t.Error("enabled-only: beta-srv should be filtered out")
	}
}

// TestScanMergesPluginManifestMCPs locks in that ScanAll reads a plugin's MCP servers from
// BOTH <installPath>/.mcp.json AND the "mcpServers" field of <installPath>/.claude-plugin/
// plugin.json (Claude Code loads both), deduped by server name, across the manifest field's
// three shapes: inline object, string path, and array of paths.
func TestScanMergesPluginManifestMCPs(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")

	writeJSON := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srv := func(cmd string) map[string]any { return map[string]any{"command": cmd} }

	// manifestOnly: servers declared ONLY in plugin.json's inline mcpServers object, no .mcp.json.
	manifestOnly := filepath.Join(pluginsDir, "cache", "mkt", "manifestOnly", "1.0")
	writeJSON(filepath.Join(manifestOnly, ".claude-plugin", "plugin.json"), map[string]any{
		"name":       "manifestOnly",
		"mcpServers": map[string]any{"mo-a": srv("node"), "mo-b": srv("node")},
	})

	// dualSource: 2 servers in .mcp.json, a superset of 4 (incl. the same 2) in plugin.json inline.
	// Expect the union deduped to 4, with no duplicate sources for the overlapping names.
	dualSource := filepath.Join(pluginsDir, "cache", "mkt", "dualSource", "1.0")
	writeJSON(filepath.Join(dualSource, ".mcp.json"), map[string]any{
		"mcpServers": map[string]any{"ds-1": srv("node"), "ds-2": srv("node")},
	})
	writeJSON(filepath.Join(dualSource, ".claude-plugin", "plugin.json"), map[string]any{
		"name": "dualSource",
		"mcpServers": map[string]any{
			"ds-1": srv("node"), "ds-2": srv("node"), "ds-3": srv("node"), "ds-4": srv("node"),
		},
	})

	// pathSource: plugin.json mcpServers is a string path back to the root .mcp.json (the supabase
	// shape). Must resolve and read that file without double-counting.
	pathSource := filepath.Join(pluginsDir, "cache", "mkt", "pathSource", "1.0")
	writeJSON(filepath.Join(pathSource, ".mcp.json"), map[string]any{
		"mcpServers": map[string]any{"ps-1": srv("node")},
	})
	writeJSON(filepath.Join(pathSource, ".claude-plugin", "plugin.json"), map[string]any{
		"name":       "pathSource",
		"mcpServers": "./.mcp.json",
	})

	settings := &Settings{Path: "<mem>", Raw: map[string]any{
		"enabledPlugins": map[string]any{
			"manifestOnly@mkt": true,
			"dualSource@mkt":   true,
			"pathSource@mkt":   true,
		},
	}}
	installed := &InstalledPlugins{Path: "<mem>", Raw: map[string]any{
		"plugins": map[string]any{
			"manifestOnly@mkt": []any{map[string]any{"installPath": manifestOnly}},
			"dualSource@mkt":   []any{map[string]any{"installPath": dualSource}},
			"pathSource@mkt":   []any{map[string]any{"installPath": pathSource}},
		},
	}}

	all := ScanAllInstalledPluginMCPs(settings, installed, pluginsDir)

	// manifest-only servers are discovered even without a .mcp.json.
	for _, name := range []string{"mo-a", "mo-b"} {
		if len(all[name]) != 1 {
			t.Errorf("%s: want 1 source from plugin.json, got %d", name, len(all[name]))
		}
	}
	// dual-source union is complete and deduped (each overlapping name = exactly one source).
	for _, name := range []string{"ds-1", "ds-2", "ds-3", "ds-4"} {
		if len(all[name]) != 1 {
			t.Errorf("%s: want 1 deduped source, got %d", name, len(all[name]))
		}
	}
	// string-path form resolves without duplicating.
	if len(all["ps-1"]) != 1 {
		t.Errorf("ps-1: want 1 source via string path, got %d", len(all["ps-1"]))
	}
}

func keysOf(m map[string][]PluginMCPSource) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestInstalledPluginsByName(t *testing.T) {
	ip := &InstalledPlugins{Raw: map[string]any{
		"plugins": map[string]any{
			"alpha@mkt1":  []any{map[string]any{"installPath": "/x1"}},
			"alpha@mkt2":  []any{map[string]any{"installPath": "/x2"}},
			"beta@mkt1":   []any{map[string]any{"installPath": "/y"}},
		},
	}}
	got := ip.ByName("alpha")
	if len(got) != 2 {
		t.Errorf("ByName(alpha): want 2, got %d", len(got))
	}
	if len(ip.ByName("beta")) != 1 {
		t.Error("ByName(beta): want 1")
	}
	if len(ip.ByName("gamma")) != 0 {
		t.Error("ByName(gamma): want 0")
	}
}
