package ctxcost

import "testing"

func TestBuildCountsProbedServersIntoProjectMCP(t *testing.T) {
	in := Input{
		MCP: map[string]MCPState{
			"widgets": {Probed: true, Cost: Cost{Loaded: 100, Deferred: 20}},
		},
	}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if idx.Project.MCP.Loaded != 100 || idx.Project.MCP.Deferred != 20 {
		t.Fatalf("Project.MCP = %+v, want {100 20}", idx.Project.MCP)
	}
	if idx.Project.Items != 1 {
		t.Fatalf("Project.Items = %d, want 1", idx.Project.Items)
	}
	if idx.Project.Unmeasured != 0 {
		t.Fatalf("Project.Unmeasured = %d, want 0", idx.Project.Unmeasured)
	}

	b, ok := idx.ByMCP["widgets"]
	if !ok {
		t.Fatalf("ByMCP missing entry for widgets")
	}
	if b.MCP.Loaded != 100 || b.MCP.Deferred != 20 {
		t.Fatalf("ByMCP[widgets].MCP = %+v, want {100 20}", b.MCP)
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
	in := Input{}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if idx.Project.Total() != (Cost{}) {
		t.Fatalf("Project.Total() = %+v, want zero", idx.Project.Total())
	}
	if len(idx.ByMCP) != 0 {
		t.Fatalf("ByMCP = %+v, want empty", idx.ByMCP)
	}
	if idx.Project.Unmeasured != 0 {
		t.Fatalf("Project.Unmeasured = %d, want 0", idx.Project.Unmeasured)
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
