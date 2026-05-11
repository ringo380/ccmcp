package cmd

import (
	"context"
	"os"
	"strings"

	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/ringo380/ccmcp/internal/selfupdate"
	"github.com/spf13/cobra"
)

var (
	// global flags
	flagPath          string // override project path (default: cwd)
	flagDryRun        bool
	flagJSON          bool
	flagNoColor       bool
	flagNoUpdateCheck bool
)

var rootCmd = &cobra.Command{
	Use:   "ccmcp",
	Short: "Manage Claude Code MCP servers, plugins, and profiles across all scopes",
	Long: `ccmcp is a dynamic controller for Claude Code's MCP servers and plugins.

Primary use case: reduce context consumption when starting a new project by enabling
only the MCPs and plugins you actually need, without editing JSON files by hand.

Scopes:
  user     — ~/.claude.json#/mcpServers and ~/.claude/settings.json (all projects)
  project  — ~/.claude.json#/projects[<cwd>] (per-directory)
  mcpjson  — ./.mcp.json allow/deny lists (committed to the repo)
  stash    — ccmcp-owned holding area for disabled user MCPs

Run with no subcommand to launch the interactive TUI.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Update check is best-effort and runs only when launching the TUI
		// (interactive, TTY). Subcommands skip it to keep scripted flows quiet.
		if !flagNoUpdateCheck {
			p, err := paths.Resolve()
			if err == nil {
				d := selfupdate.CheckOnLaunch(context.Background(), p, currentVersion())
				if d.ExitAfter {
					return nil
				}
			}
		}
		return runTUI(cmd.Context())
	},
}

// currentVersion is set by Execute() so cmd-level callers can read the version
// stamped into the binary without re-plumbing it through every cobra Run func.
// The raw versionStr from main.fullVersion() carries a "(commit ..., built ...)"
// suffix when running an installed build, so currentVersion strips it down to
// just the semver number for comparison.
var versionStr string

func currentVersion() string {
	if i := strings.IndexByte(versionStr, ' '); i > 0 {
		return versionStr[:i]
	}
	return versionStr
}

func Execute(version string) error {
	rootCmd.Version = version
	versionStr = version
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagPath, "path", "", "project path (default: current working directory)")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "print intended changes without writing")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON instead of human-readable text")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable ANSI colors")
	rootCmd.PersistentFlags().BoolVar(&flagNoUpdateCheck, "no-update-check", false, "skip the launch-time check for a newer ccmcp release (also respects $CCMCP_NO_UPDATE_CHECK)")
}

// projectPath returns the --path flag or the current working directory.
func projectPath() (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	return os.Getwd()
}

func resolvePaths() (paths.Paths, error) { return paths.Resolve() }
