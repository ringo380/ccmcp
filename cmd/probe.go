package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/ctxcost"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/ringo380/ccmcp/internal/stringslice"
	"github.com/spf13/cobra"
)

var (
	probeTimeout time.Duration
	probeForce   bool
)

// reasonNotProbedYet is the unmeasured reason for a server that simply has no
// cache entry yet. The TUI has its own wording ("press p"); this one names the
// command instead.
const reasonNotProbedYet = "not probed yet - run `ccmcp mcp probe`"

// effectiveMCP is one MCP server that Claude Code will load in a project.
//
// Reason, when set, means the server loads but cannot be probed locally (a
// claude.ai integration, a built-in with no local config). Such a server still
// belongs in the accounting: it is real prompt cost that ccmcp cannot measure,
// and dropping it would silently shrink the unmeasured count.
type effectiveMCP struct {
	Name   string
	Source config.MCPSource
	Config any
	Reason string
}

// effectiveMCPs returns one entry per MCP server that loads in proj - the CLI's
// counterpart to the MCPs tab's rebuild() + isEffective() pair
// (internal/tui/mcps.go). The rules are deliberately identical, so `ccmcp
// context` and the TUI can never disagree about the same project:
//
//   - user and local scope load unless disabled here
//   - ./.mcp.json loads unless denied by the allow/deny lists or disabled here
//   - plugin-registered servers load only when the owning plugin is enabled
//   - claude.ai integrations load (unprobeable)
//   - a name in enabledMcpServers with no enumerable source is a built-in that
//     loads only because it is listed there (unprobeable)
//
// Stash entries and orphan disabledMcpServers keys are excluded: neither loads.
func effectiveMCPs(p paths.Paths, proj string) ([]effectiveMCP, error) {
	cj, err := config.LoadClaudeJSON(p.ClaudeJSON)
	if err != nil {
		return nil, err
	}
	settings, err := config.LoadSettings(p.SettingsJSON)
	if err != nil {
		return nil, err
	}
	installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
	if err != nil {
		return nil, err
	}

	disabled := stringslice.Set(cj.ProjectDisabledMcpServers(proj))
	enabledHere := stringslice.Set(cj.ProjectEnabledMcpServers(proj))
	allow := stringslice.Set(cj.ProjectMcpjsonEnabled(proj))
	deny := stringslice.Set(cj.ProjectMcpjsonDisabled(proj))

	var out []effectiveMCP
	// seen tracks every override key an enumerable source accounts for, so the
	// built-in pass below cannot emit a second row for a name that already has
	// a real source.
	seen := map[string]bool{}

	for name, cfg := range cj.UserMCPs() {
		key := config.OverrideKey(config.SourceUser, name, "")
		seen[key] = true
		if disabled[key] {
			continue
		}
		out = append(out, effectiveMCP{Name: name, Source: config.SourceUser, Config: cfg})
	}
	for name, cfg := range cj.ProjectMCPs(proj) {
		key := config.OverrideKey(config.SourceLocal, name, "")
		seen[key] = true
		if disabled[key] {
			continue
		}
		out = append(out, effectiveMCP{Name: name, Source: config.SourceLocal, Config: cfg})
	}
	if m, err := config.LoadMCPJson(proj + "/.mcp.json"); err == nil {
		for name, cfg := range m.Servers() {
			key := config.OverrideKey(config.SourceProject, name, "")
			seen[key] = true
			if disabled[key] || deny[name] || (len(allow) > 0 && !allow[name]) {
				continue
			}
			out = append(out, effectiveMCP{Name: name, Source: config.SourceProject, Config: cfg})
		}
	}
	// ScanEnabledPluginMCPs, not ScanAllInstalledPluginMCPs: a server from a
	// globally disabled plugin does not load, so it is not effective here.
	for name, srcs := range config.ScanEnabledPluginMCPs(settings, installed, p.PluginsDir) {
		for _, s := range srcs {
			pluginName, _ := config.ParsePluginID(s.PluginID)
			key := config.OverrideKey(config.SourcePlugin, name, pluginName)
			seen[key] = true
			if disabled[key] {
				continue
			}
			out = append(out, effectiveMCP{Name: name, Source: config.SourcePlugin, Config: s.Config})
		}
	}
	for _, full := range cj.ClaudeAiEverConnected() {
		if !strings.HasPrefix(full, "claude.ai ") {
			continue
		}
		seen[full] = true
		if disabled[full] {
			continue
		}
		out = append(out, effectiveMCP{
			Name:   strings.TrimPrefix(full, "claude.ai "),
			Source: config.SourceClaude,
			Reason: "claude.ai integration - cannot probe locally",
		})
	}
	for name := range enabledHere {
		if seen[name] || disabled[name] {
			continue
		}
		out = append(out, effectiveMCP{
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
	return out, nil
}

// mcpCostStates converts the effective server list plus the probe cache into the
// per-server state ctxcost.Build consumes.
//
// It emits an entry for EVERY effective server, not just the cache hits.
// ctxcost.Build has no independent view of which servers exist: a server absent
// from Input.MCP contributes nothing to the total AND nothing to Unmeasured, so
// populating only cache hits would print a confident headline that silently
// omits every unprobed server.
//
// Input.MCP is keyed by display name, so two effective servers sharing a name
// cannot both be represented. Such a name is published as unmeasured rather than
// as the first one's measurement - keeping one figure would read as a COMPLETE
// number while a second real server vanished from the total. The per-name count
// is therefore taken in a first pass, before anything is published: computing it
// inline would publish the first colliding server as measured.
func mcpCostStates(servers []effectiveMCP, cache *mcpprobe.Cache) map[string]ctxcost.MCPState {
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
			states[s.Name] = ctxcost.MCPState{Probed: false, Reason: reasonNotProbedYet}
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

// probeOutcome is one server's line in the report.
type probeOutcome struct {
	Name     string `json:"name"`
	Key      string `json:"key,omitempty"`
	State    string `json:"state"` // probed | cached | failed | skipped
	Err      string `json:"err,omitempty"`
	Reason   string `json:"reason,omitempty"` // why skipped
	Tools    int    `json:"tools,omitempty"`
	Loaded   int    `json:"loaded,omitempty"`
	Deferred int    `json:"deferred,omitempty"`
}

var mcpProbeCmd = &cobra.Command{
	Use:   "probe [names...]",
	Short: "Probe stdio MCP servers and cache their per-turn context cost",
	Long: "Start each stdio MCP server, ask it for its instructions and tool list, and\n" +
		"cache the estimated tokens they add to every turn.\n\n" +
		"With no names, every server that loads in this project is probed. A server\n" +
		"that cannot be spoken to is reported and cached as a failure rather than\n" +
		"failing the command - a cached failure is what keeps a broken or hanging\n" +
		"server from costing the user the same wait on every later run. Pass --force\n" +
		"to re-probe servers that are already cached, including cached failures.\n\n" +
		"Remote servers (claude.ai integrations, url-based entries) cannot be probed\n" +
		"locally and are reported as skipped; `ccmcp context` counts them as\n" +
		"unmeasured. Every figure is an estimate.",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, err := projectPath()
		if err != nil {
			return err
		}
		servers, err := effectiveMCPs(p, proj)
		if err != nil {
			return err
		}

		if len(args) > 0 {
			want := stringslice.Set(args)
			var kept []effectiveMCP
			for _, s := range servers {
				if want[s.Name] {
					kept = append(kept, s)
					delete(want, s.Name)
				}
			}
			if len(want) > 0 {
				missing := make([]string, 0, len(want))
				for n := range want {
					missing = append(missing, n)
				}
				sort.Strings(missing)
				return fmt.Errorf("no MCP server named %s loads in %s", strings.Join(missing, ", "), proj)
			}
			servers = kept
		}
		if len(servers) == 0 {
			fmt.Println("no MCP servers load in this project - nothing to probe")
			return nil
		}

		cache, err := mcpprobe.LoadCache(p.ProbeCache)
		if err != nil {
			return err
		}

		if flagDryRun {
			for _, s := range servers {
				if s.Reason != "" {
					fmt.Printf("[dry-run] would skip %s: %s\n", s.Name, s.Reason)
					continue
				}
				fmt.Printf("[dry-run] would probe %s\n", s.Name)
			}
			return nil
		}

		outcomes := make([]probeOutcome, 0, len(servers))
		dirty := false
		for _, s := range servers {
			o := probeOutcome{Name: s.Name}
			if s.Reason != "" {
				o.State = "skipped"
				o.Reason = s.Reason
				outcomes = append(outcomes, o)
				continue
			}
			t, ok, reason := mcpprobe.TargetFromConfig(s.Name, s.Config)
			if !ok {
				o.State = "skipped"
				o.Reason = reason
				outcomes = append(outcomes, o)
				continue
			}
			o.Key = t.Key()

			res, hit := cache.Get(t.Key())
			cached := hit && !probeForce
			if !cached {
				ctx, cancel := context.WithTimeout(cmd.Context(), probeTimeout)
				res = mcpprobe.Probe(ctx, t)
				cancel()
				cache.Put(res)
				dirty = true
			}

			switch {
			case !res.OK:
				o.State = "failed"
				o.Err = res.Err
			case cached:
				o.State = "cached"
			default:
				o.State = "probed"
			}
			if res.OK {
				o.Tools = len(res.Tools)
				o.Loaded = res.Loaded()
				o.Deferred = res.Deferred()
			}
			outcomes = append(outcomes, o)
		}

		if dirty {
			if err := cache.Save(); err != nil {
				return err
			}
		}

		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(struct {
				ProjectPath string         `json:"projectPath"`
				CachePath   string         `json:"cachePath"`
				Servers     []probeOutcome `json:"servers"`
			}{ProjectPath: proj, CachePath: p.ProbeCache, Servers: outcomes})
		}

		counts := map[string]int{}
		for _, o := range outcomes {
			counts[o.State]++
		}
		for _, o := range outcomes {
			switch o.State {
			case "skipped":
				fmt.Printf("  %-24s skipped  %s\n", o.Name, o.Reason)
			case "failed":
				fmt.Printf("  %-24s failed   %s\n", o.Name, o.Err)
			default:
				fmt.Printf("  %-24s %-8s %s   %d tool(s)\n",
					o.Name, o.State, ctxcost.HumanCost(ctxcost.Cost{Loaded: o.Loaded, Deferred: o.Deferred}), o.Tools)
			}
		}
		fmt.Printf("\n%d probed, %d already cached, %d failed, %d skipped   (cache: %s)\n",
			counts["probed"], counts["cached"], counts["failed"], counts["skipped"], p.ProbeCache)
		if counts["failed"] > 0 {
			fmt.Println("failures are cached too - re-run with --force to try them again")
		}
		fmt.Println("figures are estimates; run `ccmcp context` to see them alongside asset cost")
		return nil
	},
}

func init() {
	mcpCmd.AddCommand(mcpProbeCmd)
	mcpProbeCmd.Flags().DurationVar(&probeTimeout, "timeout", mcpprobe.DefaultTimeout,
		"per-server timeout for the whole probe (spawn, initialize, tools/list)")
	mcpProbeCmd.Flags().BoolVar(&probeForce, "force", false,
		"re-probe servers that are already cached, including cached failures")
}
