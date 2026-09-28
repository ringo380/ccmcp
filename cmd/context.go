package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/ctxcost"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
	"github.com/ringo380/ccmcp/internal/mcpscope"
	"github.com/ringo380/ccmcp/internal/skills"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Estimate the per-turn context cost of what's enabled here",
	Long: "Estimate how many tokens the enabled plugins and MCP servers add to every turn.\n\n" +
		"Figures are estimates: they use OpenAI's cl100k_base encoder (Anthropic does not\n" +
		"publish its own), and MCP tool schemas are not counted until a server is probed.\n" +
		"Plugin enablement is global, so plugin asset cost is the same everywhere - but\n" +
		"the total also counts this project's own .claude/ skills, agents, and commands.\n\n" +
		"This command READS the probe cache and never probes anything itself: it starts no\n" +
		"MCP server. Run `ccmcp mcp probe` to fill the cache; until then each server's tool\n" +
		"schemas are reported as unmeasured (\"-\"), never as zero.",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, err := projectPath()
		if err != nil {
			return err
		}
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
		if err != nil {
			return err
		}

		// One entry per server that loads here, cache hit or not - see
		// mcpCostStates for why a cache-hits-only map under-reports silently.
		// This command never probes: a miss stays unmeasured.
		servers, err := effectiveMCPs(p, proj)
		if err != nil {
			return err
		}
		probeCache, err := mcpprobe.LoadCache(p.ProbeCache)
		if err != nil {
			return err
		}
		mcpStates := mcpscope.CostStates(servers, probeCache, reasonNotProbedYet)

		// PluginEnabled is REQUIRED, not optional. skills.Discover and
		// agents.Discover deliberately return assets from registered-but-DISABLED
		// plugins (internal/skills/skills.go:48), and their Enabled field reflects
		// only skillOverrides. Omitting this counted all 183 registered plugins
		// instead of the 45 enabled ones and overstated the total by 283%
		// (46,245 vs 12,088 tokens) - caught in Task 7 before it shipped.
		idx, err := ctxcost.Build(ctxcost.Input{
			Skills:   skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir),
			Agents:   agents.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir),
			Commands: commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir),
			MCP:      mcpStates,
			PluginEnabled: func(id string) bool {
				en, known := settings.PluginEnabled(id)
				return known && en
			},
		})
		if err != nil {
			return err
		}

		// ctxcost.Build deliberately keeps disabled plugins in ByPlugin (so a UI can
		// show what enabling one would add) while excluding them from Project.
		// Unmarked, the rows below do not sum to the printed total.
		enabledPlugin := func(id string) bool {
			en, known := settings.PluginEnabled(id)
			return known && en
		}

		// Breakdown.Items counts measured ASSETS plus measured MCP SERVERS, and
		// AddSource increments it once per probed server - so anything reporting
		// an asset count has to subtract them or it grows by one per probed
		// server and starts miscounting skills/agents/commands. Computed once,
		// above both output paths, so the JSON and the printed line cannot drift.
		probedServers := 0
		for _, st := range mcpStates {
			if st.Probed {
				probedServers++
			}
		}
		assetItems := idx.Project.Items - probedServers

		if flagJSON {
			type pluginEntry struct {
				ctxcost.Breakdown
				Enabled bool `json:"enabled"`
			}
			byPlugin := make(map[string]pluginEntry, len(idx.ByPlugin))
			for id, b := range idx.ByPlugin {
				byPlugin[id] = pluginEntry{Breakdown: b, Enabled: enabledPlugin(id)}
			}
			// assetItems/probedServers are stated explicitly because
			// project.items is the raw ctxcost figure - assets PLUS measured
			// servers. Both are exposed rather than redefining the existing
			// field, whose meaning is documented on ctxcost.Breakdown.
			payload := struct {
				Project       ctxcost.Breakdown            `json:"project"`
				AssetItems    int                          `json:"assetItems"`
				ProbedServers int                          `json:"probedServers"`
				ByPlugin      map[string]pluginEntry       `json:"byPlugin"`
				ByMCP         map[string]ctxcost.Breakdown `json:"byMcp"`
				MCP           map[string]mcpEntry          `json:"mcp"`
				Measured      *ctxcost.Measured            `json:"measured,omitempty"`
			}{
				Project: idx.Project, AssetItems: assetItems, ProbedServers: probedServers,
				ByPlugin: byPlugin, ByMCP: idx.ByMCP, MCP: mcpJSON(mcpStates),
			}
			if mm, ok := ctxcost.Calibrate(p.ClaudeConfigDir, proj); ok {
				payload.Measured = &mm
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}

		total := idx.Project.Total()
		fmt.Printf("per-turn context   %s\n", ctxcost.HumanCost(total))
		fmt.Printf("  from %d enabled skills/agents/commands (global plugins plus this project's own)\n", assetItems)
		if len(mcpStates) > 0 {
			unmeasured := idx.Project.Unmeasured
			switch {
			case probedServers == 0:
				fmt.Printf("  MCP tool schemas   %s   (%d server(s) unmeasured - run `ccmcp mcp probe`)\n",
					ctxcost.Human(ctxcost.Unmeasured), unmeasured)
			case unmeasured > 0:
				fmt.Printf("  MCP tool schemas   %s   (from %d probed server(s); %d unmeasured - run `ccmcp mcp probe`)\n",
					ctxcost.HumanCost(idx.Project.MCP), probedServers, unmeasured)
			default:
				fmt.Printf("  MCP tool schemas   %s   (from %d probed server(s))\n",
					ctxcost.HumanCost(idx.Project.MCP), probedServers)
			}
		}
		if mm, ok := ctxcost.Calibrate(p.ClaudeConfigDir, proj); ok {
			fmt.Printf("  session-start prompt prefix %s (session %s)\n",
				ctxcost.Human(mm.PrefixTokens), mm.SessionID)
			fmt.Printf("  that prefix also covers CLAUDE.md, memory, and MCP tool schemas,\n")
			fmt.Printf("  so it is an upper bound rather than a like-for-like comparison\n")
		}
		fmt.Println()

		type row struct {
			id string
			b  ctxcost.Breakdown
		}
		rows := make([]row, 0, len(idx.ByPlugin))
		for id, b := range idx.ByPlugin {
			rows = append(rows, row{id, b})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].b.Total().Loaded != rows[j].b.Total().Loaded {
				return rows[i].b.Total().Loaded > rows[j].b.Total().Loaded
			}
			return rows[i].id < rows[j].id
		})

		fmt.Println("heaviest plugins:")
		for _, r := range rows {
			suffix := ""
			if !enabledPlugin(r.id) {
				// Not counted in the total above - shown because it says what
				// enabling the plugin would add.
				suffix = "  (disabled)"
			}
			fmt.Printf("  %8s  %4d items  %s%s\n",
				ctxcost.Human(r.b.Total().Loaded), r.b.Items, r.id, suffix)
		}
		return nil
	},
}

// mcpEntry is one server's probe state in the JSON output. Probed is reported
// explicitly so a consumer can tell a measured zero from an unmeasured server -
// the loaded/deferred pair alone cannot say which it is.
type mcpEntry struct {
	Probed   bool   `json:"probed"`
	Reason   string `json:"reason,omitempty"`
	Loaded   int    `json:"loaded"`
	Deferred int    `json:"deferred"`
}

func mcpJSON(states map[string]ctxcost.MCPState) map[string]mcpEntry {
	out := make(map[string]mcpEntry, len(states))
	for name, st := range states {
		out[name] = mcpEntry{
			Probed:   st.Probed,
			Reason:   st.Reason,
			Loaded:   st.Cost.Loaded,
			Deferred: st.Cost.Deferred,
		}
	}
	return out
}

func init() {
	rootCmd.AddCommand(contextCmd)
}
