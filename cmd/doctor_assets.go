package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/doctor"
	"github.com/ringo380/ccmcp/internal/skills"
)

var doctorAssetsCmd = &cobra.Command{
	Use:   "assets",
	Short: "Lint skills, agents, commands, and plugin manifests against Claude Code constraints",
	Long: `Validates discovered skills, agents, commands, and plugin manifests against
Claude Code 2.1.141 constraints:

  SKILL001  skill name doesn't match ^[a-z0-9-]+$ (hard requirement)
  SKILL002  skill name exceeds 64 characters (hard cap)
  SKILL003  skill description + when_to_use exceeds the 1536-char display limit
            (warn at 1200, error at 1536 — content past the cap is silently dropped)
  AGENT001  agent description approaches/exceeds the 1536-char display limit
  CMD001    command description exceeds 500-char soft limit (palette readability)
  PLUGIN001 plugin manifest description exceeds 500-char soft limit

Exit code 1 if any error-severity issues are found (warnings alone are exit 0),
so this command is CI-friendly.`,
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

		sks := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		ags := agents.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		cmds := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)

		var issues []doctor.Issue
		issues = append(issues, doctor.LintSkills(sks)...)
		issues = append(issues, doctor.LintAgents(ags)...)
		issues = append(issues, doctor.LintCommands(cmds)...)

		if flagJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(issues)
		}

		if len(issues) == 0 {
			fmt.Println("✓  no asset lint issues")
			return nil
		}

		var anyError bool
		for _, iss := range issues {
			icon := "·"
			switch iss.Severity {
			case doctor.SeverityError:
				icon = "✗"
				anyError = true
			case doctor.SeverityWarning:
				icon = "⚠"
			}
			loc := iss.File
			if iss.Line > 0 {
				loc = fmt.Sprintf("%s:%d", iss.File, iss.Line)
			}
			fmt.Printf("  %s [%s] %s — %s\n", icon, iss.Code, loc, iss.Message)
		}
		if anyError {
			return fmt.Errorf("asset lint errors found — see above")
		}
		return nil
	},
}

func init() {
	doctorCmd.AddCommand(doctorAssetsCmd)
}
