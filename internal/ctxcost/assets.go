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
// their plugin and folding everything into the project total.
func Build(in Input) (*Index, error) {
	idx := &Index{
		ByPlugin: map[string]Breakdown{},
		ByMCP:    map[string]Breakdown{},
	}

	// add routes one costed item into the owning plugin (when there is one) and
	// into the project total, via `pick` which selects the kind's Cost field.
	add := func(pluginID, line string, pick func(*Breakdown) *Cost) error {
		n, err := tokens.Count(line)
		if err != nil {
			return err
		}
		c := Cost{Loaded: n, Deferred: n} // deferral never applies to assets

		if pluginID != "" {
			b := idx.ByPlugin[pluginID]
			*pick(&b) = pick(&b).Add(c)
			b.Items++
			idx.ByPlugin[pluginID] = b
		}
		*pick(&idx.Project) = pick(&idx.Project).Add(c)
		idx.Project.Items++
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
	return idx, nil
}
