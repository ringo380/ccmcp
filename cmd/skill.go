package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/skills"
	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage Claude Code skills (list, enable, disable)",
}

var skillEnableCmd = &cobra.Command{
	Use:   "enable <name> [<name>...]",
	Short: "Enable skill(s) by removing their skillOverrides entry",
	Args:  cobra.MinimumNArgs(1),
	RunE:  skillSetEnabled(true),
}

var skillDisableCmd = &cobra.Command{
	Use:   "disable <name> [<name>...]",
	Short: "Disable skill(s) by writing skillOverrides[<name>]=\"off\"",
	Args:  cobra.MinimumNArgs(1),
	RunE:  skillSetEnabled(false),
}

func skillSetEnabled(enable bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		// First pass: determine what would change without mutating settings.
		var changed []string
		for _, name := range args {
			if enable {
				cur, has := settings.SkillOverride(name)
				if has && cur == "off" {
					changed = append(changed, name)
				}
			} else {
				cur, has := settings.SkillOverride(name)
				if has && cur == "off" {
					continue
				}
				changed = append(changed, name)
			}
		}
		if len(changed) == 0 {
			fmt.Println("(no change)")
			return nil
		}
		verb := "disabled"
		if enable {
			verb = "enabled"
		}
		if flagDryRun {
			fmt.Printf("[dry-run] would %s: %s\n", verb, strings.Join(changed, ", "))
			return nil
		}
		// Apply changes only after dry-run check.
		for _, name := range changed {
			if enable {
				settings.RemoveSkillOverride(name)
			} else {
				settings.SetSkillOverride(name, "off")
			}
		}
		if err := config.Backup(settings.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := settings.Save(); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", verb, strings.Join(changed, ", "))
		return nil
	}
}

var (
	skillScopeFilter   string
	skillFilterEnabled bool
	skillFilterDisabled bool
)

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List skills across user, project, and plugin scopes",
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
		all := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)

		var rows []skills.Skill
		for _, s := range all {
			if skillScopeFilter != "" && skillScopeFilter != "all" && string(s.Scope) != skillScopeFilter {
				continue
			}
			if skillFilterEnabled && !s.Enabled {
				continue
			}
			if skillFilterDisabled && s.Enabled {
				continue
			}
			rows = append(rows, s)
		}

		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		}
		if len(rows) == 0 {
			fmt.Println("(no skills match)")
			return nil
		}
		for _, s := range rows {
			mark := "[x]"
			if !s.Enabled {
				mark = "[ ]"
			}
			src := string(s.Scope)
			if s.Scope == skills.ScopePlugin {
				pname, _ := config.ParsePluginID(s.PluginID)
				src = "plugin:" + pname
			}
			desc := assets.Truncate(s.Description, 70)
			fmt.Printf("  %s %-30s  %-20s  %s\n", mark, s.Name, src, desc)
		}
		fmt.Printf("\n%d skills shown (%s)\n", len(rows), summarizeSkills(rows))
		return nil
	},
}

func summarizeSkills(rows []skills.Skill) string {
	counts := map[skills.Scope]int{}
	var enabled int
	for _, s := range rows {
		counts[s.Scope]++
		if s.Enabled {
			enabled++
		}
	}
	parts := []string{fmt.Sprintf("%d enabled", enabled)}
	for _, sc := range []skills.Scope{skills.ScopeUser, skills.ScopeProject, skills.ScopePlugin} {
		if counts[sc] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sc], sc))
		}
	}
	return strings.Join(parts, ", ")
}

var (
	skillNewScope       string
	skillNewDescription string
	skillMoveTo         string
	skillRmScope        string
	skillShowScope      string
)

var skillNewCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Scaffold a new skill at <scope>/skills/<name>/SKILL.md",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		scope := skills.Scope(skillNewScope)
		if flagDryRun {
			root := p.ClaudeConfigDir
			if scope == skills.ScopeProject {
				root = proj + "/.claude"
			}
			fmt.Printf("[dry-run] would create %s/skills/%s/SKILL.md\n", root, args[0])
			return nil
		}
		path, err := skills.Scaffold(args[0], skillNewDescription, scope, p.ClaudeConfigDir, proj)
		if err != nil {
			return err
		}
		fmt.Printf("created %s\n", path)
		return nil
	},
}

var skillMoveCmd = &cobra.Command{
	Use:   "move <name>",
	Short: "Move a skill between user and project scope (--to user|project)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		to := skills.Scope(skillMoveTo)
		from := skills.ScopeUser
		if to == skills.ScopeUser {
			from = skills.ScopeProject
		}
		if flagDryRun {
			fmt.Printf("[dry-run] would move skill %s: %s -> %s\n", args[0], from, to)
			return nil
		}
		src, dst, err := skills.Move(args[0], from, to, p.ClaudeConfigDir, proj)
		if err != nil {
			return err
		}
		fmt.Printf("moved %s -> %s\n", src, dst)
		return nil
	},
}

var skillRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"remove", "delete"},
	Short:   "Delete a user- or project-scope skill (refuses plugin scope)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, _ := projectPath()
		scope := skills.Scope(skillRmScope)
		if flagDryRun {
			root, _ := resolveSkillRoot(scope, p.ClaudeConfigDir, proj)
			fmt.Printf("[dry-run] would delete %s/%s\n", root, args[0])
			return nil
		}
		path, existed, err := skills.Remove(args[0], scope, p.ClaudeConfigDir, proj)
		if err != nil {
			return err
		}
		if !existed {
			fmt.Printf("skill %q not found at %s\n", args[0], path)
			return nil
		}
		fmt.Printf("deleted %s\n", path)
		return nil
	},
}

var skillShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Render a skill's frontmatter and file path",
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
		all := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		var match *skills.Skill
		for i := range all {
			if all[i].Name == args[0] {
				if skillShowScope != "" && string(all[i].Scope) != skillShowScope {
					continue
				}
				match = &all[i]
				break
			}
		}
		if match == nil {
			return fmt.Errorf("skill %q not found", args[0])
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
		fmt.Printf("enabled:     %v\n", match.Enabled)
		fmt.Printf("description: %s\n", match.Description)
		fmt.Printf("path:        %s/SKILL.md\n", match.Dir)
		return nil
	},
}

// resolveSkillRoot is a small exported helper used for dry-run messages.
func resolveSkillRoot(scope skills.Scope, claudeConfigDir, projectDir string) (string, error) {
	switch scope {
	case skills.ScopeUser:
		return claudeConfigDir + "/skills", nil
	case skills.ScopeProject:
		return projectDir + "/.claude/skills", nil
	}
	return "", fmt.Errorf("cannot write to %s scope", scope)
}

func init() {
	rootCmd.AddCommand(skillCmd)
	skillCmd.AddCommand(skillListCmd, skillEnableCmd, skillDisableCmd, skillNewCmd, skillMoveCmd, skillRmCmd, skillShowCmd)
	skillListCmd.Flags().StringVar(&skillScopeFilter, "scope", "", "filter by scope: user|project|plugin|all")
	skillListCmd.Flags().BoolVar(&skillFilterEnabled, "enabled", false, "show only enabled skills")
	skillListCmd.Flags().BoolVar(&skillFilterDisabled, "disabled", false, "show only disabled skills")
	skillNewCmd.Flags().StringVar(&skillNewScope, "scope", "user", "scope: user|project")
	skillNewCmd.Flags().StringVar(&skillNewDescription, "description", "", "frontmatter description")
	skillMoveCmd.Flags().StringVar(&skillMoveTo, "to", "", "destination scope: user|project (required)")
	_ = skillMoveCmd.MarkFlagRequired("to")
	skillRmCmd.Flags().StringVar(&skillRmScope, "scope", "user", "scope: user|project")
	skillShowCmd.Flags().StringVar(&skillShowScope, "scope", "", "filter by scope when names collide")
}
