package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/mcpscope"
	"github.com/ringo380/ccmcp/internal/paths"
)

// buildAgreementState is a purpose-built sandbox for the CLI/TUI agreement test.
//
// It does NOT reuse buildState: that fixture registers its plugins at
// non-existent installPaths ("/x/1"), so config.ScanAllInstalledPluginMCPs finds
// nothing and st.pluginMCPs comes back EMPTY - which silently left the
// plugin-sourced leg of the comparison untested. Proven, not assumed: dropping
// mcpscope's plugin-Enabled gate left both ./internal/tui and ./cmd green and was
// caught only by mcpscope's own unit test.
//
// So the plugins here are written to real directories with real .mcp.json files,
// one plugin enabled and one globally disabled (the case the Enabled gate exists
// for), and claudeAiMcpEverConnected is seeded - one integration live, one
// suppressed by this project's overrides.
func buildAgreementState(t *testing.T) *state {
	t.Helper()
	home := t.TempDir()
	proj := filepath.Join(home, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Two installed plugins, each shipping one MCP server, at paths that exist.
	pluginRoot := filepath.Join(home, "plugin-installs")
	onPath := filepath.Join(pluginRoot, "on-plugin")
	offPath := filepath.Join(pluginRoot, "off-plugin")
	write(filepath.Join(onPath, ".mcp.json"), map[string]any{
		"mcpServers": map[string]any{"from-on-plugin": map[string]any{"command": "pon"}},
	})
	write(filepath.Join(offPath, ".mcp.json"), map[string]any{
		"mcpServers": map[string]any{"from-off-plugin": map[string]any{"command": "poff"}},
	})

	write(filepath.Join(home, ".claude.json"), map[string]any{
		"anonymousId": "sandbox",
		"mcpServers": map[string]any{
			"user-live": map[string]any{"command": "u"},
			"user-off":  map[string]any{"command": "u"},
		},
		"claudeAiMcpEverConnected": []any{"claude.ai Notion", "claude.ai Gmail"},
		"projects": map[string]any{
			proj: map[string]any{
				"mcpServers":            map[string]any{"local-live": map[string]any{"command": "l"}},
				"disabledMcpServers":    []any{"user-off", "claude.ai Gmail", "vanished-orphan"},
				"enabledMcpServers":     []any{"parked-builtin", "computer-use"},
				"enabledMcpjsonServers": []any{},
			},
		},
	})
	// parked-builtin is stashed AND listed in enabledMcpServers - the exact
	// divergence this test exists for.
	write(filepath.Join(home, ".claude-mcp-stash.json"), map[string]any{
		"userMcpServers": map[string]any{"parked-builtin": map[string]any{"command": "p"}},
	})
	write(filepath.Join(home, ".claude-mcp-profiles.json"), map[string]any{})
	write(filepath.Join(home, ".claude-mcp-config.json"), map[string]any{})
	write(filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"on-plugin@mkt":  true,
			"off-plugin@mkt": false,
		},
	})
	write(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			"on-plugin@mkt":  []any{map[string]any{"scope": "user", "installPath": onPath, "version": "1.0"}},
			"off-plugin@mkt": []any{map[string]any{"scope": "user", "installPath": offPath, "version": "1.0"}},
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
		ProbeCache:       filepath.Join(home, ".claude-mcp-probe-cache.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
		AppConfig:        filepath.Join(home, ".claude-mcp-config.json"),
	}
	st, err := loadState(p, proj)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// diffNames reports which names are in a but not b, and vice versa, so a failure
// says WHICH entries disagree rather than only that two counts differ.
func diffNames(a, b []string) (onlyA, onlyB []string) {
	inB := map[string]bool{}
	for _, n := range b {
		inB[n] = true
	}
	inA := map[string]bool{}
	for _, n := range a {
		inA[n] = true
	}
	for n := range inA {
		if !inB[n] {
			onlyA = append(onlyA, n)
		}
	}
	for n := range inB {
		if !inA[n] {
			onlyB = append(onlyB, n)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}

// TestMCPRowsAgreeWithMCPScope pins the two surfaces to one answer.
//
// The tab paints rows and gates them with isEffective; `ccmcp context` and
// `ccmcp mcp probe` read internal/mcpscope. If those drift, the tab and the CLI
// report different per-turn figures for the same project - which is exactly what
// happened before mcpscope existed: a name that was both stashed and listed in
// enabledMcpServers was suppressed here (the stash row accounts for the plain
// name, so no built-in row is emitted) and effective in the CLI, so the two
// printed different unmeasured counts.
//
// The fixture covers every source that can produce an effective row - user,
// local, plugin (enabled AND globally disabled), claude.ai (live and overridden),
// stash-shadowed built-in, genuine built-in, orphan key - because a comparison is
// only as strong as the rows it actually contains.
func TestMCPRowsAgreeWithMCPScope(t *testing.T) {
	st := buildAgreementState(t)
	v := newMCPView(st)

	var fromRows []string
	for _, r := range v.rows {
		if isEffective(r) {
			fromRows = append(fromRows, r.Name)
		}
	}
	sort.Strings(fromRows)

	var fromScope []string
	for _, s := range mcpscope.Effective(mcpscope.Inputs{
		CJ:         st.cj,
		Project:    st.project,
		Stash:      st.stash,
		PluginMCPs: st.pluginMCPs,
	}) {
		fromScope = append(fromScope, s.Name)
	}
	sort.Strings(fromScope)

	if onlyRows, onlyScope := diffNames(fromRows, fromScope); len(onlyRows) > 0 || len(onlyScope) > 0 {
		t.Fatalf("tab and mcpscope disagree about what loads here:\n  only in tab rows: %v\n  only in mcpscope: %v\n  rows:  %v\n  scope: %v",
			onlyRows, onlyScope, fromRows, fromScope)
	}

	// Guard the fixture itself: without this, the comparison above would pass on
	// two empty sets, and every leg it claims to cover has to be present.
	seen := map[string]bool{}
	for _, n := range fromRows {
		seen[n] = true
	}
	for _, want := range []string{"user-live", "local-live", "from-on-plugin", "Notion", "computer-use"} {
		if !seen[want] {
			t.Fatalf("fixture is not exercising %q - the comparison is weaker than it claims: %v", want, fromRows)
		}
	}
	for _, unwanted := range []string{"user-off", "from-off-plugin", "Gmail", "parked-builtin", "vanished-orphan"} {
		if seen[unwanted] {
			t.Fatalf("%q must not load here: %v", unwanted, fromRows)
		}
	}

	// And the published cost states must cover exactly that set - the
	// completeness contract, at the seam where the tab feeds the estimator.
	// Compared as NAME SETS, not lengths: CostStates is keyed per name, so a
	// duplicate-name row added to this fixture later would make a length
	// comparison false-fail instead of reporting a real disagreement.
	var published []string
	for name := range st.mcpStates {
		published = append(published, name)
	}
	sort.Strings(published)
	if onlyPub, onlyScope := diffNames(published, fromScope); len(onlyPub) > 0 || len(onlyScope) > 0 {
		t.Fatalf("published cost states do not cover the loading set:\n  only published: %v\n  only loading:   %v\n  states: %v",
			onlyPub, onlyScope, published)
	}
	// Every unmeasured entry must say why, or the tab renders a bare "-".
	for name, stt := range st.mcpStates {
		if !stt.Probed && strings.TrimSpace(stt.Reason) == "" {
			t.Fatalf("%s is unmeasured with no reason", name)
		}
	}
}
