// Package ctxcost estimates how many tokens the currently enabled skills,
// agents, and commands add to every turn's prompt, attributed per source and
// scoped to one project's perspective.
//
// Phase 1 measures assets only. MCP tool schemas require a server probe and are
// reported as unmeasured ("-"), never as zero; the types here carry the MCP
// fields so Phase 2 can fill them in.
//
// Every figure is an ESTIMATE. Counts come from internal/tokens (cl100k_base,
// not Anthropic's tokenizer), and the exact wording Claude Code uses to inject
// a listing line is not a published contract. Present figures with "≈".
package ctxcost

// Tier records how a cost figure was derived, so the UI can distinguish
// "measured as zero" from "not measured".
type Tier int

const (
	// TierExact was read off disk - asset frontmatter, config strings.
	TierExact Tier = iota
	// TierProbed came from a completed MCP server probe (Phase 2).
	TierProbed
	// TierUnknown could not be measured. Render "-", never 0.
	TierUnknown
)

// Cost is one attributed per-turn contribution, in estimated tokens.
//
// Loaded is the cost when tool schemas are inlined in full. Deferred is the
// cost when Claude Code replaces MCP tool schemas with a bare name list behind
// a ToolSearch call, which it does above an internal, undocumented tool-count
// threshold. For assets (skills/agents/commands) the two are equal - deferral
// applies only to MCP tool schemas.
type Cost struct {
	Loaded   int `json:"loaded"`
	Deferred int `json:"deferred"`
}

// Add returns the sum of c and o.
func (c Cost) Add(o Cost) Cost {
	return Cost{Loaded: c.Loaded + o.Loaded, Deferred: c.Deferred + o.Deferred}
}

// Source is one measurable thing: a single MCP server. Only AddSource consumes
// it, and that folds solely into Breakdown.MCP - plugin asset contributions are
// accumulated directly into the per-kind fields instead.
type Source struct {
	Cost   Cost   `json:"cost"`
	Tier   Tier   `json:"tier"`
	Reason string `json:"reason,omitempty"` // why TierUnknown; empty otherwise
}

// Breakdown is the per-kind split for one owner - a plugin, an MCP server, or a
// whole project.
type Breakdown struct {
	Skills   Cost `json:"skills"`
	Agents   Cost `json:"agents"`
	Commands Cost `json:"commands"`
	MCP      Cost `json:"mcp"`

	// Items counts measured contributors. Unmeasured counts sources that exist
	// but could not be measured; they add nothing to Total and are reported
	// separately so the UI never implies a complete number.
	Items      int `json:"items"`
	Unmeasured int `json:"unmeasured"`
}

// Total sums every kind.
func (b Breakdown) Total() Cost {
	return b.Skills.Add(b.Agents).Add(b.Commands).Add(b.MCP)
}

// AddSource folds an MCP-kind source into b, routing an unmeasurable source to
// Unmeasured instead of adding a misleading zero.
func (b *Breakdown) AddSource(s Source) {
	if s.Tier == TierUnknown {
		b.Unmeasured++
		return
	}
	b.MCP = b.MCP.Add(s.Cost)
	b.Items++
}

// AddTo folds b into dst, summing every field.
func (b Breakdown) AddTo(dst *Breakdown) {
	dst.Skills = dst.Skills.Add(b.Skills)
	dst.Agents = dst.Agents.Add(b.Agents)
	dst.Commands = dst.Commands.Add(b.Commands)
	dst.MCP = dst.MCP.Add(b.MCP)
	dst.Items += b.Items
	dst.Unmeasured += b.Unmeasured
}
