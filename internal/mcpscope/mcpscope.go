// Package mcpscope answers one question in exactly one place: which MCP servers
// will Claude Code load in a given project, and what does each one cost per
// turn?
//
// It exists because the MCPs tab and the CLI (`ccmcp context`, `ccmcp mcp
// probe`) each had their own copy of the rules, and the copies drifted: a name
// that was both stashed and listed in enabledMcpServers was effective in one and
// suppressed in the other, so the two surfaces reported different unmeasured
// counts for the same config. The rules here are ported from the MCPs tab's
// rebuild()/isEffective pair, which is the set that has been reviewed against
// real config - not an average of the two.
package mcpscope

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/ctxcost"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
	"github.com/ringo380/ccmcp/internal/stringslice"
)

// Server is one MCP server that loads in a project.
//
// Reason, when set, means the server loads but cannot be probed locally - a
// claude.ai integration, or a built-in with no local config. Such a server still
// belongs in the accounting: it is real prompt cost that ccmcp cannot measure,
// and dropping it would silently shrink the unmeasured count instead of
// reporting a gap.
type Server struct {
	Name   string
	Source config.MCPSource
	Config any
	Reason string
}

// Inputs is the already-loaded config Effective reads. Callers pass loaded state
// rather than paths because the TUI holds an edited, not-yet-saved copy in
// memory and must get an answer about that copy, not about what is on disk.
//
// PluginMCPs must be the ALL-installed scan (config.ScanAllInstalledPluginMCPs),
// including plugins whose enabledPlugins value is false: a disabled plugin's
// server does not load, but it still accounts for its override key, which is
// what keeps a shadowed built-in from being counted twice.
type Inputs struct {
	CJ         *config.ClaudeJSON
	Project    string
	Stash      *config.Stash
	PluginMCPs map[string][]config.PluginMCPSource
}

// McpjsonExcluded reports whether ./.mcp.json's server `name` is kept out of a
// project by its allow/deny lists (enabledMcpjsonServers / disabledMcpjsonServers).
//
// An explicit deny always wins. A NON-EMPTY allow-list is exclusive: anything
// missing from it is excluded. An empty allow-list means "no allow-list", not
// "allow nothing" - which is the half of the rule an inverted condition flips,
// and it had no test coverage on either surface until this was extracted.
//
// Exported because the MCPs tab computes the same fact when it paints rows
// (mcpRow.McpjsonDeny) and cannot reuse Effective's Server list for it. This is
// the single definition; the tab calls it rather than keeping a second copy.
func McpjsonExcluded(name string, allow, deny map[string]bool) bool {
	return deny[name] || (len(allow) > 0 && !allow[name])
}

// Effective returns one entry per server that loads in in.Project, sorted by
// name then source. Two entries can share a Name when two sources register the
// same one - that is a real configuration, and CostStates is where it is handled.
//
// Excluded, because none of them load: stash entries, servers from globally
// disabled plugins, anything suppressed by this project's disabledMcpServers,
// .mcp.json servers excluded by the allow/deny lists, and orphan override keys
// with no source on disk.
func Effective(in Inputs) []Server {
	if in.CJ == nil {
		return nil
	}

	disabled := stringslice.Set(in.CJ.ProjectDisabledMcpServers(in.Project))
	enabledHere := stringslice.Set(in.CJ.ProjectEnabledMcpServers(in.Project))
	allow := stringslice.Set(in.CJ.ProjectMcpjsonEnabled(in.Project))
	deny := stringslice.Set(in.CJ.ProjectMcpjsonDisabled(in.Project))

	var out []Server
	// accounted holds every key some enumerable source explains, whether or not
	// that source loads. The built-in pass at the end consults it: a name is
	// only a built-in when nothing else on disk accounts for it. Stash entries
	// register their plain name here for exactly that reason.
	accounted := map[string]bool{}

	for name, cfg := range in.CJ.UserMCPs() {
		key := config.OverrideKey(config.SourceUser, name, "")
		accounted[key] = true
		if disabled[key] {
			continue
		}
		out = append(out, Server{Name: name, Source: config.SourceUser, Config: cfg})
	}
	for name, cfg := range in.CJ.ProjectMCPs(in.Project) {
		key := config.OverrideKey(config.SourceLocal, name, "")
		accounted[key] = true
		if disabled[key] {
			continue
		}
		out = append(out, Server{Name: name, Source: config.SourceLocal, Config: cfg})
	}
	if m, err := config.LoadMCPJson(in.Project + "/.mcp.json"); err == nil {
		for name, cfg := range m.Servers() {
			key := config.OverrideKey(config.SourceProject, name, "")
			accounted[key] = true
			if disabled[key] || McpjsonExcluded(name, allow, deny) {
				continue
			}
			out = append(out, Server{Name: name, Source: config.SourceProject, Config: cfg})
		}
	}
	for name, srcs := range in.PluginMCPs {
		for _, s := range srcs {
			pluginName, _ := config.ParsePluginID(s.PluginID)
			key := config.OverrideKey(config.SourcePlugin, name, pluginName)
			accounted[key] = true
			// A plugin-registered server loads only when the plugin itself is
			// globally enabled.
			if !s.Enabled || disabled[key] {
				continue
			}
			out = append(out, Server{Name: name, Source: config.SourcePlugin, Config: s.Config})
		}
	}
	for _, full := range in.CJ.ClaudeAiEverConnected() {
		if !strings.HasPrefix(full, "claude.ai ") {
			continue
		}
		accounted[full] = true
		if disabled[full] {
			continue
		}
		out = append(out, Server{
			Name:   strings.TrimPrefix(full, "claude.ai "),
			Source: config.SourceClaude,
			Reason: "claude.ai integration - cannot probe locally",
		})
	}
	if in.Stash != nil {
		// Stash entries never load, but they DO account for their plain name -
		// dropping this line is the divergence that made the CLI and the MCPs
		// tab disagree about a stashed name that also appears in
		// enabledMcpServers.
		for name := range in.Stash.Entries() {
			accounted[name] = true
		}
	}
	// A stale disabledMcpServers key is an orphan: it explains the name (so it
	// cannot resurface as a built-in) but nothing loads from it.
	for k := range disabled {
		accounted[k] = true
	}
	for name := range enabledHere {
		if accounted[name] {
			continue
		}
		out = append(out, Server{
			Name:   name,
			Source: config.SourceBuiltin,
			Reason: "built-in with no local config - cannot probe locally",
		})
	}

	sort.Slice(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if li != lj {
			return li < lj
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// CostStates converts the effective server list plus the probe cache into the
// per-server state ctxcost.Build consumes. notProbedReason is the wording for a
// plain cache miss, which differs per surface (the TUI can say "press p").
//
// It emits an entry for EVERY server, not just the cache hits. ctxcost.Build has
// no independent view of which servers exist: a server absent from Input.MCP
// contributes nothing to the total AND nothing to Unmeasured, so populating only
// cache hits would print a confident headline that silently omits every unprobed
// server.
//
// Input.MCP is keyed by display name, so two effective servers sharing a name
// cannot both be represented. Such a name is published as unmeasured rather than
// as the first one's measurement: keeping one figure would read as a COMPLETE
// number while a second real server vanished from the total. The per-name count
// is therefore taken in a first pass, before anything is published - computing
// it inline would publish the first colliding server as measured.
func CostStates(servers []Server, cache *mcpprobe.Cache, notProbedReason string) map[string]ctxcost.MCPState {
	states := make(map[string]ctxcost.MCPState, len(servers))

	byName := map[string]int{}
	for _, s := range servers {
		byName[s.Name]++
	}

	for _, s := range servers {
		if _, done := states[s.Name]; done {
			continue
		}
		if byName[s.Name] > 1 {
			states[s.Name] = ctxcost.MCPState{
				Probed: false,
				Reason: fmt.Sprintf("duplicate server name (%d sources) - cannot attribute cost", byName[s.Name]),
			}
			continue
		}
		if s.Reason != "" {
			states[s.Name] = ctxcost.MCPState{Probed: false, Reason: s.Reason}
			continue
		}
		t, ok, reason := mcpprobe.TargetFromConfig(s.Name, s.Config)
		if !ok {
			states[s.Name] = ctxcost.MCPState{Probed: false, Reason: reason}
			continue
		}
		res, hit := cache.Get(t.Key())
		if !hit {
			states[s.Name] = ctxcost.MCPState{Probed: false, Reason: notProbedReason}
			continue
		}
		// MCPStateFrom is the single conversion point, and the reason it
		// exists: a failed probe's Loaded()/Deferred() are arithmetically 0,
		// and rendering that as a measured "≈0" is the exact defect this
		// feature is meant to prevent.
		states[s.Name] = ctxcost.MCPStateFrom(res.OK, res.Err, res.Loaded(), res.Deferred())
	}
	return states
}
