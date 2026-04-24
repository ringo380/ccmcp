package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:     "agent",
	Aliases: []string{"agents"},
	Short:   "Manage Claude Code subagents (list, new, move, rm, show)",
}

var agentScopeFilter string

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List subagents across user, project, and plugin scopes",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
		if err != nil {
			return err
		}
		all := agents.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)

		var rows []agents.Agent
		for _, a := range all {
			if agentScopeFilter != "" && agentScopeFilter != "all" && string(a.Scope) != agentScopeFilter {
				continue
			}
			rows = append(rows, a)
		}

		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		}
		if len(rows) == 0 {
			fmt.Println("(no agents match)")
			return nil
		}
		for _, a := range rows {
			src := string(a.Scope)
			if a.Scope == agents.ScopePlugin {
				pname, _ := config.ParsePluginID(a.PluginID)
				src = "plugin:" + pname
			}
			model := a.Model
			if model == "" {
				model = "-"
			}
			desc := assets.Truncate(a.Description, 60)
			fmt.Printf("  %-30s  %-20s  %-8s  %s\n", a.Name, src, model, desc)
		}
		counts := map[agents.Scope]int{}
		for _, a := range rows {
			counts[a.Scope]++
		}
		var parts []string
		for _, sc := range []agents.Scope{agents.ScopeUser, agents.ScopeProject, agents.ScopePlugin} {
			if counts[sc] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[sc], sc))
			}
		}
		fmt.Printf("\n%d agents shown (%s)\n", len(rows), strings.Join(parts, ", "))
		return nil
	},
}

var (
	agentNewScope       string
	agentNewDescription string
	agentNewModel       string
	agentMoveTo         string
	agentRmScope        string
	agentShowScope      string
)

var agentNewCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Scaffold a new agent at <scope>/agents/<name>.md",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		scope := agents.Scope(agentNewScope)
		if flagDryRun {
			root := p.ClaudeConfigDir
			if scope == agents.ScopeProject {
				root = proj + "/.claude"
			}
			fmt.Printf("[dry-run] would create %s/agents/%s.md\n", root, args[0])
			return nil
		}
		path, err := agents.Scaffold(args[0], agentNewDescription, agentNewModel, scope, p.ClaudeConfigDir, proj)
		if err != nil {
			return err
		}
		fmt.Printf("created %s\n", path)
		return nil
	},
}

var agentMoveCmd = &cobra.Command{
	Use:   "move <name>",
	Short: "Move an agent between user and project scope (--to user|project)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		to := agents.Scope(agentMoveTo)
		from := agents.ScopeUser
		if to == agents.ScopeUser {
			from = agents.ScopeProject
		}
		if flagDryRun {
			fmt.Printf("[dry-run] would move agent %s: %s -> %s\n", args[0], from, to)
			return nil
		}
		src, dst, err := agents.Move(args[0], from, to, p.ClaudeConfigDir, proj)
		if err != nil {
			return err
		}
		fmt.Printf("moved %s -> %s\n", src, dst)
		return nil
	},
}

var agentRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"remove", "delete"},
	Short:   "Delete a user- or project-scope agent (refuses plugin scope)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		scope := agents.Scope(agentRmScope)
		if flagDryRun {
			fmt.Printf("[dry-run] would delete %s-scope agent %s\n", scope, args[0])
			return nil
		}
		path, existed, err := agents.Remove(args[0], scope, p.ClaudeConfigDir, proj)
		if err != nil {
			return err
		}
		if !existed {
			fmt.Printf("agent %q not found at %s\n", args[0], path)
			return nil
		}
		fmt.Printf("deleted %s\n", path)
		return nil
	},
}

var agentShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Render an agent's frontmatter and file path",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
		if err != nil {
			return err
		}
		all := agents.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		var match *agents.Agent
		for i := range all {
			if all[i].Name == args[0] {
				if agentShowScope != "" && string(all[i].Scope) != agentShowScope {
					continue
				}
				match = &all[i]
				break
			}
		}
		if match == nil {
			return fmt.Errorf("agent %q not found", args[0])
		}
		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(match)
		}
		fmt.Printf("name:        %s\n", match.Name)
		fmt.Printf("scope:       %s\n", match.Scope)
		if match.PluginID != "" {
			fmt.Printf("plugin:      %s\n", match.PluginID)
		}
		fmt.Printf("model:       %s\n", match.Model)
		fmt.Printf("description: %s\n", match.Description)
		fmt.Printf("path:        %s\n", match.File)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(agentListCmd, agentNewCmd, agentMoveCmd, agentRmCmd, agentShowCmd)
	agentListCmd.Flags().StringVar(&agentScopeFilter, "scope", "", "filter by scope: user|project|plugin|all")
	agentNewCmd.Flags().StringVar(&agentNewScope, "scope", "user", "scope: user|project")
	agentNewCmd.Flags().StringVar(&agentNewDescription, "description", "", "frontmatter description")
	agentNewCmd.Flags().StringVar(&agentNewModel, "model", "sonnet", "model (sonnet, opus, haiku)")
	agentMoveCmd.Flags().StringVar(&agentMoveTo, "to", "", "destination scope: user|project (required)")
	_ = agentMoveCmd.MarkFlagRequired("to")
	agentRmCmd.Flags().StringVar(&agentRmScope, "scope", "user", "scope: user|project")
	agentShowCmd.Flags().StringVar(&agentShowScope, "scope", "", "filter by scope when names collide")
}
