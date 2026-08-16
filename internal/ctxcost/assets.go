package ctxcost

import (
	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/skills"
	"github.com/ringo380/ccmcp/internal/tokens"
)

// Input is the already-discovered asset state the estimator costs. Callers pass
// the results of skills.Discover / agents.Discover / commands.Discover rather
// than a directory, so ctxcost never re-walks the filesystem.
type Input struct {
	Skills   []skills.Skill
	Agents   []agents.Agent
	Commands []commands.Command

	// MCP is the per-server probe state, keyed by the server name as the MCPs
	// tab displays it. Build has no other source of truth for which servers
	// exist: a server absent from this map is invisible to the estimate, not
	// counted as unmeasured. To produce a complete estimate the caller MUST
	// supply one entry per configured server (Probed: false for any it
	// couldn't probe). See MCPState for the full absent-vs-unmeasured-vs-zero
	// contract.
	MCP map[string]MCPState

	// PluginEnabled reports whether a plugin id ("name@marketplace") is enabled.
	// Assets from a disabled plugin are still costed into ByPlugin (so the UI can
	// show what enabling it would add) but are excluded from the Project total,
	// because a disabled plugin injects nothing into the prompt.
	//
	// Necessary because skills.Discover/agents.Discover deliberately return assets
	// from registered-but-disabled plugins (see internal/skills/skills.go:48), and
	// their Enabled field reflects only skillOverrides, not plugin enablement.
	// A nil func means treat every plugin as enabled.
	PluginEnabled func(pluginID string) bool
}

// Index is the computed estimate from one project's perspective.
//
// ByPlugin is keyed by qualified plugin id ("name@marketplace"). ByMCP is keyed
// by the MCP server name as the MCPs tab shows it; it stays empty in Phase 1
// (no probing) and is populated in Phase 2.
type Index struct {
	ByPlugin map[string]Breakdown `json:"byPlugin"`
	ByMCP    map[string]Breakdown `json:"byMcp"`
	Project  Breakdown            `json:"project"`
}

// Listing-line shapes. Claude Code injects one line per asset into the system
// prompt, roughly "- <name>: <description>". The exact wording is not a
// published contract, so these are deliberately simple and the resulting
// figures are estimates.
//
// Known underestimate: the real agent listing also carries a "(Tools: ...)"
// suffix, which agents.Agent does not expose. Agent cost therefore reads
// slightly low.
func skillLine(s skills.Skill) string { return "- " + s.Name + ": " + s.Description + "\n" }
func agentLine(a agents.Agent) string { return "- " + a.Name + ": " + a.Description + "\n" }
func commandLine(c commands.Command) string {
	name := c.Effective
	if name == "" {
		name = c.Name
	}
	return "- " + name + ": " + c.Description + "\n"
}

// Build costs every enabled asset in `in`, attributing plugin-scope assets to
// their plugin. Project only folds in assets that actually load in the
// current prompt - see the PluginEnabled field on Input.
func Build(in Input) (*Index, error) {
	idx := &Index{
		ByPlugin: map[string]Breakdown{},
		ByMCP:    map[string]Breakdown{},
	}

	// add routes one costed item into the owning plugin (when there is one) and
	// into the project total, via `pick` which selects the kind's Cost field.
	// ByPlugin always accumulates, regardless of enablement, so the UI can show
	// what enabling a disabled plugin would add. The project total only counts
	// assets that actually load: user/project-scope assets (empty pluginID) and
	// assets owned by an enabled plugin.
	add := func(pluginID, line string, pick func(*Breakdown) *Cost) error {
		n, err := tokens.Count(line)
		if err != nil {
			return err
		}
		c := Cost{Loaded: n, Deferred: n} // deferral never applies to assets

		pluginEnabled := pluginID == "" || in.PluginEnabled == nil || in.PluginEnabled(pluginID)

		if pluginID != "" {
			b := idx.ByPlugin[pluginID]
			*pick(&b) = pick(&b).Add(c)
			b.Items++
			idx.ByPlugin[pluginID] = b
		}
		if pluginEnabled {
			*pick(&idx.Project) = pick(&idx.Project).Add(c)
			idx.Project.Items++
		}
		return nil
	}

	for _, s := range in.Skills {
		if !s.Enabled {
			continue
		}
		if err := add(s.PluginID, skillLine(s), func(b *Breakdown) *Cost { return &b.Skills }); err != nil {
			return nil, err
		}
	}
	for _, a := range in.Agents {
		if !a.Enabled {
			continue
		}
		if err := add(a.PluginID, agentLine(a), func(b *Breakdown) *Cost { return &b.Agents }); err != nil {
			return nil, err
		}
	}
	// commands.Command has no Enabled field - every discovered command loads.
	for _, c := range in.Commands {
		if err := add(c.PluginID, commandLine(c), func(b *Breakdown) *Cost { return &b.Commands }); err != nil {
			return nil, err
		}
	}

	// MCP servers fold through the same AddSource call path used for
	// per-server (ByMCP) and project accounting, so the two stay arithmetically
	// consistent by construction rather than via a parallel sum.
	for name, st := range in.MCP {
		src := Source{Cost: st.Cost, Tier: TierProbed, Reason: st.Reason}
		if !st.Probed {
			src.Tier = TierUnknown
		}

		b := idx.ByMCP[name]
		b.AddSource(src)
		idx.ByMCP[name] = b

		idx.Project.AddSource(src)
	}

	return idx, nil
}
