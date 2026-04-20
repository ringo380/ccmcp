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
