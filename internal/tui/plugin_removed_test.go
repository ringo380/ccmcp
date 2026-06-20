package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/paths"
)

// buildStateRemovedFromMkt seeds a sandboxed home where the marketplace "mkt" is
// cached locally but lists only "keep" - so "gone@mkt" is an obsolete plugin.
// Both plugins have real on-disk cache dirs so the clean-removal path can be
// asserted to delete the obsolete one and preserve the kept one.
func buildStateRemovedFromMkt(t *testing.T) (*state, paths.Paths) {
	t.Helper()
	home := t.TempDir()
	write := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pluginsDir := filepath.Join(home, ".claude", "plugins")
	keepCache := filepath.Join(pluginsDir, "cache", "mkt", "keep")
	goneCache := filepath.Join(pluginsDir, "cache", "mkt", "gone")
	for _, d := range []string{keepCache, goneCache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "plugin.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(home, ".claude.json"), map[string]any{"anonymousId": "sandbox"})
	write(filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"keep@mkt": true,
			"gone@mkt": true,
		},
	})
	write(filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			"keep@mkt": []any{map[string]any{"scope": "user", "installPath": keepCache, "version": "1.0"}},
			"gone@mkt": []any{map[string]any{"scope": "user", "installPath": goneCache, "version": "1.0"}},
		},
	})
	// Marketplace cached locally, lists only "keep".
	mktManifest := filepath.Join(pluginsDir, "marketplaces", "mkt", ".claude-plugin", "marketplace.json")
	write(mktManifest, map[string]any{
		"name":    "mkt",
		"plugins": []any{map[string]any{"name": "keep", "source": "./plugins/keep"}},
	})

	p := paths.Paths{
		Home:             home,
		ClaudeConfigDir:  filepath.Join(home, ".claude"),
		ClaudeJSON:       filepath.Join(home, ".claude.json"),
		SettingsJSON:     filepath.Join(home, ".claude", "settings.json"),
		SettingsLocal:    filepath.Join(home, ".claude", "settings.local.json"),
		PluginsDir:       pluginsDir,
		InstalledPlugins: filepath.Join(pluginsDir, "installed_plugins.json"),
		KnownMarkets:     filepath.Join(pluginsDir, "known_marketplaces.json"),
		Stash:            filepath.Join(home, ".claude-mcp-stash.json"),
		Profiles:         filepath.Join(home, ".claude-mcp-profiles.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
	}
	st, err := loadState(p, filepath.Join(home, "project"))
	if err != nil {
		t.Fatal(err)
	}
	return st, p
}

func TestPluginsTabShowsRemovedFromMarketplace(t *testing.T) {
	st, _ := buildStateRemovedFromMkt(t)
	m := newModel(st)

	view := stripANSI(drive(m, "2")) // jump to Plugins tab
	if !strings.Contains(view, "removed from marketplace") {
		t.Fatalf("expected 'removed from marketplace' indicator; got:\n%s", view)
	}
}

func TestPluginsTabCleanRemovesObsoletePlugin(t *testing.T) {
	st, _ := buildStateRemovedFromMkt(t)
	m := newModel(st)

	// gone@mkt sorts before keep@mkt, so cursor index 0 selects it. x x confirms.
	drive(m, "2", "x", "x")

	for _, ip := range st.installed.List() {
		if ip.ID == "gone@mkt" {
			t.Fatalf("gone@mkt should have been removed from installed_plugins; still present")
		}
	}
	if _, known := st.settings.PluginEnabled("gone@mkt"); known {
		t.Errorf("gone@mkt should have been removed from enabledPlugins")
	}
	// Obsolete plugin's cache dir deleted; kept plugin's cache preserved.
	goneCache := filepath.Join(st.paths.PluginsDir, "cache", "mkt", "gone")
	if _, err := os.Stat(goneCache); !os.IsNotExist(err) {
		t.Errorf("gone cache dir should be deleted; stat err=%v", err)
	}
	keepCache := filepath.Join(st.paths.PluginsDir, "cache", "mkt", "keep")
	if _, err := os.Stat(keepCache); err != nil {
		t.Errorf("keep cache dir should be preserved; stat err=%v", err)
	}
	if !st.dirtyPlugins || !st.dirtySettings {
		t.Errorf("dirty flags should be set after removal: plugins=%v settings=%v", st.dirtyPlugins, st.dirtySettings)
	}
}

func TestSummaryTabFlagsRemovedFromMarketplace(t *testing.T) {
	st, _ := buildStateRemovedFromMkt(t)
	m := newModel(st)

	view := stripANSI(drive(m, "9")) // Summary tab
	if !strings.Contains(view, "removed from marketplace") {
		t.Fatalf("expected Summary 'removed from marketplace' row; got:\n%s", view)
	}
}
