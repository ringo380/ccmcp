package mcpscope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
)

// fixture writes a .claude.json plus an optional .mcp.json and returns Inputs
// pointing at them.
func fixture(t *testing.T, claudeDoc map[string]any, mcpjson map[string]any, stashEntries map[string]any, pluginMCPs map[string][]config.PluginMCPSource) Inputs {
	t.Helper()
	dir := t.TempDir()
	proj := filepath.Join(dir, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(path string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cjPath := filepath.Join(dir, ".claude.json")
	write(cjPath, claudeDoc)
	if mcpjson != nil {
		write(filepath.Join(proj, ".mcp.json"), map[string]any{"mcpServers": mcpjson})
	}
	stashPath := filepath.Join(dir, "stash.json")
	write(stashPath, map[string]any{"userMcpServers": stashEntries})

	cj, err := config.LoadClaudeJSON(cjPath)
	if err != nil {
		t.Fatal(err)
	}
	stash, err := config.LoadStash(stashPath)
	if err != nil {
		t.Fatal(err)
	}
	return Inputs{CJ: cj, Project: proj, Stash: stash, PluginMCPs: pluginMCPs}
}

func names(servers []Server) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEffectiveAppliesEveryScopeRule walks one fixture that exercises every
// source: what loads, what an override suppresses, and what is unprobeable but
// still real prompt cost.
func TestEffectiveAppliesEveryScopeRule(t *testing.T) {
	in := fixture(t,
		map[string]any{
			"mcpServers": map[string]any{
				"user-live": map[string]any{"command": "u"},
				"user-off":  map[string]any{"command": "u"},
			},
			"claudeAiMcpEverConnected": []any{"claude.ai Notion", "claude.ai Gmail"},
		},
		map[string]any{
			"shared-live":   map[string]any{"command": "s"},
			"shared-denied": map[string]any{"command": "s"},
		},
		map[string]any{"parked": map[string]any{"command": "p"}},
		map[string][]config.PluginMCPSource{
			"from-on-plugin": {{
				MCPName: "from-on-plugin", PluginID: "on@mkt",
				Config: map[string]any{"command": "pon"}, Enabled: true,
			}},
			"from-off-plugin": {{
				MCPName: "from-off-plugin", PluginID: "off@mkt",
				Config: map[string]any{"command": "poff"}, Enabled: false,
			}},
		},
	)
	// Project-scope config that only the loaded ClaudeJSON can carry.
	in.CJ.SetProjectMCP(in.Project, "local-live", map[string]any{"command": "l"})
	in.CJ.SetProjectDisabledMcpServers(in.Project, []string{"user-off", "shared-denied", "ghost-orphan"})
	in.CJ.SetProjectEnabledMcpServers(in.Project, []string{"computer-use"})

	got := names(Effective(in))
	want := []string{"Gmail", "Notion", "computer-use", "from-on-plugin", "local-live", "shared-live", "user-live"}
	if !equalStrings(got, want) {
		t.Fatalf("effective set = %v, want %v", got, want)
	}

	byName := map[string]Server{}
	for _, s := range Effective(in) {
		byName[s.Name] = s
	}
	if r := byName["Notion"].Reason; r == "" {
		t.Fatal("a claude.ai integration is effective but unprobeable - it must carry a reason")
	}
	if r := byName["computer-use"].Reason; r == "" {
		t.Fatal("a built-in enabled here is effective but unprobeable - it must carry a reason")
	}
	if byName["user-live"].Reason != "" {
		t.Fatalf("a live stdio server must be probeable, got reason %q", byName["user-live"].Reason)
	}
	if byName["user-live"].Config == nil {
		t.Fatal("a probeable server must carry its config")
	}
}

// TestEffectiveSuppressesABuiltinShadowedByAStashEntry pins the exact
// divergence review found between the CLI's first enumerator and the MCPs tab:
// the tab registers a stash entry under its plain name, so a name that is both
// stashed and listed in enabledMcpServers produces only the (never-effective)
// stash row. An enumerator that skips the stash names calls the same server
// effective-and-unmeasured, and the two surfaces then report different
// unmeasured counts for one project.
func TestEffectiveSuppressesABuiltinShadowedByAStashEntry(t *testing.T) {
	in := fixture(t,
		map[string]any{"mcpServers": map[string]any{}},
		nil,
		map[string]any{"parked-builtin": map[string]any{"command": "p"}},
		nil,
	)
	in.CJ.SetProjectEnabledMcpServers(in.Project, []string{"parked-builtin"})

	if got := names(Effective(in)); len(got) != 0 {
		t.Fatalf("a stashed name shadows the built-in row, so nothing loads here; got %v", got)
	}
}

// TestEffectiveExcludesOrphansAndStash: neither a stale disabledMcpServers key
// nor a stash entry loads, so neither may enter the accounting.
func TestEffectiveExcludesOrphansAndStash(t *testing.T) {
	in := fixture(t,
		map[string]any{"mcpServers": map[string]any{}},
		nil,
		map[string]any{"parked": map[string]any{"command": "p"}},
		nil,
	)
	in.CJ.SetProjectDisabledMcpServers(in.Project, []string{"plugin:gone:server", "vanished"})

	if got := names(Effective(in)); len(got) != 0 {
		t.Fatalf("stash entries and orphan override keys never load; got %v", got)
	}
}

// TestCostStatesCoversEveryServer is the completeness contract in one place:
// every effective server gets an entry, a cache miss and an unprobeable source
// are unmeasured with a reason, and a cached failure is never published as a
// measured zero.
func TestCostStatesCoversEveryServer(t *testing.T) {
	okCfg := map[string]any{"command": "good"}
	failCfg := map[string]any{"command": "bad"}
	servers := []Server{
		{Name: "measured", Config: okCfg},
		{Name: "failed", Config: failCfg},
		{Name: "missing", Config: map[string]any{"command": "never-probed"}},
		{Name: "remote", Reason: "claude.ai integration - cannot probe locally"},
	}

	cache, err := mcpprobe.LoadCache(filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	okTarget, _, _ := mcpprobe.TargetFromConfig("measured", okCfg)
	cache.Put(mcpprobe.Result{
		Key: okTarget.Key(), Name: "measured", ProbedAt: time.Now(), OK: true,
		InstructionTokens: 100, NameTokens: 10,
		Tools: []mcpprobe.Tool{{Name: "a", SchemaTokens: 400}},
	})
	failTarget, _, _ := mcpprobe.TargetFromConfig("failed", failCfg)
	cache.Put(mcpprobe.Result{
		Key: failTarget.Key(), Name: "failed", ProbedAt: time.Now(), OK: false,
		Err: "cannot start server",
	})

	states := CostStates(servers, cache, "not probed yet")
	if len(states) != len(servers) {
		t.Fatalf("every effective server needs an entry: got %d, want %d (%v)", len(states), len(servers), states)
	}
	if st := states["measured"]; !st.Probed || st.Cost.Loaded != 500 || st.Cost.Deferred != 110 {
		t.Fatalf("measured server = %+v, want probed 500/110", st)
	}
	for _, name := range []string{"failed", "missing", "remote"} {
		st := states[name]
		if st.Probed {
			t.Fatalf("%s must not be published as a measurement: %+v", name, st)
		}
		if st.Reason == "" {
			t.Fatalf("%s is unmeasured and must say why", name)
		}
		if st.Cost.Loaded != 0 || st.Cost.Deferred != 0 {
			t.Fatalf("%s carries a cost it never measured: %+v", name, st)
		}
	}
	if states["failed"].Reason != "cannot start server" {
		t.Fatalf("a cached failure should surface the server's own error, got %q", states["failed"].Reason)
	}
}

// TestCostStatesReportsDuplicateNamesAsUnmeasured: Input.MCP is keyed by display
// name, so two effective servers sharing one name cannot both be represented.
// Publishing the first one's measurement would read as a complete figure while a
// second real server vanished from the total.
func TestCostStatesReportsDuplicateNamesAsUnmeasured(t *testing.T) {
	aCfg := map[string]any{"command": "a"}
	bCfg := map[string]any{"command": "b"}
	servers := []Server{{Name: "same", Config: aCfg}, {Name: "same", Config: bCfg}}

	cache, err := mcpprobe.LoadCache(filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []map[string]any{aCfg, bCfg} {
		target, _, _ := mcpprobe.TargetFromConfig("same", cfg)
		cache.Put(mcpprobe.Result{
			Key: target.Key(), Name: "same", ProbedAt: time.Now(), OK: true,
			InstructionTokens: 900, Tools: []mcpprobe.Tool{{Name: "x", SchemaTokens: 1000}},
		})
	}

	states := CostStates(servers, cache, "not probed yet")
	st, ok := states["same"]
	if !ok {
		t.Fatal("the colliding name must still be accounted for")
	}
	if st.Probed {
		t.Fatalf("a colliding name must not be published as a measurement: %+v", st)
	}
	if st.Cost.Loaded != 0 {
		t.Fatalf("no cost may be attributed to a colliding name: %+v", st)
	}
	if want := "duplicate server name (2 sources)"; !contains(st.Reason, want) {
		t.Fatalf("reason %q should contain %q", st.Reason, want)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
