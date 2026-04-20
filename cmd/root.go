package cmd

import (
	"os"

	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/spf13/cobra"
)

var (
	// global flags
	flagPath    string // override project path (default: cwd)
	flagDryRun  bool
	flagJSON    bool
	flagNoColor bool
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
		// No args => launch TUI
		return runTUI(cmd.Context())
	},
}

func Execute(version string) error {
	rootCmd.Version = version
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagPath, "path", "", "project path (default: current working directory)")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "print intended changes without writing")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON instead of human-readable text")
	rootCmd.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable ANSI colors")
}

// projectPath returns the --path flag or the current working directory.
func projectPath() (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	return os.Getwd()
}

func resolvePaths() (paths.Paths, error) { return paths.Resolve() }
