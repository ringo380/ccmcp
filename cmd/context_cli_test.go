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

// TestCLIContextMarksDisabledPlugins pins the honesty of the "heaviest
// plugins" list: ctxcost.Build keeps disabled plugins in ByPlugin (so a UI can
// show what enabling one would add) but excludes them from the project total,
// so an unmarked row makes the list fail to sum to the printed total. The TUI
// conveys this by dimming the row; the CLI needs a marker and a JSON flag.
func TestCLIContextMarksDisabledPlugins(t *testing.T) {
	home := setupSandbox(t)
	claudeDir := filepath.Join(home, ".claude")
	pluginsRoot := filepath.Join(home, "test-plugins")

	write := func(id, skill, desc string) string {
		installPath := filepath.Join(pluginsRoot, id)
		skillDir := filepath.Join(installPath, "skills", skill)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + skill + "\ndescription: " + desc + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return installPath
	}
	onPath := write("on-plugin@mkt", "on-skill", "an enabled plugin skill")
	offPath := write("off-plugin@mkt", "off-skill", "a disabled plugin skill that loads nothing")

	settingsBytes, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]any{
		"on-plugin@mkt":  true,
		"off-plugin@mkt": false,
	}})
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settingsBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	installedBytes, _ := json.Marshal(map[string]any{"version": float64(2), "plugins": map[string]any{
		"on-plugin@mkt":  []any{map[string]any{"scope": "user", "installPath": onPath, "version": "1.0"}},
		"off-plugin@mkt": []any{map[string]any{"scope": "user", "installPath": offPath, "version": "1.0"}},
	}})
	if err := os.WriteFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json"), installedBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, out)
	}
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(ln, "off-plugin@mkt"):
			if !strings.Contains(ln, "(disabled)") {
				t.Fatalf("the disabled plugin's row must be marked, or it looks like it counts toward the total: %q", ln)
			}
		case strings.Contains(ln, "on-plugin@mkt"):
			if strings.Contains(ln, "(disabled)") {
				t.Fatalf("an enabled plugin must not be marked disabled: %q", ln)
			}
		}
	}

	jsonOut, err := runCLI(t, home, "context", "--json")
	if err != nil {
		t.Fatalf("context --json: %v\n%s", err, jsonOut)
	}
	var payload struct {
		ByPlugin map[string]struct {
			Enabled bool `json:"enabled"`
		} `json:"byPlugin"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, jsonOut)
	}
	if payload.ByPlugin["off-plugin@mkt"].Enabled {
		t.Fatalf("off-plugin@mkt must carry enabled=false:\n%s", jsonOut)
	}
	if !payload.ByPlugin["on-plugin@mkt"].Enabled {
		t.Fatalf("on-plugin@mkt must carry enabled=true:\n%s", jsonOut)
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
