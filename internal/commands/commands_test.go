package commands

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

func TestDiscoverUserAndPluginCommands(t *testing.T) {
	home := t.TempDir()
	// User command: /commit
	writeFile(t, filepath.Join(home, ".claude", "commands", "commit.md"),
		"---\ndescription: make a commit\n---\n")
	// Plugin command: /superpowers:brainstorm
	pluginPath := filepath.Join(home, ".claude", "plugins", "cache", "official", "superpowers", "1.0.0")
	writeFile(t, filepath.Join(pluginPath, "commands", "brainstorm.md"),
		"---\ndescription: brainstorm\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"enabledPlugins": {"superpowers@official": true}}`)
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"superpowers@official":[{"scope":"user","installPath":"`+pluginPath+`"}]}}`)

	settings, _ := config.LoadSettings(filepath.Join(home, ".claude", "settings.json"))
	installed, _ := config.LoadInstalledPlugins(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	cmds := Discover(filepath.Join(home, ".claude"), "", settings, installed, filepath.Join(home, ".claude", "plugins"))
	if len(cmds) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(cmds), cmds)
	}

	byEff := map[string]Command{}
	for _, c := range cmds {
		byEff[c.Effective] = c
	}
	if c, ok := byEff["commit"]; !ok || c.Scope != ScopeUser {
		t.Errorf("missing or wrong scope for /commit: %+v", c)
	}
	if c, ok := byEff["superpowers:brainstorm"]; !ok || c.Scope != ScopePlugin {
		t.Errorf("missing or wrong scope for plugin cmd: %+v", c)
	}
}
