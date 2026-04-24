package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/skills"
	"github.com/spf13/cobra"
)

var commandCmd = &cobra.Command{
	Use:     "command",
	Aliases: []string{"commands", "cmd"},
	Short:   "Inspect Claude Code slash commands (list; conflicts in phase 2)",
}

var commandScopeFilter string

var commandListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered slash commands",
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
		all := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)

		var rows []commands.Command
		for _, c := range all {
			if commandScopeFilter != "" && commandScopeFilter != "all" && string(c.Scope) != commandScopeFilter {
				continue
			}
			rows = append(rows, c)
		}

		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		}
		if len(rows) == 0 {
			fmt.Println("(no commands match)")
			return nil
		}
		for _, c := range rows {
			src := string(c.Scope)
			if c.Scope == commands.ScopePlugin {
				pname, _ := config.ParsePluginID(c.PluginID)
				src = "plugin:" + pname
			}
			desc := assets.Truncate(c.Description, 60)
			fmt.Printf("  /%-40s  %-25s  %s\n", c.Effective, src, desc)
		}
		counts := map[commands.Scope]int{}
		for _, c := range rows {
			counts[c.Scope]++
		}
		var parts []string
		for _, sc := range []commands.Scope{commands.ScopeUser, commands.ScopeProject, commands.ScopePlugin} {
			if counts[sc] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[sc], sc))
			}
		}
		fmt.Printf("\n%d commands shown (%s)\n", len(rows), strings.Join(parts, ", "))
		return nil
	},
}

var (
	conflictsIncludeIgnored bool
	resolveStrategy         string
)

var commandResolveCmd = &cobra.Command{
	Use:   "resolve <effective-name>",
	Short: "Resolve a detected command conflict by picking a strategy",
	Long: `Strategies (--strategy):
  disable-skill    write skillOverrides[<name>]="off" (for skill-vs-command)
  ignore           add <effective-name> to ~/.claude-ccmcp-ignores.json so it stops showing in reports
  list             list strategies applicable to this conflict (default if --strategy is unset)`,
	Args: cobra.ExactArgs(1),
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
		cmds := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		skls := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		allConflicts := commands.FindConflicts(cmds, skls)

		var match *commands.Conflict
		for i := range allConflicts {
			if allConflicts[i].Effective == args[0] {
				match = &allConflicts[i]
				break
			}
		}
		if match == nil {
			return fmt.Errorf("no detected conflict with effective name %q", args[0])
		}

		switch resolveStrategy {
		case "", "list":
			fmt.Printf("Conflict: %s  /%s\n", match.Kind, match.Effective)
			fmt.Println("Participants:")
			for _, pt := range match.Participants {
				label := pt.Scope
				if pt.PluginID != "" {
					pname, _ := config.ParsePluginID(pt.PluginID)
					label = pt.Scope + ":" + pname
				}
				fmt.Printf("  - %s (%s)  %s\n", pt.Kind, label, pt.File)
			}
			fmt.Println("\nAvailable strategies:")
			if match.Kind == commands.SkillVsCommand {
				fmt.Println("  disable-skill  — write skillOverrides[" + match.Effective + `]="off"`)
			}
			fmt.Println("  ignore         — record in ~/.claude-ccmcp-ignores.json (stops reporting)")
			return nil
		case "disable-skill":
			if match.Kind != commands.SkillVsCommand {
				return fmt.Errorf("disable-skill only applies to skill-vs-command conflicts; got %s", match.Kind)
			}
			if flagDryRun {
				fmt.Printf("[dry-run] would set skillOverrides[%q]=\"off\"\n", match.Effective)
				return nil
			}
			settings.SetSkillOverride(match.Effective, "off")
			if err := config.Backup(settings.Path, p.BackupsDir); err != nil {
				return err
			}
			if err := settings.Save(); err != nil {
				return err
			}
			fmt.Printf("disabled skill %q (skillOverrides entry written)\n", match.Effective)
			return nil
		case "ignore":
			ig, err := commands.LoadIgnores(p.Ignores)
			if err != nil {
				return err
			}
			if !ig.Add(match.Effective) {
				fmt.Printf("%q was already ignored\n", match.Effective)
				return nil
			}
			if flagDryRun {
				fmt.Printf("[dry-run] would add %q to %s\n", match.Effective, p.Ignores)
				return nil
			}
			if err := ig.Save(); err != nil {
				return err
			}
			fmt.Printf("ignored %q (recorded in %s)\n", match.Effective, p.Ignores)
			return nil
		default:
			return fmt.Errorf("unknown strategy %q (use disable-skill|ignore|list)", resolveStrategy)
		}
	},
}

var commandConflictsCmd = &cobra.Command{
	Use:   "conflicts",
	Short: "Report duplicate slash-command names across plugins, user scope, and skills",
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
		cmds := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		skls := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		conflicts := commands.FindConflicts(cmds, skls)
		if !conflictsIncludeIgnored {
			if ig, err := commands.LoadIgnores(p.Ignores); err == nil {
				conflicts = ig.Filter(conflicts)
			}
		}

		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(conflicts)
		}
		if len(conflicts) == 0 {
			fmt.Println("(no conflicts detected)")
			return nil
		}
		for _, c := range conflicts {
			fmt.Printf("  %-22s /%s\n", c.Kind, c.Effective)
			for _, pt := range c.Participants {
				label := pt.Scope
				if pt.PluginID != "" {
					pname, _ := config.ParsePluginID(pt.PluginID)
					label = pt.Scope + ":" + pname
				}
				fmt.Printf("    - %s %s\n", pt.Kind, label)
			}
		}
		fmt.Printf("\n%d conflict(s) detected\n", len(conflicts))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(commandCmd)
	commandCmd.AddCommand(commandListCmd, commandConflictsCmd, commandResolveCmd)
	commandListCmd.Flags().StringVar(&commandScopeFilter, "scope", "", "filter by scope: user|project|plugin|all")
	commandConflictsCmd.Flags().BoolVar(&conflictsIncludeIgnored, "include-ignored", false, "include conflicts marked ignored")
	commandResolveCmd.Flags().StringVar(&resolveStrategy, "strategy", "", "resolution strategy: disable-skill|ignore|list")
}
