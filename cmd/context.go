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
		"the total also counts this project's own .claude/ skills, agents, and commands.",
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
			PluginEnabled: func(id string) bool {
				en, known := settings.PluginEnabled(id)
				return known && en
			},
		})
		if err != nil {
			return err
		}

		if flagJSON {
			payload := struct {
				Project  ctxcost.Breakdown            `json:"project"`
				ByPlugin map[string]ctxcost.Breakdown `json:"byPlugin"`
				Measured *ctxcost.Measured            `json:"measured,omitempty"`
			}{Project: idx.Project, ByPlugin: idx.ByPlugin}
			if mm, ok := ctxcost.Calibrate(p.ClaudeConfigDir, proj); ok {
				payload.Measured = &mm
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}

		total := idx.Project.Total()
		fmt.Printf("per-turn context   %s\n", ctxcost.HumanCost(total))
		fmt.Printf("  from %d enabled skills/agents/commands (global plugins plus this project's own)\n", idx.Project.Items)
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
			fmt.Printf("  %8s  %4d items  %s\n",
				ctxcost.Human(r.b.Total().Loaded), r.b.Items, r.id)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(contextCmd)
}
