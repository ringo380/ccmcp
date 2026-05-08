package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/discovery"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/ringo380/ccmcp/internal/skills"
)

var (
	discoverJSON    bool
	discoverRefresh bool
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Browse Claude Code marketplaces from authoritative online sources",
	Long: `Discover surfaces marketplaces from a merged set of registries:
the embedded ccmcp-curated list, an Anthropic-published registry probe,
awesome-list-style README scrapers, and any user-supplied registry URLs
configured under settings.json#discoverySources.

Subcommands:
  list                  show every discoverable marketplace
  show <marketplace>    fetch a marketplace's manifest and list its plugins
  plugin <mkt> <plugin> shallow-clone the plugin and report conflicts against
                        currently-installed skills/agents/commands/MCPs/hooks`,
}

var discoverListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered marketplaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		opts := discovery.Options{
			Sources:   buildSources(settings),
			CachePath: discovery.CachePath(p.ClaudeConfigDir),
			Refresh:   discoverRefresh,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		res, err := discovery.Discover(ctx, opts)
		if err != nil {
			return err
		}

		if discoverJSON {
			return writeJSON(cmd, res)
		}

		if res.FromCache {
			fmt.Fprintf(cmd.OutOrStdout(), "(showing cached results from %s)\n", res.FetchedAt.Format(time.RFC3339))
		}
		if len(res.Marketplaces) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no marketplaces found")
		} else {
			for _, mp := range res.Marketplaces {
				origin := mp.Origin
				if origin == "" {
					origin = "?"
				}
				src := mp.Source
				if mp.Repo != "" {
					src = mp.Source + " " + mp.Repo
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %-32s %s\n", mp.Name, src)
				if mp.Description != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "      %s\n", mp.Description)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "      origin: %s\n", origin)
			}
		}
		if len(res.Errors) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "\nsource errors:")
			ids := make([]string, 0, len(res.Errors))
			for k := range res.Errors {
				ids = append(ids, k)
			}
			sort.Strings(ids)
			for _, id := range ids {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", id, res.Errors[id])
			}
		}
		return nil
	},
}

var discoverShowCmd = &cobra.Command{
	Use:   "show <marketplace>",
	Short: "Fetch a marketplace's manifest and list its plugins",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mp, err := lookupRemote(cmd, args[0])
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		man, err := discovery.FetchManifest(ctx, &http.Client{Timeout: 15 * time.Second}, mp)
		if err != nil {
			return fmt.Errorf("fetch manifest: %w", err)
		}
		if discoverJSON {
			return writeJSON(cmd, man)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s — %d plugin(s)\n", mp.Name, len(man.Plugins))
		for _, pl := range man.Plugins {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", pl.Name)
		}
		return nil
	},
}

var discoverPluginCmd = &cobra.Command{
	Use:   "plugin <marketplace> <plugin>",
	Short: "Preview-clone a plugin and report conflicts",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		mp, err := lookupRemote(cmd, args[0])
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		man, err := discovery.FetchManifest(ctx, &http.Client{Timeout: 15 * time.Second}, mp)
		if err != nil {
			return fmt.Errorf("fetch manifest: %w", err)
		}
		var plugin discovery.RemotePlugin
		found := false
		for _, p := range man.Plugins {
			if p.Name == args[1] {
				plugin = discovery.RemotePlugin{Name: p.Name, Source: p.Source}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("plugin %q not found in marketplace %q", args[1], mp.Name)
		}

		p, err := resolvePaths()
		if err != nil {
			return err
		}
		preview, err := discovery.PreviewClone(ctx, p, mp, plugin)
		if err != nil {
			return fmt.Errorf("preview clone: %w", err)
		}

		state, err := buildConflictState(p)
		if err != nil {
			return err
		}
		report := discovery.DetectConflicts(preview.Dir, mp, plugin, state)

		if discoverJSON {
			return writeJSON(cmd, map[string]any{
				"preview":  preview,
				"conflict": report,
			})
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "previewed %s/%s @ %s\n", mp.Name, plugin.Name, shortSha(preview.Sha))
		fmt.Fprintf(out, "  cache: %s\n", preview.Dir)
		if report.Empty() {
			fmt.Fprintln(out, "  no conflicts")
			return nil
		}
		fmt.Fprintf(out, "  %d conflict(s):\n", report.Total())
		if report.MarketplaceNameClash {
			fmt.Fprintln(out, "    marketplace name already known")
		}
		if report.PluginIDClash {
			fmt.Fprintln(out, "    plugin ID already installed")
		}
		printConflicts(out, "skill", report.Skills)
		printConflicts(out, "agent", report.Agents)
		printConflicts(out, "command", report.Commands)
		printConflicts(out, "mcp-server", report.MCPServers)
		printConflicts(out, "hook", report.Hooks)
		return nil
	},
}

func printConflicts(out interface{ Write([]byte) (int, error) }, kind string, conflicts []discovery.Conflict) {
	for _, c := range conflicts {
		fmt.Fprintf(out, "    %s %q already provided by %s\n", kind, c.Name, c.ExistingSource)
	}
}

func shortSha(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "HEAD"
	}
	return sha
}

// lookupRemote finds a discovered marketplace by name. Uses the cache (if
// fresh) or runs a fresh Discover() — either way avoids the user having to
// rediscover before each show/plugin call.
func lookupRemote(cmd *cobra.Command, name string) (discovery.RemoteMarketplace, error) {
	p, err := resolvePaths()
	if err != nil {
		return discovery.RemoteMarketplace{}, err
	}
	settings, err := config.LoadSettings(p.SettingsJSON)
	if err != nil {
		return discovery.RemoteMarketplace{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	res, err := discovery.Discover(ctx, discovery.Options{
		Sources:   buildSources(settings),
		CachePath: discovery.CachePath(p.ClaudeConfigDir),
		Refresh:   discoverRefresh,
	})
	if err != nil {
		return discovery.RemoteMarketplace{}, err
	}
	for _, mp := range res.Marketplaces {
		if strings.EqualFold(mp.Name, name) {
			return mp, nil
		}
	}
	return discovery.RemoteMarketplace{}, fmt.Errorf("no discovered marketplace named %q (try `ccmcp discover list`)", name)
}

// buildSources merges the default sources with any user-configured registry URLs.
func buildSources(settings *config.Settings) []discovery.Source {
	out := discovery.DefaultSources()
	for _, u := range settings.DiscoverySources() {
		out = append(out, discovery.UserURLSource(u))
	}
	return out
}

// buildConflictState builds a ConflictState from the user's currently-installed
// scanners. Mirrors the loading the TUI/skill/agent/command tabs do.
func buildConflictState(p paths.Paths) (discovery.ConflictState, error) {
	settings, err := config.LoadSettings(p.SettingsJSON)
	if err != nil {
		return discovery.ConflictState{}, err
	}
	installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
	if err != nil {
		return discovery.ConflictState{}, err
	}
	cj, err := config.LoadClaudeJSON(p.ClaudeJSON)
	if err != nil {
		return discovery.ConflictState{}, err
	}

	skillRows := skills.Discover(p.ClaudeConfigDir, "", settings, installed, p.PluginsDir)
	agentRows := agents.Discover(p.ClaudeConfigDir, "", settings, installed, p.PluginsDir)
	cmdRows := commands.Discover(p.ClaudeConfigDir, "", settings, installed, p.PluginsDir)

	var mcpKeys []string
	for k := range cj.UserMCPs() {
		mcpKeys = append(mcpKeys, k)
	}

	var mktNames []string
	for _, mp := range settings.ExtraMarketplaces() {
		mktNames = append(mktNames, mp.Name)
	}
	cloned, _ := install.ListLocalMarketplaces(p)
	mktNames = append(mktNames, cloned...)

	var ids []string
	for _, ip := range installed.List() {
		ids = append(ids, ip.ID)
	}

	return discovery.BuildState(skillRows, agentRows, cmdRows, mcpKeys, nil, mktNames, ids), nil
}

func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func init() {
	rootCmd.AddCommand(discoverCmd)
	discoverCmd.AddCommand(discoverListCmd)
	discoverCmd.AddCommand(discoverShowCmd)
	discoverCmd.AddCommand(discoverPluginCmd)

	discoverCmd.PersistentFlags().BoolVar(&discoverJSON, "json", false, "emit machine-readable JSON output")
	discoverCmd.PersistentFlags().BoolVar(&discoverRefresh, "refresh", false, "bypass discovery cache and re-fetch")
}
