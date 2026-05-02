package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/updates"
	"github.com/spf13/cobra"
)

var pluginCmd = &cobra.Command{
	Use:     "plugin",
	Aliases: []string{"plugins"},
	Short:   "Manage Claude Code plugins (enable, disable, install, remove)",
}

var (
	pluginFilterEnabled  bool
	pluginFilterDisabled bool
	pluginPurge          bool
	pluginMarketplace    string
)

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known plugins and their enabled state",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
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

		installedIndex := map[string]config.InstalledPlugin{}
		for _, ip := range installed.List() {
			installedIndex[ip.ID] = ip
		}

		// Merge keys from enabledPlugins + installed_plugins so we show both
		// "in settings but not installed" and "installed but no settings entry" cases.
		seen := map[string]bool{}
		var rows []pluginRow
		for _, e := range settings.PluginEntries() {
			seen[e.ID] = true
			ip := installedIndex[e.ID]
			rows = append(rows, pluginRow{ID: e.ID, Enabled: e.Enabled, Known: true, Installed: ip.InstallPath != "", Version: ip.Version})
		}
		for _, ip := range installed.List() {
			if seen[ip.ID] {
				continue
			}
			rows = append(rows, pluginRow{ID: ip.ID, Enabled: false, Known: false, Installed: true, Version: ip.Version})
		}

		// Filters
		var filtered []pluginRow
		for _, r := range rows {
			switch {
			case pluginFilterEnabled && !r.Enabled:
				continue
			case pluginFilterDisabled && (r.Enabled || !r.Known):
				continue
			}
			filtered = append(filtered, r)
		}

		if len(filtered) == 0 {
			fmt.Println("(no plugins match)")
			return nil
		}
		var enabled, disabled, notInSettings int
		for _, r := range filtered {
			mark := "[ ]"
			switch {
			case r.Enabled:
				mark = "[x]"
				enabled++
			case !r.Known:
				mark = "[?]"
				notInSettings++
			default:
				disabled++
			}
			ver := ""
			if r.Version != "" {
				ver = " v" + r.Version
			}
			fmt.Printf("  %s %s%s\n", mark, r.ID, ver)
		}
		fmt.Printf("\n%d enabled, %d disabled, %d installed-but-unknown\n", enabled, disabled, notInSettings)
		return nil
	},
}

type pluginRow struct {
	ID        string
	Enabled   bool
	Known     bool // appears in enabledPlugins
	Installed bool
	Version   string
}

var pluginEnableCmd = &cobra.Command{
	Use:   "enable <id>[@marketplace] [<id>...]",
	Short: "Enable plugin(s) (flips enabledPlugins boolean to true)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  pluginSetEnabled(true),
}

var pluginDisableCmd = &cobra.Command{
	Use:   "disable <id>[@marketplace] [<id>...]",
	Short: "Disable plugin(s) without uninstalling (flips enabledPlugins boolean to false)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  pluginSetEnabled(false),
}

func pluginSetEnabled(enabled bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
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

		var applied []string
		for _, raw := range args {
			id, ambiguous := resolveQualifiedID(raw, settings, installed)
			if id == "" {
				if len(ambiguous) > 0 {
					return fmt.Errorf("%q is ambiguous; pick one of: %s", raw, strings.Join(ambiguous, ", "))
				}
				return fmt.Errorf("plugin %q not found in settings or installed_plugins (try --marketplace)", raw)
			}
			settings.SetPluginEnabled(id, enabled)
			applied = append(applied, id)
		}
		if flagDryRun {
			fmt.Printf("[dry-run] would set enabled=%v: %s\n", enabled, strings.Join(applied, ", "))
			return nil
		}
		if err := config.Backup(settings.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := settings.Save(); err != nil {
			return err
		}
		verb := "disabled"
		if enabled {
			verb = "enabled"
		}
		fmt.Printf("%s: %s\n", verb, strings.Join(applied, ", "))
		return nil
	}
}

var pluginRegisterOnly bool

var pluginInstallCmd = &cobra.Command{
	Use:   "install <id> --marketplace <m>",
	Short: "Install a plugin: fetch source code and register it with Claude Code",
	Long: `Fetch a plugin's source code from its marketplace into
~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/, then enable it in
~/.claude/settings.json.

Supported marketplace source types: bare-string path, url, git-subdir, github.

The marketplace manifest must already be present at
~/.claude/plugins/marketplaces/<m>/.claude-plugin/marketplace.json — use Claude Code's
/plugin menu or clone the marketplace repo manually before running this.

Pass --register-only to skip the fetch and just wire up enabledPlugins + installed_plugins.json
(useful when the cache dir already exists).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		id := args[0]
		if pluginMarketplace != "" {
			id = config.QualifyPluginID(id, pluginMarketplace)
		}
		if !strings.Contains(id, "@") {
			return fmt.Errorf("%q is unqualified; pass --marketplace or use name@marketplace", id)
		}
		name, mkt := config.ParsePluginID(id)

		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
		if err != nil {
			return err
		}

		var result *install.Result
		if pluginRegisterOnly {
			result = &install.Result{QualifiedID: id, Version: "unknown"}
			fmt.Printf("registering %s (no fetch)\n", id)
		} else {
			if flagDryRun {
				fmt.Printf("[dry-run] would fetch %s from marketplace %s\n", name, mkt)
				return nil
			}
			fmt.Printf("fetching %s from marketplace %s…\n", name, mkt)
			result, err = install.Install(p, mkt, name)
			if err != nil {
				return err
			}
			fmt.Printf("fetched into %s (sha=%s)\n", result.InstallPath, firstN(result.GitCommitSha, 8))
		}

		install.RegisterInstall(settings, installed, result)

		if flagDryRun {
			fmt.Printf("[dry-run] would register + enable %s\n", id)
			return nil
		}
		if err := config.Backup(settings.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := config.Backup(installed.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := settings.Save(); err != nil {
			return err
		}
		if err := installed.Save(); err != nil {
			return err
		}
		fmt.Printf("registered + enabled %s\n", id)
		if !pluginRegisterOnly {
			fmt.Println("restart Claude Code (or reload the window) to pick up the new plugin.")
		}
		return nil
	},
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var pluginRemoveCmd = &cobra.Command{
	Use:     "remove <id>[@marketplace]",
	Aliases: []string{"uninstall", "rm"},
	Short:   "Remove a plugin from enabledPlugins + installed_plugins (use --purge to delete data dir too)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
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
		id, ambiguous := resolveQualifiedID(args[0], settings, installed)
		if id == "" {
			if len(ambiguous) > 0 {
				return fmt.Errorf("%q is ambiguous; pick one of: %s", args[0], strings.Join(ambiguous, ", "))
			}
			return fmt.Errorf("plugin %q not found", args[0])
		}
		settings.RemovePluginEntry(id)
		installPath, _ := installed.Remove(id)

		if flagDryRun {
			fmt.Printf("[dry-run] would remove %s from settings + installed_plugins", id)
			if pluginPurge && installPath != "" {
				fmt.Printf(" + delete %s", installPath)
			}
			fmt.Println()
			return nil
		}
		if err := config.Backup(settings.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := config.Backup(installed.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := settings.Save(); err != nil {
			return err
		}
		if err := installed.Save(); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", id)
		if pluginPurge && installPath != "" {
			if err := os.RemoveAll(installPath); err != nil {
				fmt.Fprintf(os.Stderr, "warn: could not delete %s: %v\n", installPath, err)
			} else {
				fmt.Printf("deleted %s\n", installPath)
			}
		} else if installPath != "" {
			fmt.Printf("cache preserved at %s (pass --purge to delete)\n", installPath)
		}
		return nil
	},
}

var pluginUpdateAll bool

var pluginUpdateCmd = &cobra.Command{
	Use:   "update [<id>[@marketplace]] | --all",
	Short: "Re-fetch and update installed plugin(s) to their latest version",
	Long: `Re-fetch each plugin's source and update installed_plugins.json.
For bare-string plugins (sourced from a marketplace repo) run
'ccmcp marketplace update' first so the catalog reflects upstream changes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !pluginUpdateAll && len(args) == 0 {
			return fmt.Errorf("specify a plugin id or pass --all")
		}
		p, err := resolvePaths()
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

		var targets []config.InstalledPlugin
		if pluginUpdateAll {
			targets = installed.List()
			if len(targets) == 0 {
				fmt.Println("no plugins installed")
				return nil
			}
		} else {
			id, ambiguous := resolveQualifiedID(args[0], settings, installed)
			if id == "" {
				if len(ambiguous) > 0 {
					return fmt.Errorf("%q is ambiguous; pick one of: %s", args[0], strings.Join(ambiguous, ", "))
				}
				return fmt.Errorf("plugin %q not found in settings or installed_plugins", args[0])
			}
			ip := installed.List()
			var found config.InstalledPlugin
			for _, x := range ip {
				if x.ID == id {
					found = x
					break
				}
			}
			if found.ID == "" {
				return fmt.Errorf("plugin %q is not installed (use 'ccmcp plugin install')", id)
			}
			targets = []config.InstalledPlugin{found}
		}

		var anyChanged bool
		for _, ip := range targets {
			name, mkt := config.ParsePluginID(ip.ID)
			if mkt == "" {
				fmt.Fprintf(os.Stderr, "skip %s: unqualified ID\n", ip.ID)
				continue
			}
			if flagDryRun {
				fmt.Printf("[dry-run] would re-fetch %s from %s\n", name, mkt)
				continue
			}
			result, err := install.Install(p, mkt, name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error updating %s: %v\n", ip.ID, err)
				continue
			}
			if result.GitCommitSha != "" && result.GitCommitSha == ip.GitCommitSha {
				fmt.Printf("already up to date: %s\n", ip.ID)
				continue
			}
			oldSha := firstN(ip.GitCommitSha, 8)
			newSha := firstN(result.GitCommitSha, 8)
			install.UpdateInstall(installed, result, ip.InstallPath)
			anyChanged = true
			fmt.Printf("updated %s: %s → %s\n", ip.ID, oldSha, newSha)
		}

		if flagDryRun || !anyChanged {
			return nil
		}
		if err := config.Backup(installed.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := installed.Save(); err != nil {
			return err
		}
		fmt.Println("restart Claude Code (or reload the window) to pick up updated plugins.")
		return nil
	},
}

// --- marketplace subcommand -----------------------------------------------

func resolveQualifiedID(id string, s *config.Settings, i *config.InstalledPlugins) (string, []string) {
	if strings.Contains(id, "@") {
		return id, nil
	}
	if pluginMarketplace != "" {
		return id + "@" + pluginMarketplace, nil
	}
	return config.ResolvePluginID(id, s, i)
}

// --- marketplace subcommand -----------------------------------------------

var marketplaceCmd = &cobra.Command{
	Use:     "marketplace",
	Aliases: []string{"mkt", "marketplaces"},
	Short:   "Manage plugin marketplaces (extraKnownMarketplaces)",
}

var marketplaceUpdateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Refresh marketplace catalog(s) via git pull",
	Long: `Run 'git pull --ff-only' in each locally-cloned marketplace directory so that
subsequent 'ccmcp plugin update' or 'ccmcp plugin install' calls see the latest
plugin list. With no arguments, updates every cloned marketplace.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		var names []string
		if len(args) > 0 {
			names = args
		} else {
			names, err = install.ListLocalMarketplaces(p)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Println("no locally-cloned marketplaces found")
				return nil
			}
		}
		for _, name := range names {
			if flagDryRun {
				fmt.Printf("[dry-run] would git pull marketplace %s\n", name)
				continue
			}
			if err := install.UpdateMarketplace(p, name); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			fmt.Printf("updated marketplace %s\n", name)
		}
		return nil
	},
}

var marketplaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered marketplaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		known, _ := config.LoadKnownMarketplaces(p.KnownMarkets)

		extras := settings.ExtraMarketplaces()
		if known != nil {
			fmt.Printf("system-known: %s\n", strings.Join(known.Names(), ", "))
		}
		fmt.Println("extras (from settings.json):")
		if len(extras) == 0 {
			fmt.Println("  (none)")
			return nil
		}
		for _, mp := range extras {
			src := mp.SourceType
			if mp.Repo != "" {
				src += " " + mp.Repo
			} else if mp.Path != "" {
				src += " " + mp.Path
			}
			auto := ""
			if mp.AutoUpdate {
				auto = " (autoUpdate)"
			}
			fmt.Printf("  - %s: %s%s\n", mp.Name, src, auto)
		}
		return nil
	},
}

var (
	mktSource     string
	mktRepo       string
	mktLocalPath  string
	mktAutoUpdate bool
	mktPurge      bool
	mktNoClone    bool
)

var marketplaceAddCmd = &cobra.Command{
	Use:   "add <name> --source <github|git|local> [--repo <r>] [--path <p>]",
	Short: "Register a marketplace in ~/.claude/settings.json",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		mp := config.Marketplace{Name: args[0], SourceType: mktSource, Repo: mktRepo, Path: mktLocalPath, AutoUpdate: mktAutoUpdate}
		if flagDryRun {
			fmt.Printf("[dry-run] would add marketplace %s (%s) and clone\n", mp.Name, mp.SourceType)
			return nil
		}
		if mktNoClone {
			if err := settings.AddMarketplace(mp); err != nil {
				return err
			}
		} else {
			if err := install.AddMarketplace(p, settings, mp); err != nil {
				return err
			}
		}
		if err := config.Backup(settings.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := settings.Save(); err != nil {
			return err
		}
		fmt.Printf("added marketplace %s\n", mp.Name)
		return nil
	},
}

var marketplaceRemoveCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Remove a marketplace from extraKnownMarketplaces",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
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
		if flagDryRun {
			fmt.Printf("[dry-run] would remove marketplace %s", args[0])
			if mktPurge {
				fmt.Print(" + delete clone dir")
			}
			fmt.Println()
			return nil
		}
		if err := install.RemoveMarketplace(p, settings, installed, args[0], mktPurge); err != nil {
			return err
		}
		if err := config.Backup(settings.Path, p.BackupsDir); err != nil {
			return err
		}
		if err := settings.Save(); err != nil {
			return err
		}
		fmt.Printf("removed marketplace %s\n", args[0])
		return nil
	},
}

var pluginOutdatedCmd = &cobra.Command{
	Use:   "outdated",
	Short: "List installed plugins with a newer upstream version available",
	Long: `Probes each installed plugin's upstream source (the marketplace HEAD for
bare-string plugins, or the explicit git URL for url/github/git-subdir sources)
and prints rows whose local gitCommitSha differs from upstream.

Network access is required. Plugins whose source can't be reached are reported
with an err note and skipped.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
		if err != nil {
			return err
		}
		list := installed.List()
		if len(list) == 0 {
			fmt.Println("(no plugins installed)")
			return nil
		}
		var outdated, skipped int
		for _, ip := range list {
			_, mkt := config.ParsePluginID(ip.ID)
			head := install.LocalMarketplaceHead(p, mkt)
			s := updates.CheckPlugin(p, ip, head)
			switch {
			case s.Err != nil:
				skipped++
			case s.Outdated:
				outdated++
				fmt.Printf("  ↑ %s  %s → %s\n", ip.ID, firstN(s.Local, 8), firstN(s.Remote, 8))
			}
		}
		if outdated == 0 {
			fmt.Println("all installed plugins are up to date")
		} else {
			fmt.Printf("\n%d outdated, %d skipped\n", outdated, skipped)
		}
		return nil
	},
}

var marketplaceOutdatedCmd = &cobra.Command{
	Use:   "outdated",
	Short: "List cloned marketplaces whose upstream HEAD has advanced",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		names, err := install.ListLocalMarketplaces(p)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Println("(no cloned marketplaces)")
			return nil
		}
		var outdated, skipped int
		for _, n := range names {
			s := updates.CheckMarketplace(p, n)
			switch {
			case s.Err != nil:
				skipped++
			case s.Outdated:
				outdated++
				fmt.Printf("  ↑ %s  %s → %s\n", n, firstN(s.Local, 8), firstN(s.Remote, 8))
			}
		}
		if outdated == 0 {
			fmt.Println("all marketplaces are up to date")
		} else {
			fmt.Printf("\n%d outdated, %d skipped\n", outdated, skipped)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pluginCmd, marketplaceCmd)
	pluginCmd.AddCommand(pluginListCmd, pluginEnableCmd, pluginDisableCmd, pluginInstallCmd, pluginRemoveCmd, pluginUpdateCmd, pluginOutdatedCmd)
	marketplaceCmd.AddCommand(marketplaceListCmd, marketplaceAddCmd, marketplaceRemoveCmd, marketplaceUpdateCmd, marketplaceOutdatedCmd)

	pluginListCmd.Flags().BoolVar(&pluginFilterEnabled, "enabled", false, "show only enabled plugins")
	pluginListCmd.Flags().BoolVar(&pluginFilterDisabled, "disabled", false, "show only disabled plugins")
	pluginRemoveCmd.Flags().BoolVar(&pluginPurge, "purge", false, "also delete the plugin's cache directory")
	pluginInstallCmd.Flags().BoolVar(&pluginRegisterOnly, "register-only", false, "skip fetch, only register bookkeeping")
	pluginUpdateCmd.Flags().BoolVar(&pluginUpdateAll, "all", false, "update all installed plugins")
	for _, c := range []*cobra.Command{pluginEnableCmd, pluginDisableCmd, pluginInstallCmd, pluginRemoveCmd, pluginUpdateCmd} {
		c.Flags().StringVar(&pluginMarketplace, "marketplace", "", "marketplace name (disambiguates bare plugin names)")
	}

	marketplaceAddCmd.Flags().StringVar(&mktSource, "source", "github", "source: github|git|local")
	marketplaceAddCmd.Flags().StringVar(&mktRepo, "repo", "", "repo (owner/name for github, URL for git)")
	marketplaceAddCmd.Flags().StringVar(&mktLocalPath, "path", "", "local filesystem path (for --source local)")
	marketplaceAddCmd.Flags().BoolVar(&mktAutoUpdate, "auto-update", true, "enable auto-update")
	marketplaceAddCmd.Flags().BoolVar(&mktNoClone, "no-clone", false, "register only — skip git clone")
	marketplaceRemoveCmd.Flags().BoolVar(&mktPurge, "purge", false, "also delete the marketplace clone directory")
}
