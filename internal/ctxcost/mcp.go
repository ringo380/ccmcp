package ctxcost

// MCPState is the per-server probe state, keyed by the server name as the
// MCPs tab displays it, in Input.MCP.
//
// Build has no independent knowledge of which servers are configured - it
// only ever sees the entries the caller supplies. So the completeness
// obligation is the caller's: to get a complete estimate, the caller MUST
// supply one entry per configured server, using Probed: false (with a Reason)
// for any server it has not probed or whose probe failed. A key present with
// Probed=false is TierUnknown - it contributes nothing to the total and IS
// counted in Unmeasured. A server whose key is simply ABSENT from the map is
// invisible to Build entirely: it contributes nothing to the total AND
// nothing to Unmeasured, so a caller that only populates cache hits will
// silently under-report rather than flag the gap.
type MCPState struct {
	Probed bool
	Reason string // why unmeasured, when !Probed
	Cost   Cost
}

// MCPStateFrom converts probe primitives into an MCPState. It takes
// primitives rather than an internal/mcpprobe.Result so ctxcost does not
// import mcpprobe - the caller (TUI, CLI) does the conversion.
//
// A failed probe (ok == false) always yields Probed: false with the cost
// discarded, even if loaded/deferred happen to be nonzero - a Result that
// failed round-trips its Loaded()/Deferred() as 0, but nothing here should
// depend on that being true. Presenting a failed probe's cost, zero or not,
// as a measurement is exactly the bug this type exists to prevent.
func MCPStateFrom(ok bool, errText string, loaded, deferred int) MCPState {
	if !ok {
		reason := errText
		if reason == "" {
			reason = "probe failed"
		}
		return MCPState{Probed: false, Reason: reason}
	}
	return MCPState{Probed: true, Cost: Cost{Loaded: loaded, Deferred: deferred}}
}
