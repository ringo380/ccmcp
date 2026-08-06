package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIContextListsHeaviestPluginsFirst(t *testing.T) {
	// setupSandbox + runCLI are the existing helpers in cmd/cli_test.go:15.
	// runCLI resets cobra's package-global flag vars between Execute() calls.
	home := setupSandbox(t)
	out, err := runCLI(t, home, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, out)
	}
	if !strings.Contains(out, "per-turn context") {
		t.Fatalf("missing total line:\n%s", out)
	}
	if !strings.Contains(out, "≈") {
		t.Fatalf("figures must be estimates:\n%s", out)
	}
}

// TestCLIContextSortsPluginsByCostDescending builds a sandbox with three
// enabled plugins whose only skill descriptions differ enormously in length -
// large, medium, and tiny - so their token costs are unambiguously ordered
// (large is many multiples of medium, which is many multiples of tiny). It
// then asserts the "heaviest plugins" rows in the command's human output
// appear in that exact descending-cost order, not merely that the plugin
// names or the "per-turn context" banner are present somewhere in the
// output. A regression that reverses or drops the sort in cmd/context.go
// must fail this test.
func TestCLIContextSortsPluginsByCostDescending(t *testing.T) {
	home := setupSandbox(t)
	claudeDir := filepath.Join(home, ".claude")
	pluginsRoot := filepath.Join(home, "test-plugins")

	type plugin struct {
		id    string
		skill string
		desc  string
	}
	// Description lengths are chosen so the resulting token counts are
	// unmistakably ordered heavy > medium > light, well beyond any possible
	// tokenizer rounding noise.
	plugins := []plugin{
		{"heavy-plugin@mkt", "heavy-skill", strings.Repeat("expansive contextual payload words describing this skill in exhaustive detail ", 60)},
		{"medium-plugin@mkt", "medium-skill", strings.Repeat("moderately detailed skill description phrase ", 12)},
		{"light-plugin@mkt", "light-skill", "tiny skill"},
	}

	enabled := map[string]any{}
	installedPlugins := map[string]any{}
	for _, p := range plugins {
		installPath := filepath.Join(pluginsRoot, p.id)
		skillDir := filepath.Join(installPath, "skills", p.skill)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + p.skill + "\ndescription: " + p.desc + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		enabled[p.id] = true
		installedPlugins[p.id] = []any{map[string]any{
			"scope":       "user",
			"installPath": installPath,
			"version":     "1.0",
		}}
	}

	settingsBytes, err := json.Marshal(map[string]any{"enabledPlugins": enabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settingsBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	installedBytes, err := json.Marshal(map[string]any{"version": float64(2), "plugins": installedPlugins})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json"), installedBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, out)
	}

	positions := map[string]int{}
	for _, p := range plugins {
		i := strings.Index(out, p.id)
		if i < 0 {
			t.Fatalf("plugin %s missing from output:\n%s", p.id, out)
		}
		positions[p.id] = i
	}
	if !(positions["heavy-plugin@mkt"] < positions["medium-plugin@mkt"] &&
		positions["medium-plugin@mkt"] < positions["light-plugin@mkt"]) {
		t.Fatalf("expected heavy, medium, light order (descending cost); got positions %v in:\n%s",
			positions, out)
	}
}

func TestCLIContextJSONHasProjectTotal(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "context", "--json")
	if err != nil {
		t.Fatalf("context --json: %v\n%s", err, out)
	}
	for _, key := range []string{`"project"`, `"byPlugin"`, `"loaded"`, `"deferred"`} {
		if !strings.Contains(out, key) {
			t.Fatalf("JSON missing %s:\n%s", key, out)
		}
	}
}
