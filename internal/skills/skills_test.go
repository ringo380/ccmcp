package skills

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

func TestDiscoverUserAndProjectSkills(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()

	writeFile(t, filepath.Join(home, ".claude", "skills", "alpha", "SKILL.md"),
		"---\nname: alpha\ndescription: user skill\n---\nbody\n")
	writeFile(t, filepath.Join(proj, ".claude", "skills", "beta", "SKILL.md"),
		"---\nname: beta\ndescription: project skill\n---\nbody\n")

	skills := Discover(filepath.Join(home, ".claude"), proj, nil, nil, "")
	if len(skills) != 2 {
		t.Fatalf("want 2 skills, got %d: %+v", len(skills), skills)
	}
	if skills[0].Name != "alpha" || skills[0].Scope != ScopeUser {
		t.Errorf("skills[0]=%+v", skills[0])
	}
	if skills[1].Name != "beta" || skills[1].Scope != ScopeProject {
		t.Errorf("skills[1]=%+v", skills[1])
	}
	for _, s := range skills {
		if !s.Enabled {
			t.Errorf("%s should default to enabled", s.Name)
		}
	}
}

func TestDiscoverRespectsSkillOverrides(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "skills", "off-me", "SKILL.md"),
		"---\nname: off-me\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"skillOverrides": {"off-me": "off"}}`)

	settings, err := config.LoadSettings(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	skills := Discover(filepath.Join(home, ".claude"), "", settings, nil, "")
	if len(skills) != 1 {
		t.Fatalf("want 1, got %d", len(skills))
	}
	if skills[0].Enabled {
		t.Errorf("override should mark skill disabled")
	}
}

func TestDiscoverPluginScope(t *testing.T) {
	home := t.TempDir()
	// Fake a plugin install: settings says enabled, installed_plugins points at a path with skills/.
	pluginPath := filepath.Join(home, ".claude", "plugins", "cache", "mkt", "p", "1.0.0")
	writeFile(t, filepath.Join(pluginPath, "skills", "ship-it", "SKILL.md"),
		"---\nname: ship-it\ndescription: from plugin\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"enabledPlugins": {"p@mkt": true}}`)
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"p@mkt":[{"scope":"user","installPath":"`+pluginPath+`","version":"1.0.0"}]}}`)

	settings, _ := config.LoadSettings(filepath.Join(home, ".claude", "settings.json"))
	installed, _ := config.LoadInstalledPlugins(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	skills := Discover(filepath.Join(home, ".claude"), "", settings, installed, filepath.Join(home, ".claude", "plugins"))
	if len(skills) != 1 {
		t.Fatalf("want 1 plugin skill, got %d", len(skills))
	}
	if skills[0].Scope != ScopePlugin || skills[0].PluginID != "p@mkt" {
		t.Errorf("unexpected: %+v", skills[0])
	}
}
