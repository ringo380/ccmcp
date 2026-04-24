package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/skills"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show MCP + plugin state across all scopes",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, err := projectPath()
		if err != nil {
			return err
		}

		cj, err := config.LoadClaudeJSON(p.ClaudeJSON)
		if err != nil {
			return err
		}
		stash, err := config.LoadStash(p.Stash)
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
		pluginMCPs := config.ScanEnabledPluginMCPs(settings, installed, p.PluginsDir)
		discoveredSkills := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		discoveredAgents := agents.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		discoveredCmds := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		conflicts := commands.FindConflicts(discoveredCmds, discoveredSkills)

		// mcp.json servers (project-shared)
		var mcpjsonNames []string
		if mcpj, err := config.LoadMCPJson(proj + "/.mcp.json"); err == nil && mcpj != nil {
			mcpjsonNames = mcpj.Names()
		}

		pluginSourced := config.SortedPluginSources(pluginMCPs)
		overrides := cj.ProjectDisabledMcpServers(proj)
		claudeai := cj.ClaudeAiEverConnected()

		skillEnabled := 0
		for _, s := range discoveredSkills {
			if s.Enabled {
				skillEnabled++
			}
		}
		data := struct {
			ProjectPath    string   `json:"projectPath"`
			UserMCPs       []string `json:"userMcps"`
			LocalMCPs      []string `json:"localMcps"`
			McpjsonShared  []string `json:"mcpjsonShared"`
			McpjsonEnabled []string `json:"mcpjsonEnabled"`
			McpjsonDenied  []string `json:"mcpjsonDenied"`
			PluginSourced  []string `json:"pluginSourced"`
			ClaudeAi       []string `json:"claudeAi"`
			Overrides      []string `json:"disabledHere"`
			Stashed        []string `json:"stashed"`
			PluginsActive  int      `json:"pluginsActive"`
			PluginsTotal   int      `json:"pluginsTotal"`
			Installed      int      `json:"pluginsInstalled"`
			SkillsTotal    int      `json:"skillsTotal"`
			SkillsEnabled  int      `json:"skillsEnabled"`
			AgentsTotal    int      `json:"agentsTotal"`
			CommandsTotal  int      `json:"commandsTotal"`
			Conflicts      []commands.Conflict `json:"commandConflicts"`
		}{
			ProjectPath:    proj,
			UserMCPs:       cj.UserMCPNames(),
			LocalMCPs:      cj.ProjectMCPNames(proj),
			McpjsonShared:  mcpjsonNames,
			McpjsonEnabled: cj.ProjectMcpjsonEnabled(proj),
			McpjsonDenied:  cj.ProjectMcpjsonDisabled(proj),
			PluginSourced:  pluginSourced,
			ClaudeAi:       claudeai,
			Overrides:      overrides,
			Stashed:        stash.Names(),
			Installed:      len(installed.List()),
			SkillsTotal:    len(discoveredSkills),
			SkillsEnabled:  skillEnabled,
			AgentsTotal:    len(discoveredAgents),
			CommandsTotal:  len(discoveredCmds),
			Conflicts:      conflicts,
		}
		for _, e := range settings.PluginEntries() {
			data.PluginsTotal++
			if e.Enabled {
				data.PluginsActive++
			}
		}

		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(data)
		}

		fmt.Println("Project:")
		fmt.Println("  " + data.ProjectPath)
		fmt.Println()
		section("User MCPs (~/.claude.json — loads in every project)", data.UserMCPs)
		section("Local MCPs (this directory only)", data.LocalMCPs)
		if len(data.PluginSourced) > 0 {
			fmt.Println("MCPs registered by enabled plugins (auto-load via the Plugins tab):")
			for _, n := range data.PluginSourced {
				srcs := pluginMCPs[n]
				ids := make([]string, 0, len(srcs))
				for _, s := range srcs {
					pname, _ := config.ParsePluginID(s.PluginID)
					ids = append(ids, pname)
				}
				fmt.Printf("  - %s  (via plugin: %s)\n", n, strings.Join(ids, ", "))
			}
			fmt.Println()
		}
		if len(mcpjsonNames) > 0 || len(data.McpjsonEnabled) > 0 || len(data.McpjsonDenied) > 0 {
			fmt.Println(".mcp.json (shared, git-tracked):")
			if len(mcpjsonNames) > 0 {
				for _, n := range mcpjsonNames {
					fmt.Println("  - " + n)
				}
			} else {
				fmt.Println("  (none)")
			}
			if len(data.McpjsonEnabled) > 0 {
				fmt.Println("  allow-list:", data.McpjsonEnabled)
			}
			if len(data.McpjsonDenied) > 0 {
				fmt.Println("  deny-list: ", data.McpjsonDenied)
			}
			fmt.Println()
		}
		if len(data.ClaudeAi) > 0 {
			fmt.Printf("Claude.ai integrations (%d known; may be toggled per-project via /mcp):\n", len(data.ClaudeAi))
			for _, n := range data.ClaudeAi {
				fmt.Println("  - " + n)
			}
			fmt.Println()
		}
		if len(data.Overrides) > 0 {
			fmt.Printf("Per-project overrides for this directory (disabledMcpServers, %d):\n", len(data.Overrides))
			for _, k := range data.Overrides {
				fmt.Println("  ~ " + k)
			}
			fmt.Println()
		}
		section("Stashed MCPs (parked, not active)", data.Stashed)
		fmt.Printf("Plugins: %d enabled / %d known (%d installed)\n", data.PluginsActive, data.PluginsTotal, data.Installed)
		fmt.Printf("Skills:  %d/%d enabled  |  Agents: %d  |  Commands: %d\n", data.SkillsEnabled, data.SkillsTotal, data.AgentsTotal, data.CommandsTotal)
		if len(data.Conflicts) > 0 {
			fmt.Printf("\n⚠  %d slash-command conflict(s) — run `ccmcp command conflicts` for details\n", len(data.Conflicts))
			for i, c := range data.Conflicts {
				if i >= 3 {
					fmt.Printf("   … and %d more\n", len(data.Conflicts)-3)
					break
				}
				fmt.Printf("   - %s /%s\n", c.Kind, c.Effective)
			}
		}
		return nil
	},
}

func section(title string, items []string) {
	fmt.Println(title + ":")
	if len(items) == 0 {
		fmt.Println("  (none)")
		fmt.Println()
		return
	}
	for _, n := range items {
		fmt.Println("  - " + n)
	}
	fmt.Println()
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
