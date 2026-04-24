package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ringo380/ccmcp/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverUserAgents(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "agents", "code-reviewer.md"),
		"---\nname: code-reviewer\ndescription: reviews code\nmodel: sonnet\n---\nbody\n")
	writeFile(t, filepath.Join(home, ".claude", "agents", "README.txt"), "ignore me")

	ags := Discover(filepath.Join(home, ".claude"), "", nil, nil, "")
	if len(ags) != 1 {
		t.Fatalf("want 1 agent, got %d", len(ags))
	}
	if ags[0].Name != "code-reviewer" || ags[0].Model != "sonnet" {
		t.Errorf("unexpected: %+v", ags[0])
	}
}

func TestDiscoverPluginAgents(t *testing.T) {
	home := t.TempDir()
	pluginPath := filepath.Join(home, ".claude", "plugins", "cache", "mkt", "ap", "1.0.0")
	writeFile(t, filepath.Join(pluginPath, "agents", "copywriter.md"),
		"---\nname: copywriter\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"enabledPlugins": {"ap@mkt": false}}`)
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"ap@mkt":[{"scope":"user","installPath":"`+pluginPath+`"}]}}`)

	settings, _ := config.LoadSettings(filepath.Join(home, ".claude", "settings.json"))
	installed, _ := config.LoadInstalledPlugins(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	// Discovery surfaces disabled plugins' agents too — only Enabled reflects runtime state.
	ags := Discover(filepath.Join(home, ".claude"), "", settings, installed, filepath.Join(home, ".claude", "plugins"))
	if len(ags) != 1 {
		t.Fatalf("want 1, got %d", len(ags))
	}
	if ags[0].PluginID != "ap@mkt" || ags[0].Scope != ScopePlugin {
		t.Errorf("unexpected: %+v", ags[0])
	}
}
