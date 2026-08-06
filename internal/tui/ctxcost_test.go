package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ringo380/ccmcp/internal/paths"
)

func TestCostIndexIsCachedAndInvalidated(t *testing.T) {
	st, _ := buildState(t) // existing helper, internal/tui/tui_test.go:20

	first := st.costIndex()
	if first == nil {
		t.Fatal("costIndex returned nil")
	}
	if second := st.costIndex(); second != first {
		t.Fatal("costIndex must return the cached pointer on the second call")
	}

	st.invalidateCost()
	if third := st.costIndex(); third == first {
		t.Fatal("costIndex must rebuild after invalidateCost")
	}
}

// TestCostIndexStaleOnSettingsMutation covers the gap the explicit
// invalidateCost() call site does not: a skill/agent override mutation changes
// ctxcost's Enabled filter (internal/ctxcost/assets.go) without ever touching
// pluginMCPs. Any of the 14 dirtySettings=true sites in internal/tui/ can
// trigger this, so the cache must detect the flag change itself rather than
// rely on being invalidated at every mutation site.
func TestCostIndexStaleOnSettingsMutation(t *testing.T) {
	st, _ := buildState(t)

	first := st.costIndex()
	if first == nil {
		t.Fatal("costIndex returned nil")
	}

	// Mutate skill-override state the way skillView does, without calling
	// invalidateCost() directly - only flipping dirtySettings, as the real
	// mutation sites do.
	st.settings.SetSkillOverride("some-skill", "off")
	st.markSettingsDirty()

	if second := st.costIndex(); second == first {
		t.Fatal("costIndex must rebuild after a settings mutation (dirtySettings flip), even without an explicit invalidateCost() call")
	}
}

// TestCostIndexRebuildsOnEverySuccessiveMutation is the regression for a cache
// that keyed off the pending-write booleans dirtySettings/dirtyPlugins instead
// of a version. Those only ever transition false->true, so the FIRST mutation
// was detected and every later one was not: the guard compared true == true and
// handed back the stale index. A user disabling skills one at a time saw the
// total move once and then freeze.
func TestCostIndexRebuildsOnEverySuccessiveMutation(t *testing.T) {
	home := t.TempDir()
	writeUserSkill := func(name, desc string) {
		dir := filepath.Join(home, ".claude", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeUserSkill("alpha", "the first user skill, costed into the project total")
	writeUserSkill("beta", "the second user skill, costed into the project total")
	must(filepath.Join(home, ".claude.json"), map[string]any{"anonymousId": "sandbox"})
	must(filepath.Join(home, ".claude-mcp-stash.json"), map[string]any{})
	must(filepath.Join(home, ".claude-mcp-profiles.json"), map[string]any{})
	must(filepath.Join(home, ".claude-mcp-config.json"), map[string]any{})
	must(filepath.Join(home, ".claude", "settings.json"), map[string]any{})
	must(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), map[string]any{
		"version": float64(2), "plugins": map[string]any{},
	})

	p := paths.Paths{
		Home:             home,
		ClaudeConfigDir:  filepath.Join(home, ".claude"),
		ClaudeJSON:       filepath.Join(home, ".claude.json"),
		SettingsJSON:     filepath.Join(home, ".claude", "settings.json"),
		SettingsLocal:    filepath.Join(home, ".claude", "settings.local.json"),
		PluginsDir:       filepath.Join(home, ".claude", "plugins"),
		InstalledPlugins: filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		KnownMarkets:     filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"),
		Stash:            filepath.Join(home, ".claude-mcp-stash.json"),
		Profiles:         filepath.Join(home, ".claude-mcp-profiles.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
		AppConfig:        filepath.Join(home, ".claude-mcp-config.json"),
	}
	st, err := loadState(p, filepath.Join(home, "project"))
	if err != nil {
		t.Fatal(err)
	}

	base := st.costIndex().Project.Total().Loaded
	if base == 0 {
		t.Fatal("expected a non-zero baseline total from the two user skills")
	}

	// First mutation: the old boolean guard caught this one.
	st.settings.SetSkillOverride("alpha", "off")
	st.markSettingsDirty()
	afterFirst := st.costIndex().Project.Total().Loaded
	if afterFirst >= base {
		t.Fatalf("total must drop after disabling alpha: base=%d after=%d", base, afterFirst)
	}

	// Second mutation, with dirtySettings ALREADY true - the case the boolean
	// guard could not see.
	st.settings.SetSkillOverride("beta", "off")
	st.markSettingsDirty()
	afterSecond := st.costIndex().Project.Total().Loaded
	if afterSecond >= afterFirst {
		t.Fatalf("total must drop again after disabling beta: after-first=%d after-second=%d (stale cache)", afterFirst, afterSecond)
	}
}

// TestMeasuredIsCachedBetweenRenders pins the calibration cache: both tab
// renders call measured() unconditionally, and ctxcost.Calibrate scans the
// whole newest transcript. Deleting the transcript after the first call must
// not change the answer, which proves the second call did no disk I/O.
func TestMeasuredIsCachedBetweenRenders(t *testing.T) {
	st, p := buildState(t)
	dir := filepath.Join(p.ClaudeConfigDir, "projects", paths.TranscriptSlug(st.project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(transcript, []byte(
		`{"message":{"usage":{"cache_creation_input_tokens":100,"cache_read_input_tokens":23}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, ok := st.measured()
	if !ok || m.PrefixTokens != 123 {
		t.Fatalf("first measured() = %+v ok=%v, want 123", m, ok)
	}
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	m2, ok2 := st.measured()
	if !ok2 || m2.PrefixTokens != 123 {
		t.Fatalf("second measured() = %+v ok=%v, want the cached 123 (no per-frame disk I/O)", m2, ok2)
	}

	// A mutation must still invalidate it - the transcript is gone now, so the
	// rebuilt answer is ok=false.
	st.markSettingsDirty()
	if _, ok3 := st.measured(); ok3 {
		t.Fatal("measured() must rebuild after a mutation, not serve the stale cache forever")
	}
}

// TestCostIndexExcludesDisabledPluginFromProjectTotal is the TUI-level
// regression proof for the plan-level correctness bug: skills.Discover
// deliberately surfaces registered-but-disabled plugins' assets (with
// Enabled=true, since that field reflects only skillOverrides), so
// costIndex() must wire settings.PluginEnabled through to ctxcost.Build's
// PluginEnabled hook, or a disabled plugin's cost leaks into the project
// total that never actually loads into the prompt.
//
// ByPlugin must still carry the disabled plugin's cost (so the UI can show
// what enabling it would add); only Project must exclude it.
func TestCostIndexExcludesDisabledPluginFromProjectTotal(t *testing.T) {
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
	writeSkill := func(installPath, name, desc string) {
		dir := filepath.Join(installPath, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	enabledInstall := filepath.Join(home, "enabled-install")
	disabledInstall := filepath.Join(home, "disabled-install")
	writeSkill(enabledInstall, "on-skill", "ships from the enabled plugin")
	writeSkill(disabledInstall, "off-skill", "ships from the disabled plugin, must not load")

	must(filepath.Join(home, ".claude.json"), map[string]any{"anonymousId": "sandbox"})
	must(filepath.Join(home, ".claude-mcp-stash.json"), map[string]any{})
	must(filepath.Join(home, ".claude-mcp-profiles.json"), map[string]any{})
	must(filepath.Join(home, ".claude-mcp-config.json"), map[string]any{})
	must(filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"enabled@mkt":  true,
			"disabled@mkt": false,
		},
	})
	must(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			"enabled@mkt":  []any{map[string]any{"scope": "user", "installPath": enabledInstall, "version": "1.0"}},
			"disabled@mkt": []any{map[string]any{"scope": "user", "installPath": disabledInstall, "version": "1.0"}},
		},
	})

	p := paths.Paths{
		Home:             home,
		ClaudeConfigDir:  filepath.Join(home, ".claude"),
		ClaudeJSON:       filepath.Join(home, ".claude.json"),
		SettingsJSON:     filepath.Join(home, ".claude", "settings.json"),
		SettingsLocal:    filepath.Join(home, ".claude", "settings.local.json"),
		PluginsDir:       filepath.Join(home, ".claude", "plugins"),
		InstalledPlugins: filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		KnownMarkets:     filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"),
		Stash:            filepath.Join(home, ".claude-mcp-stash.json"),
		Profiles:         filepath.Join(home, ".claude-mcp-profiles.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
	}
	st, err := loadState(p, filepath.Join(home, "project"))
	if err != nil {
		t.Fatal(err)
	}

	idx := st.costIndex()

	enabledCost := idx.ByPlugin["enabled@mkt"].Total().Loaded
	disabledCost := idx.ByPlugin["disabled@mkt"].Total().Loaded
	if enabledCost == 0 {
		t.Fatalf("enabled@mkt must have non-zero cost in ByPlugin, got %+v", idx.ByPlugin["enabled@mkt"])
	}
	if disabledCost == 0 {
		t.Fatalf("disabled@mkt must still have non-zero cost in ByPlugin (shows what enabling it would add), got %+v", idx.ByPlugin["disabled@mkt"])
	}

	if got := idx.Project.Total().Loaded; got != enabledCost {
		t.Fatalf("Project.Total().Loaded = %d, want exactly the enabled plugin's cost (%d); disabled plugin's cost (%d) must be excluded from the header total",
			got, enabledCost, disabledCost)
	}
}
