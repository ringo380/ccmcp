package cmd

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/spf13/cobra"
)

var _ = strings.Contains

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage MCP servers (enable, disable, stash, restore, move)",
}

// --- flag-bearing vars ---
var (
	mcpScope     string // normalized: user | project | mcpjson | stash
	mcpFromStash bool
	mcpToStash   bool
	mcpAll       bool
	mcpMoveTo    string
)

// normalizeScope maps user-friendly scope names (aligned with Claude Code's own
// terminology — "local", "project") to ccmcp's legacy internal keys.
func normalizeScope(s string) string {
	switch s {
	case "local":
		return "project" // ~/.claude.json#/projects[<cwd>]/mcpServers
	case "project-shared", "shared":
		return "mcpjson" // ./.mcp.json
	default:
		return s
	}
}

// --- list -------------------------------------------------------------------

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List MCP servers across scopes",
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
		scope := normalizeScope(mcpScope)
		scopes := []string{scope}
		if scope == "" || scope == "all" {
			scopes = []string{"user", "project", "mcpjson", "plugin", "claudeai", "overrides", "stash"}
		}
		for _, s := range scopes {
			switch s {
			case "user":
				printMCPs("User MCPs (~/.claude.json#/mcpServers — loads in every project)", cj.UserMCPs())
			case "project":
				printMCPs(fmt.Sprintf("Local MCPs (%s — this dir only)", proj), cj.ProjectMCPs(proj))
			case "stash":
				printMCPs("Stashed", stash.Entries())
			case "mcpjson":
				if m, err := config.LoadMCPJson(paths.ProjectMCPJSON(proj)); err == nil {
					printMCPs("Project-shared (./.mcp.json, git-tracked)", m.Servers())
				}
			case "claudeai":
				claudeai := cj.ClaudeAiEverConnected()
				fmt.Println("Claude.ai integrations (from claudeAiMcpEverConnected — best-effort):")
				if len(claudeai) == 0 {
					fmt.Println("  (none)")
					fmt.Println()
				} else {
					sort.Strings(claudeai)
					for _, n := range claudeai {
						fmt.Println("  - " + n)
					}
					fmt.Println()
				}
			case "overrides":
				keys := cj.ProjectDisabledMcpServers(proj)
				fmt.Printf("Per-project overrides for %s:\n", proj)
				if len(keys) == 0 {
					fmt.Println("  (none)")
					fmt.Println()
				} else {
					sort.Strings(keys)
					for _, k := range keys {
						fmt.Println("  ~ " + k)
					}
					fmt.Println()
				}
			case "plugin":
				settings, _ := config.LoadSettings(p.SettingsJSON)
				installed, _ := config.LoadInstalledPlugins(p.InstalledPlugins)
				srcs := config.ScanEnabledPluginMCPs(settings, installed, p.PluginsDir)
				fmt.Println("Plugin-registered MCPs (auto-load via enabled plugins):")
				if len(srcs) == 0 {
					fmt.Println("  (none)")
					fmt.Println()
				} else {
					for _, n := range config.SortedPluginSources(srcs) {
						names := []string{}
						for _, s := range srcs[n] {
							pname, _ := config.ParsePluginID(s.PluginID)
							names = append(names, pname)
						}
						fmt.Printf("  %-30s via: %s\n", n, strings.Join(names, ", "))
					}
					fmt.Println()
				}
			default:
				return fmt.Errorf("unknown --scope %q (use user|local|project|plugin|claudeai|overrides|stash|all)", s)
			}
		}
		return nil
	},
}

func printMCPs(title string, m map[string]any) {
	fmt.Println(title + ":")
	if len(m) == 0 {
		fmt.Println("  (none)")
		fmt.Println()
		return
	}
	// print sorted by key
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Printf("  %-30s %s\n", k, config.DescribeMCP(m[k]))
	}
	fmt.Println()
}

// --- enable / disable -------------------------------------------------------

var mcpEnableCmd = &cobra.Command{
	Use:   "enable <name> [<name>...]",
	Short: "Enable MCP(s) in a scope",
	Long: `Enable one or more MCP servers.

Defaults to --scope project: copies from stash (or from existing user-scope entry)
into the current project's config so it only loads for this directory.

Flags:
  --scope user      add to user scope (loads globally)
  --scope project   add to current project (default)
  --scope mcpjson   add name to project's allow-list for .mcp.json servers
  --from stash      require that source is the stash (error if name not stashed)`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mcpScope = normalizeScope(mcpScope)
		if mcpScope == "" {
			mcpScope = "project"
		}
		return runMCPEnable(args)
	},
}

var mcpDisableCmd = &cobra.Command{
	Use:   "disable <name> [<name>...]",
	Short: "Disable MCP(s) in a scope",
	Long: `Disable one or more MCP servers.

Defaults to --scope project: removes from the current project's config.
With --to stash, the removed config is preserved in the stash for later re-enable.`,
	Args: cobra.MatchAll(cobra.MinimumNArgs(0), func(cmd *cobra.Command, args []string) error {
		if !mcpAll && len(args) == 0 {
			return fmt.Errorf("pass one or more names, or --all")
		}
		return nil
	}),
	RunE: func(cmd *cobra.Command, args []string) error {
		mcpScope = normalizeScope(mcpScope)
		if mcpScope == "" {
			mcpScope = "project"
		}
		return runMCPDisable(args)
	},
}

var (
	overrideUndo     bool
	overrideSource   string
	overridePluginOf string
)

var mcpOverrideCmd = &cobra.Command{
	Use:   "override <name>",
	Short: "Disable an MCP for the current project only (writes to disabledMcpServers)",
	Long: `Add an MCP to the current project's disabledMcpServers list, which is the same
mechanism Claude Code's /mcp dialog uses to toggle an MCP off per-project without
disabling it globally.

Name forms accepted:
  foo                         stdio MCP named "foo"
  claude.ai Gmail             claude.ai integration
  plugin:<plugin>:<server>    plugin-sourced MCP (full override key)

If the name is ambiguous (e.g., "context7" could be a stdio MCP, a claude.ai integration,
or a plugin-sourced MCP), pass --source {stdio,plugin,claudeai} and for plugin use --plugin <name>.

--undo removes the override (re-enables the MCP for this project).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMCPOverride(args[0])
	},
}

var mcpMoveCmd = &cobra.Command{
	Use:   "move <name> --to <scope>",
	Short: "Move an MCP's config between scopes (user ↔ local ↔ stash)",
	Long: `Move the named MCP's full config from its current location(s) to the target scope.
Removes the entry from every other mutable scope so there's no duplication (the main reason
to move: "I don't want this auto-loading in every project anymore — park it in local/stash").

Scopes (Claude Code terminology):
  user     — ~/.claude.json#/mcpServers         (loads in every project)
  local    — ~/.claude.json#/projects[<cwd>]    (this dir only, private)
  stash    — ~/.claude-mcp-stash.json            (parked, not active anywhere)

Aliases: 'project' means 'local' here for back-compat with earlier ccmcp versions.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := normalizeScope(mcpMoveTo)
		if target != "user" && target != "project" && target != "stash" {
			return fmt.Errorf("--to must be one of user|local|stash (got %q)", mcpMoveTo)
		}
		return runMCPMove(args[0], target)
	},
}

func runMCPEnable(names []string) error {
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

	switch mcpScope {
	case "mcpjson":
		enabled := cj.ProjectMcpjsonEnabled(proj)
		disabled := cj.ProjectMcpjsonDisabled(proj)
		for _, n := range names {
			enabled = uniqueAppend(enabled, n)
			disabled = removeString(disabled, n)
		}
		cj.SetProjectMcpjsonEnabled(proj, enabled)
		cj.SetProjectMcpjsonDisabled(proj, disabled)
	case "user", "project":
		for _, n := range names {
			cfg, ok := findMCPConfig(n, cj, stash, proj)
			if !ok {
				return fmt.Errorf("%q not found in stash, user, or project scope — nothing to enable", n)
			}
			if mcpScope == "user" {
				cj.SetUserMCP(n, cfg)
			} else {
				cj.SetProjectMCP(proj, n, cfg)
			}
			// If it came from the stash and the user explicitly opted in, pull it out of stash.
			if mcpFromStash {
				stash.Delete(n)
			}
		}
	default:
		return fmt.Errorf("unknown --scope %q", mcpScope)
	}

	if flagDryRun {
		fmt.Printf("[dry-run] would enable in scope=%s: %s\n", mcpScope, strings.Join(names, ", "))
		return nil
	}
	if err := backupAndSave(p, cj); err != nil {
		return err
	}
	if mcpFromStash {
		if err := stash.Save(); err != nil {
			return err
		}
	}
	fmt.Printf("enabled in scope=%s: %s\n", mcpScope, strings.Join(names, ", "))
	return nil
}

func runMCPDisable(names []string) error {
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

	var effective []string

	switch mcpScope {
	case "mcpjson":
		enabled := cj.ProjectMcpjsonEnabled(proj)
		disabled := cj.ProjectMcpjsonDisabled(proj)
		targets := names
		if mcpAll {
			// disable every server listed in .mcp.json
			if m, err := config.LoadMCPJson(paths.ProjectMCPJSON(proj)); err == nil {
				targets = m.Names()
			}
		}
		for _, n := range targets {
			disabled = uniqueAppend(disabled, n)
			enabled = removeString(enabled, n)
			effective = append(effective, n)
		}
		cj.SetProjectMcpjsonEnabled(proj, enabled)
		cj.SetProjectMcpjsonDisabled(proj, disabled)
	case "user":
		targets := names
		if mcpAll {
			targets = cj.UserMCPNames()
		}
		for _, n := range targets {
			cfg, ok := cj.DeleteUserMCP(n)
			if !ok {
				fmt.Fprintf(os.Stderr, "warn: %q not present in user scope\n", n)
				continue
			}
			if mcpToStash {
				stash.Put(n, cfg)
			}
			effective = append(effective, n)
		}
	case "project":
		targets := names
		if mcpAll {
			targets = cj.ProjectMCPNames(proj)
		}
		for _, n := range targets {
			// capture config first so we can stash it if requested
			cfg, ok := cj.ProjectMCPs(proj)[n]
			if !ok {
				fmt.Fprintf(os.Stderr, "warn: %q not present in project %s\n", n, proj)
				continue
			}
			cj.DeleteProjectMCP(proj, n)
			if mcpToStash {
				stash.Put(n, cfg)
			}
			effective = append(effective, n)
		}
	default:
		return fmt.Errorf("unknown --scope %q", mcpScope)
	}

	if flagDryRun {
		fmt.Printf("[dry-run] would disable in scope=%s: %s\n", mcpScope, strings.Join(effective, ", "))
		return nil
	}
	if err := backupAndSave(p, cj); err != nil {
		return err
	}
	if mcpToStash {
		if err := stash.Save(); err != nil {
			return err
		}
	}
	if len(effective) == 0 {
		fmt.Println("nothing changed")
	} else {
		fmt.Printf("disabled in scope=%s: %s\n", mcpScope, strings.Join(effective, ", "))
	}
	return nil
}

// --- stash / restore --------------------------------------------------------

var mcpStashCmd = &cobra.Command{
	Use:   "stash [<name>...]",
	Short: "Move user-scope MCPs into the stash (disables them but keeps their config)",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
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

		var moved []string
		if len(args) == 0 {
			for name, cfg := range cj.ClearUserMCPs() {
				stash.Put(name, cfg)
				moved = append(moved, name)
			}
		} else {
			for _, n := range args {
				cfg, ok := cj.DeleteUserMCP(n)
				if !ok {
					fmt.Fprintf(os.Stderr, "warn: %q not in user scope\n", n)
					continue
				}
				stash.Put(n, cfg)
				moved = append(moved, n)
			}
		}
		slices.Sort(moved)

		if flagDryRun {
			fmt.Printf("[dry-run] would stash: %s\n", strings.Join(moved, ", "))
			return nil
		}
		if err := backupAndSave(p, cj); err != nil {
			return err
		}
		if err := stash.Save(); err != nil {
			return err
		}
		if len(moved) == 0 {
			fmt.Println("nothing to stash")
		} else {
			fmt.Printf("stashed: %s\n", strings.Join(moved, ", "))
		}
		return nil
	},
}

var mcpRestoreCmd = &cobra.Command{
	Use:   "restore [<name>...]",
	Short: "Move stashed MCPs back to user scope",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
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
		names := args
		if len(names) == 0 {
			names = stash.Names()
		}
		var moved []string
		for _, n := range names {
			cfg, ok := stash.Get(n)
			if !ok {
				fmt.Fprintf(os.Stderr, "warn: %q not in stash\n", n)
				continue
			}
			cj.SetUserMCP(n, cfg)
			stash.Delete(n)
			moved = append(moved, n)
		}
		if flagDryRun {
			fmt.Printf("[dry-run] would restore: %s\n", strings.Join(moved, ", "))
			return nil
		}
		if err := backupAndSave(p, cj); err != nil {
			return err
		}
		if err := stash.Save(); err != nil {
			return err
		}
		fmt.Printf("restored: %s\n", strings.Join(moved, ", "))
		return nil
	},
}

// --- override --------------------------------------------------------------

func runMCPOverride(nameOrKey string) error {
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

	key, err := resolveOverrideKey(p, proj, cj, nameOrKey)
	if err != nil {
		return err
	}

	var changed bool
	var verb string
	if overrideUndo {
		changed = cj.RemoveProjectDisabledMcpServer(proj, key)
		verb = "re-enabled"
	} else {
		changed = cj.AddProjectDisabledMcpServer(proj, key)
		verb = "disabled"
	}
	if !changed {
		fmt.Printf("no change — %q already %s for %s\n", key, verb, proj)
		return nil
	}
	if flagDryRun {
		fmt.Printf("[dry-run] would %s %q in %s\n", verb, key, proj)
		return nil
	}
	if err := backupAndSave(p, cj); err != nil {
		return err
	}
	fmt.Printf("%s %q for %s\n", verb, key, proj)
	return nil
}

// resolveOverrideKey turns a user-supplied name into the correct disabledMcpServers key,
// disambiguating by --source/--plugin flags when necessary.
func resolveOverrideKey(p paths.Paths, proj string, cj *config.ClaudeJSON, nameOrKey string) (string, error) {
	// If it already looks like a fully-qualified key, accept it verbatim.
	if strings.HasPrefix(nameOrKey, "plugin:") || strings.HasPrefix(nameOrKey, "claude.ai ") {
		return nameOrKey, nil
	}
	// Explicit source hint
	switch overrideSource {
	case "plugin":
		if overridePluginOf == "" {
			return "", fmt.Errorf("--source plugin requires --plugin <name>")
		}
		return config.OverrideKey(config.SourcePlugin, nameOrKey, overridePluginOf), nil
	case "claudeai":
		return config.OverrideKey(config.SourceClaude, nameOrKey, ""), nil
	case "stdio", "user", "local", "project", "":
		// continue auto-resolve
	default:
		return "", fmt.Errorf("--source must be one of stdio|plugin|claudeai (got %q)", overrideSource)
	}

	// Auto-resolve: check all sources, fail on ambiguity.
	var candidates []string
	// stdio: user or local or existing disabled-list plain entry
	if _, ok := cj.UserMCPs()[nameOrKey]; ok {
		candidates = append(candidates, nameOrKey)
	} else if _, ok := cj.ProjectMCPs(proj)[nameOrKey]; ok {
		candidates = append(candidates, nameOrKey)
	} else {
		// could be a stdio name already disabled
		for _, k := range cj.ProjectDisabledMcpServers(proj) {
			if k == nameOrKey {
				candidates = append(candidates, nameOrKey)
				break
			}
		}
	}
	// plugin: look at all enabled plugins
	settings, _ := config.LoadSettings(p.SettingsJSON)
	installed, _ := config.LoadInstalledPlugins(p.InstalledPlugins)
	pluginMCPs := config.ScanEnabledPluginMCPs(settings, installed, p.PluginsDir)
	if srcs, ok := pluginMCPs[nameOrKey]; ok {
		for _, s := range srcs {
			pname, _ := config.ParsePluginID(s.PluginID)
			candidates = append(candidates, config.OverrideKey(config.SourcePlugin, nameOrKey, pname))
		}
	}
	// claude.ai
	for _, full := range cj.ClaudeAiEverConnected() {
		if strings.TrimPrefix(full, "claude.ai ") == nameOrKey {
			candidates = append(candidates, full)
		}
	}

	if len(candidates) == 0 {
		// Accept as raw stdio name anyway — user may be disabling a legacy name
		return nameOrKey, nil
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", fmt.Errorf("ambiguous name %q — candidates: %s  (pass --source and --plugin to disambiguate)", nameOrKey, strings.Join(candidates, ", "))
}

// --- move ------------------------------------------------------------------

func runMCPMove(name, target string) error {
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
	// Locate the current config (stash first → local → user).
	var cfg any
	var found bool
	if v, ok := stash.Get(name); ok {
		cfg, found = v, true
	}
	if v, ok := cj.ProjectMCPs(proj)[name]; ok {
		cfg, found = v, true
	}
	if v, ok := cj.UserMCPs()[name]; ok {
		cfg, found = v, true
	}
	if !found {
		return fmt.Errorf("%q not found in stash, local, or user scope", name)
	}

	// Delete from every mutable scope that's NOT the target.
	var removed []string
	if _, ok := stash.Get(name); ok && target != "stash" {
		stash.Delete(name)
		removed = append(removed, "stash")
	}
	if _, ok := cj.ProjectMCPs(proj)[name]; ok && target != "project" {
		cj.DeleteProjectMCP(proj, name)
		removed = append(removed, "local")
	}
	if _, ok := cj.UserMCPs()[name]; ok && target != "user" {
		cj.DeleteUserMCP(name)
		removed = append(removed, "user")
	}
	// Write into target.
	switch target {
	case "user":
		cj.SetUserMCP(name, cfg)
	case "project":
		cj.SetProjectMCP(proj, name, cfg)
	case "stash":
		stash.Put(name, cfg)
	}

	if flagDryRun {
		fmt.Printf("[dry-run] would move %s from [%s] to %s\n", name, strings.Join(removed, "+"), displayScope(target))
		return nil
	}
	if err := backupAndSave(p, cj); err != nil {
		return err
	}
	if err := stash.Save(); err != nil {
		return err
	}
	fromStr := "(nowhere)"
	if len(removed) > 0 {
		fromStr = strings.Join(removed, "+")
	}
	fmt.Printf("moved %s: %s → %s\n", name, fromStr, displayScope(target))
	return nil
}

// displayScope converts an internal scope key to the name the user would recognize.
func displayScope(s string) string {
	switch s {
	case "project":
		return "local"
	case "mcpjson":
		return "project-shared"
	default:
		return s
	}
}

// --- helpers ---------------------------------------------------------------

func findMCPConfig(name string, cj *config.ClaudeJSON, stash *config.Stash, proj string) (any, bool) {
	if mcpFromStash {
		return stash.Get(name)
	}
	if v, ok := stash.Get(name); ok {
		return v, true
	}
	if v, ok := cj.UserMCPs()[name]; ok {
		return v, true
	}
	if v, ok := cj.ProjectMCPs(proj)[name]; ok {
		return v, true
	}
	return nil, false
}

func uniqueAppend(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func removeString(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func backupAndSave(p paths.Paths, cj *config.ClaudeJSON) error {
	if err := config.Backup(cj.Path, p.BackupsDir); err != nil {
		return err
	}
	return cj.Save()
}

func init() {
	rootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcpListCmd, mcpEnableCmd, mcpDisableCmd, mcpStashCmd, mcpRestoreCmd, mcpMoveCmd, mcpOverrideCmd)

	for _, c := range []*cobra.Command{mcpListCmd, mcpEnableCmd, mcpDisableCmd} {
		c.Flags().StringVar(&mcpScope, "scope", "", "scope: user|local|project|stash|all  (aliases: project→local legacy, mcpjson→project)")
	}
	mcpEnableCmd.Flags().BoolVar(&mcpFromStash, "from-stash", false, "require source is the stash (removes from stash on enable)")
	mcpDisableCmd.Flags().BoolVar(&mcpToStash, "to-stash", false, "move the removed config into the stash instead of deleting it")
	mcpDisableCmd.Flags().BoolVar(&mcpAll, "all", false, "disable every MCP in the chosen scope")
	mcpMoveCmd.Flags().StringVar(&mcpMoveTo, "to", "", "target scope: user|local|stash")
	_ = mcpMoveCmd.MarkFlagRequired("to")

	mcpOverrideCmd.Flags().BoolVar(&overrideUndo, "undo", false, "remove the override (re-enable for this project)")
	mcpOverrideCmd.Flags().StringVar(&overrideSource, "source", "", "source hint: stdio|plugin|claudeai  (default: auto-resolve)")
	mcpOverrideCmd.Flags().StringVar(&overridePluginOf, "plugin", "", "plugin name (required when --source plugin and name is ambiguous)")
}
