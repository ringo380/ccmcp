package tui

import (
	"sort"
	"testing"

	"github.com/ringo380/ccmcp/internal/mcpscope"
)

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
// The fixture below includes that case ("stashed-a" is in the stash AND in
// enabledMcpServers) alongside a genuine built-in that nothing else accounts for.
func TestMCPRowsAgreeWithMCPScope(t *testing.T) {
	st, _ := buildState(t)
	st.cj.SetProjectMCP(st.project, "local-live", map[string]any{"command": "l"})
	st.cj.SetProjectDisabledMcpServers(st.project, []string{"shared", "vanished-orphan"})
	// stashed-a is parked in the stash (see buildState) and also enabled here;
	// computer-use has no other source at all.
	st.cj.SetProjectEnabledMcpServers(st.project, []string{"stashed-a", "computer-use"})

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

	if len(fromRows) != len(fromScope) {
		t.Fatalf("tab and mcpscope disagree about what loads here:\n  rows:  %v\n  scope: %v", fromRows, fromScope)
	}
	for i := range fromRows {
		if fromRows[i] != fromScope[i] {
			t.Fatalf("tab and mcpscope disagree about what loads here:\n  rows:  %v\n  scope: %v", fromRows, fromScope)
		}
	}

	// Guard the fixture itself: if neither surface saw the interesting rows, the
	// comparison above would pass on two empty sets.
	seen := map[string]bool{}
	for _, n := range fromRows {
		seen[n] = true
	}
	if !seen["computer-use"] {
		t.Fatalf("fixture is not exercising the built-in path: %v", fromRows)
	}
	if seen["stashed-a"] {
		t.Fatalf("a stashed name must not resurface as a loading built-in: %v", fromRows)
	}
	if !seen["local-live"] || seen["shared"] {
		t.Fatalf("fixture is not exercising local scope and per-project overrides: %v", fromRows)
	}

	// And the published cost states must cover exactly that set - the
	// completeness contract, at the seam where the tab feeds the estimator.
	if len(st.mcpStates) != len(fromScope) {
		t.Fatalf("published %d cost states for %d loading servers: %v",
			len(st.mcpStates), len(fromScope), st.mcpStates)
	}
}
