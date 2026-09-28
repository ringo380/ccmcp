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
	"github.com/ringo380/ccmcp/internal/mcpscope"
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

// effectiveMCPs loads this machine's config and asks internal/mcpscope which MCP
// servers load in proj. The rules live there, in one place, shared with the MCPs
// tab: two surfaces reporting different unmeasured counts for the same project is
// what having two copies of them already produced.
func effectiveMCPs(p paths.Paths, proj string) ([]mcpscope.Server, error) {
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
	stash, err := config.LoadStash(p.Stash)
	if err != nil {
		return nil, err
	}
	return mcpscope.Effective(mcpscope.Inputs{
		CJ:      cj,
		Project: proj,
		Stash:   stash,
		// The ALL-installed scan, matching the MCPs tab: a disabled plugin's
		// server does not load, but it still accounts for its override key.
		PluginMCPs: config.ScanAllInstalledPluginMCPs(settings, installed, p.PluginsDir),
	}), nil
}

// probeOutcome is one server's line in the report.
type probeOutcome struct {
	Name      string `json:"name"`
	Key       string `json:"key,omitempty"`
	State     string `json:"state"` // probed | cached | failed | skipped
	Err       string `json:"err,omitempty"`
	Reason    string `json:"reason,omitempty"`    // why skipped
	NotCached bool   `json:"notCached,omitempty"` // failed under a caller-supplied --timeout
	Tools     int    `json:"tools,omitempty"`
	Loaded    int    `json:"loaded,omitempty"`
	Deferred  int    `json:"deferred,omitempty"`
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
		// Checked before anything is loaded or started: a non-positive timeout
		// makes every probe fail with a deadline error that reads as the
		// server's fault. Rejected rather than coerced - silently substituting
		// another value would hide a flag the user meant to pass.
		if probeTimeout <= 0 {
			return fmt.Errorf("--timeout must be positive, got %s", probeTimeout)
		}
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
			var kept []mcpscope.Server
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

		// A probe run under a timeout the CALLER chose is an experiment, not a
		// measurement of that server: `--timeout 1ms` fails everything. Caching
		// that would overwrite a good result and leave `ccmcp context` blaming
		// the server for the user's flag, recoverable only via --force. The
		// narrowest rule that fixes it: when --timeout is explicitly set, cache
		// only SUCCESSES. Narrower than matching on the error text (which cannot
		// tell a caller-induced deadline from a server that really is that slow)
		// and narrower than skipping the cache entirely (a successful probe is a
		// real measurement whatever bound it ran under).
		//
		// Keyed off the VALUE rather than cobra's Changed bit, which is sticky
		// for the life of the command object: a caller that runs the command
		// twice in one process (the test harness does) would otherwise carry the
		// first run's --timeout into the second. Passing exactly the default is
		// indistinguishable from not passing it, which is the right answer.
		explicitTimeout := probeTimeout != mcpprobe.DefaultTimeout

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
				probed := mcpprobe.Probe(ctx, t)
				cancel()
				if probed.OK || !explicitTimeout {
					cache.Put(probed)
					dirty = true
				} else {
					o.NotCached = true
				}
				res = probed
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
				note := ""
				if o.NotCached {
					note = "   (not cached: ran under an explicit --timeout)"
				}
				fmt.Printf("  %-24s failed   %s%s\n", o.Name, o.Err, note)
			default:
				fmt.Printf("  %-24s %-8s %s   %d tool(s)\n",
					o.Name, o.State, ctxcost.HumanCost(ctxcost.Cost{Loaded: o.Loaded, Deferred: o.Deferred}), o.Tools)
			}
		}
		fmt.Printf("\n%d probed, %d already cached, %d failed, %d skipped   (cache: %s)\n",
			counts["probed"], counts["cached"], counts["failed"], counts["skipped"], p.ProbeCache)
		notCached := 0
		for _, o := range outcomes {
			if o.NotCached {
				notCached++
			}
		}
		switch {
		case notCached > 0:
			fmt.Printf("%d failure(s) under an explicit --timeout were NOT cached - any earlier result still stands\n", notCached)
		case counts["failed"] > 0:
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
