package cmd

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ringo380/ccmcp/internal/classify"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/stringslice"
	"github.com/spf13/cobra"
)

var (
	pruneIncludeStashGhosts bool
	pruneYes                bool
)

var mcpPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove orphaned entries from the current project's disabledMcpServers",
	Long: `Removes "stale" entries from ~/.claude.json#/projects[<cwd>]/disabledMcpServers
— entries that reference an MCP source no longer on disk. Categories removed by default:

  orphan (plugin)  — "plugin:X:Y" where plugin X isn't installed anywhere
  orphan (stdio)   — plain names with no source in user / local / project / stash / plugin

NEVER removed by default:
  plugin (disabled) — "plugin:X:Y" where plugin X is installed but globally off.
                      Re-enabling the plugin would re-activate the MCP, and the user
                      likely wanted it off per-project. Re-enable the plugin or pass
                      --include-stash-ghosts? No — for these, use 'ccmcp mcp override
                      <name> --undo' explicitly.

Optional:
  --include-stash-ghosts  also remove plain-name entries that match a stash entry
                          (harmless, but sometimes users want to keep them as
                          intentional per-project markers)

Flags:
  --dry-run   list what would be removed; make no changes
  --yes       skip the interactive confirmation prompt
  --path P    target a different project than cwd`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMCPPrune()
	},
}

func runMCPPrune() error {
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
	settings, _ := config.LoadSettings(p.SettingsJSON)
	installed, _ := config.LoadInstalledPlugins(p.InstalledPlugins)
	stash, _ := config.LoadStash(p.Stash)

	overrides := cj.ProjectDisabledMcpServers(proj)
	if len(overrides) == 0 {
		fmt.Printf("no per-project overrides for %s — nothing to prune\n", proj)
		return nil
	}

	pluginMCPs := config.ScanAllInstalledPluginMCPs(settings, installed, p.PluginsDir)
	var stashNames []string
	if stash != nil {
		stashNames = stash.Names()
	}
	cls := classify.Classify(overrides, cj.UserMCPNames(), cj.ProjectMCPNames(proj),
		cj.ClaudeAiEverConnected(), stashNames, pluginMCPs)

	var toRemove []string
	toRemove = append(toRemove, cls.OrphanPlugin...)
	toRemove = append(toRemove, cls.OrphanStdio...)
	if pruneIncludeStashGhosts {
		toRemove = append(toRemove, cls.StashGhost...)
	}
	sort.Strings(toRemove)

	if len(toRemove) == 0 {
		fmt.Printf("no orphaned overrides to prune in %s\n", proj)
		if len(cls.StashGhost) > 0 {
			fmt.Printf("(%d stash-ghost entr%s exist — pass --include-stash-ghosts to sweep them too)\n",
				len(cls.StashGhost), classify.PluralY(len(cls.StashGhost)))
		}
		return nil
	}

	fmt.Printf("Project: %s\n", proj)
	fmt.Printf("Would remove %d entr%s from disabledMcpServers:\n", len(toRemove), classify.PluralY(len(toRemove)))
	for _, k := range toRemove {
		fmt.Printf("  - %s  (%s)\n", k, reasonForPruneEntry(k, cls))
	}
	if n := len(cls.PluginDisabled); n > 0 {
		fmt.Printf("\nKept (plugin installed but globally disabled — use `plugin enable %s` to reactivate):\n",
			"<id>")
		for _, k := range cls.PluginDisabled {
			fmt.Printf("  ~ %s\n", k)
		}
	}

	if flagDryRun {
		fmt.Println("\n[dry-run] no changes made")
		return nil
	}
	if !pruneYes {
		if !confirmInteractive(fmt.Sprintf("\nRemove %d entr%s? (y/N) ", len(toRemove), classify.PluralY(len(toRemove)))) {
			fmt.Println("aborted")
			return nil
		}
	}

	// Remove each entry from the live list.
	remaining := overrides
	for _, k := range toRemove {
		remaining = stringslice.Remove(remaining, k)
	}
	cj.SetProjectDisabledMcpServers(proj, remaining)

	if err := backupAndSave(p, cj); err != nil {
		return err
	}
	fmt.Printf("pruned %d entr%s from %s\n", len(toRemove), classify.PluralY(len(toRemove)), proj)
	return nil
}

func reasonForPruneEntry(k string, cls classify.Overrides) string {
	if stringslice.Contains(cls.OrphanPlugin, k) {
		return "plugin not installed"
	}
	if stringslice.Contains(cls.StashGhost, k) {
		return "stash ghost"
	}
	return "no source on disk"
}

func confirmInteractive(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes"
}

func init() {
	mcpPruneCmd.Flags().BoolVar(&pruneIncludeStashGhosts, "include-stash-ghosts", false, "also remove plain-name overrides matching a stash entry")
	mcpPruneCmd.Flags().BoolVar(&pruneYes, "yes", false, "skip the interactive confirmation prompt")
	mcpCmd.AddCommand(mcpPruneCmd)
}
