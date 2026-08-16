package ctxcost

// MCPState is the per-server probe state, keyed by the server name as the
// MCPs tab displays it. A server absent from Input.MCP, or present with
// Probed=false, is TierUnknown: it contributes nothing to the total and is
// counted in Unmeasured.
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
