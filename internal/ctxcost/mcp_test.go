package ctxcost

import (
	"testing"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/skills"
)

func TestBuildCountsProbedServersIntoProjectMCP(t *testing.T) {
	// Two servers with DIFFERENT, asymmetric cost numbers: a hoisted (shared)
	// accumulator bug would make one server's row bleed into the other's, and
	// with only one server (or matching numbers) that bug is invisible.
	in := Input{
		MCP: map[string]MCPState{
			"widgets": {Probed: true, Cost: Cost{Loaded: 100, Deferred: 20}},
			"gadgets": {Probed: true, Cost: Cost{Loaded: 400, Deferred: 30}},
		},
	}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if idx.Project.MCP.Loaded != 500 || idx.Project.MCP.Deferred != 50 {
		t.Fatalf("Project.MCP = %+v, want {500 50}", idx.Project.MCP)
	}
	if idx.Project.Items != 2 {
		t.Fatalf("Project.Items = %d, want 2", idx.Project.Items)
	}
	if idx.Project.Unmeasured != 0 {
		t.Fatalf("Project.Unmeasured = %d, want 0", idx.Project.Unmeasured)
	}

	widgets, ok := idx.ByMCP["widgets"]
	if !ok {
		t.Fatalf("ByMCP missing entry for widgets")
	}
	if widgets.MCP.Loaded != 100 || widgets.MCP.Deferred != 20 {
		t.Fatalf("ByMCP[widgets].MCP = %+v, want {100 20}", widgets.MCP)
	}
	gadgets, ok := idx.ByMCP["gadgets"]
	if !ok {
		t.Fatalf("ByMCP missing entry for gadgets")
	}
	if gadgets.MCP.Loaded != 400 || gadgets.MCP.Deferred != 30 {
		t.Fatalf("ByMCP[gadgets].MCP = %+v, want {400 30}", gadgets.MCP)
	}

	// Per-server entries must sum to the project total by construction.
	var sumLoaded, sumDeferred int
	for _, e := range idx.ByMCP {
		sumLoaded += e.MCP.Loaded
		sumDeferred += e.MCP.Deferred
	}
	if sumLoaded != idx.Project.MCP.Loaded || sumDeferred != idx.Project.MCP.Deferred {
		t.Fatalf("ByMCP sums (%d,%d) != Project.MCP (%d,%d)", sumLoaded, sumDeferred, idx.Project.MCP.Loaded, idx.Project.MCP.Deferred)
	}
}

// TestBuildLeavesAbsentServerInvisible pins the real (and only achievable)
// behavior for a server that never appears in Input.MCP at all: Build has no
// independent list of configured servers, so an absent key contributes to
// neither the total nor Unmeasured - it is not the same thing as
// Probed:false, which IS counted in Unmeasured. A caller that only populates
// cache hits into Input.MCP will silently omit the rest of the fleet from the
// estimate rather than flagging them as unmeasured; this test exists so a
// future reader cannot mistake "absent" for "unmeasured".
func TestBuildLeavesAbsentServerInvisible(t *testing.T) {
	in := Input{MCP: map[string]MCPState{}} // "phantom" server never listed
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if idx.Project.Total() != (Cost{}) {
		t.Fatalf("Project.Total() = %+v, want zero", idx.Project.Total())
	}
	if idx.Project.Unmeasured != 0 {
		t.Fatalf("Project.Unmeasured = %d, want 0 - an absent server must not inflate Unmeasured", idx.Project.Unmeasured)
	}
	if _, ok := idx.ByMCP["phantom"]; ok {
		t.Fatalf("ByMCP contains an entry for a server that was never in Input.MCP")
	}
}

func TestBuildRoutesUnprobedServersToUnmeasured(t *testing.T) {
	in := Input{
		MCP: map[string]MCPState{
			"broken": {Probed: false, Reason: "probe timed out"},
		},
	}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if idx.Project.Total() != (Cost{}) {
		t.Fatalf("Project.Total() = %+v, want zero", idx.Project.Total())
	}
	if idx.Project.Unmeasured != 1 {
		t.Fatalf("Project.Unmeasured = %d, want 1", idx.Project.Unmeasured)
	}
	if idx.Project.Items != 0 {
		t.Fatalf("Project.Items = %d, want 0", idx.Project.Items)
	}

	b, ok := idx.ByMCP["broken"]
	if !ok {
		t.Fatalf("ByMCP missing entry for broken")
	}
	if b.Unmeasured != 1 {
		t.Fatalf("ByMCP[broken].Unmeasured = %d, want 1", b.Unmeasured)
	}
	if b.Total() != (Cost{}) {
		t.Fatalf("ByMCP[broken].Total() = %+v, want zero", b.Total())
	}
}

func TestBuildKeepsAssetTotalsUnchangedWhenMCPIsEmpty(t *testing.T) {
	// Real assets (one skill, one agent, one command) so this test can
	// actually detect a perturbed asset figure - Input{} with zero assets
	// would pass under almost any bug because zero stays zero.
	assetsIn := Input{
		Skills:   []skills.Skill{{Name: "alpha", Description: "does the alpha thing", Enabled: true}},
		Agents:   []agents.Agent{{Name: "helper", Description: "helps out", Enabled: true}},
		Commands: []commands.Command{{Name: "deploy", Effective: "deploy", Description: "ships it"}},
	}

	withoutMCP, err := Build(assetsIn)
	if err != nil {
		t.Fatalf("Build (no MCP): %v", err)
	}

	withMCP := assetsIn
	withMCP.MCP = map[string]MCPState{
		"widgets": {Probed: true, Cost: Cost{Loaded: 100, Deferred: 20}},
		"broken":  {Probed: false, Reason: "probe timed out"},
	}
	idx, err := Build(withMCP)
	if err != nil {
		t.Fatalf("Build (with MCP): %v", err)
	}

	wantSkills := withoutMCP.Project.Skills
	wantAgents := withoutMCP.Project.Agents
	wantCommands := withoutMCP.Project.Commands
	if wantSkills == (Cost{}) || wantAgents == (Cost{}) || wantCommands == (Cost{}) {
		t.Fatalf("fixture produced a zero asset figure, test cannot detect perturbation: skills=%+v agents=%+v commands=%+v", wantSkills, wantAgents, wantCommands)
	}

	if idx.Project.Skills != wantSkills {
		t.Fatalf("Project.Skills = %+v, want %+v (unchanged by MCP accounting)", idx.Project.Skills, wantSkills)
	}
	if idx.Project.Agents != wantAgents {
		t.Fatalf("Project.Agents = %+v, want %+v (unchanged by MCP accounting)", idx.Project.Agents, wantAgents)
	}
	if idx.Project.Commands != wantCommands {
		t.Fatalf("Project.Commands = %+v, want %+v (unchanged by MCP accounting)", idx.Project.Commands, wantCommands)
	}
}

func TestMCPStateFromFailedProbeIsUnmeasuredEvenWithNonzeroCost(t *testing.T) {
	// A failed probe must never surface a confident zero (or any) cost - the
	// numbers here are deliberately nonzero to prove the failure path discards
	// them rather than merely defaulting an already-zero Result.
	st := MCPStateFrom(false, "context deadline exceeded", 500, 100)
	if st.Probed {
		t.Fatalf("Probed = true, want false for a failed probe")
	}
	if st.Cost != (Cost{}) {
		t.Fatalf("Cost = %+v, want zero for a failed probe", st.Cost)
	}
	if st.Reason == "" {
		t.Fatalf("Reason is empty, want the failure reason surfaced")
	}
}

func TestMCPStateFromOKProbeCarriesCost(t *testing.T) {
	st := MCPStateFrom(true, "", 100, 20)
	if !st.Probed {
		t.Fatalf("Probed = false, want true for a successful probe")
	}
	if st.Cost != (Cost{Loaded: 100, Deferred: 20}) {
		t.Fatalf("Cost = %+v, want {100 20}", st.Cost)
	}
	if st.Reason != "" {
		t.Fatalf("Reason = %q, want empty on success", st.Reason)
	}
}
