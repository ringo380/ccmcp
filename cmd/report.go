package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/classify"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/report"
	"github.com/ringo380/ccmcp/internal/skills"
)

var reportFormat string // --format json|md|csv

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate reports about MCP, plugin, skill, and agent state",
}

// ── snapshot ──────────────────────────────────────────────────────────────────

var reportSnapshotOut string

var reportSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Capture current state as a point-in-time snapshot",
	Long: `Captures the full ccmcp state for the current project and renders it as JSON,
Markdown, or CSV. Useful for archiving, diffing, or sharing current config.

Use --out to write to a file (required for drift baseline); defaults to stdout.
JSON snapshots can later be used as the baseline for 'ccmcp report drift --from <file>'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := buildSnapshot()
		if err != nil {
			return err
		}

		w := os.Stdout
		if reportSnapshotOut != "" {
			f, err := os.Create(reportSnapshotOut)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer f.Close()
			w = f
		}

		switch strings.ToLower(reportFormat) {
		case "md", "markdown":
			return report.WriteSnapshotMD(snap, w)
		case "csv":
			return report.WriteSnapshotCSV(snap, w)
		default: // json
			return report.WriteJSON(snap, w)
		}
	},
}

// ── sweep ─────────────────────────────────────────────────────────────────────

var sweepBase string

var reportSweepCmd = &cobra.Command{
	Use:   "sweep",
	Short: "Summarise all known projects in one table",
	Long: `Reads every project entry in ~/.claude.json and emits a summary row per
project, showing MCP counts, plugin activity, skill/agent totals, and conflicts.
Optionally filter to projects whose path starts with --base.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		cj, err := config.LoadClaudeJSON(p.ClaudeJSON)
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

		pluginMCPs := config.ScanEnabledPluginMCPs(settings, installed, p.PluginsDir)
		pluginsActive, pluginsTotal := 0, 0
		for _, e := range settings.PluginEntries() {
			pluginsTotal++
			if e.Enabled {
				pluginsActive++
			}
		}

		// Collect project paths
		projectPaths := cj.ProjectPaths()
		if sweepBase != "" {
			filtered := projectPaths[:0]
			for _, pp := range projectPaths {
				if strings.HasPrefix(pp, sweepBase) {
					filtered = append(filtered, pp)
				}
			}
			projectPaths = filtered
		}
		sort.Strings(projectPaths)

		sr := report.SweepReport{GeneratedAt: time.Now()}

		for _, proj := range projectPaths {
			localMCPs := cj.ProjectMCPNames(proj)
			overrides := cj.ProjectDisabledMcpServers(proj)
			dSkills := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
			dAgents := agents.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
			dCmds := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
			conflicts := commands.FindConflicts(dCmds, dSkills)

			skillEnabled := 0
			for _, s := range dSkills {
				if s.Enabled {
					skillEnabled++
				}
			}
			_ = pluginMCPs

			sr.Rows = append(sr.Rows, report.SweepRow{
				ProjectPath:   proj,
				UserMCPs:      len(cj.UserMCPNames()),
				LocalMCPs:     len(localMCPs),
				Overrides:     len(overrides),
				PluginsActive: pluginsActive,
				PluginsTotal:  pluginsTotal,
				SkillsEnabled: skillEnabled,
				SkillsTotal:   len(dSkills),
				AgentsTotal:   len(dAgents),
				CommandsTotal: len(dCmds),
				Conflicts:     len(conflicts),
			})
		}

		switch strings.ToLower(reportFormat) {
		case "md", "markdown":
			return report.WriteSweepMD(sr, os.Stdout)
		case "csv":
			return report.WriteSweepCSV(sr, os.Stdout)
		default:
			return report.WriteJSON(sr, os.Stdout)
		}
	},
}

// ── drift ─────────────────────────────────────────────────────────────────────

var driftFrom string

var reportDriftCmd = &cobra.Command{
	Use:   "drift --from <snapshot.json>",
	Short: "Compare current state to a saved snapshot",
	Long: `Loads a previously saved JSON snapshot and compares it against the current
state. Shows MCPs added/removed, override changes, and new/resolved command conflicts.

Produce a baseline with: ccmcp report snapshot --out baseline.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if driftFrom == "" {
			return fmt.Errorf("--from <snapshot.json> is required")
		}

		raw, err := os.ReadFile(driftFrom)
		if err != nil {
			return fmt.Errorf("read baseline: %w", err)
		}
		var baseline report.Snapshot
		if err := json.Unmarshal(raw, &baseline); err != nil {
			return fmt.Errorf("parse baseline: %w", err)
		}

		current, err := buildSnapshot()
		if err != nil {
			return err
		}

		// Diff MCPs across all scope buckets
		oldAllMCPs := uniqueWithScope(baseline.UserMCPs, "user", baseline.LocalMCPs, "local",
			baseline.PluginSourced, "plugin", baseline.ClaudeAi, "claudeai")
		newAllMCPs := uniqueWithScope(current.UserMCPs, "user", current.LocalMCPs, "local",
			current.PluginSourced, "plugin", current.ClaudeAi, "claudeai")

		oldNames := make([]string, 0, len(oldAllMCPs))
		newNames := make([]string, 0, len(newAllMCPs))
		for k := range oldAllMCPs {
			oldNames = append(oldNames, k)
		}
		for k := range newAllMCPs {
			newNames = append(newNames, k)
		}

		addedNames, removedNames := report.DiffStrings(oldNames, newNames)
		addedMCPs := make([]report.DriftItem, 0, len(addedNames))
		removedMCPs := make([]report.DriftItem, 0, len(removedNames))
		for _, n := range addedNames {
			addedMCPs = append(addedMCPs, report.DriftItem{Name: n, Scope: newAllMCPs[n]})
		}
		for _, n := range removedNames {
			removedMCPs = append(removedMCPs, report.DriftItem{Name: n, Scope: oldAllMCPs[n]})
		}

		ovAdded, ovRemoved := report.DiffStrings(baseline.Overrides, current.Overrides)
		conflAdded, conflRemoved := report.DiffConflicts(baseline.Conflicts, current.Conflicts)

		d := report.DriftReport{
			GeneratedAt:      time.Now(),
			ProjectPath:      current.ProjectPath,
			Before:           baseline,
			MCPsAdded:        addedMCPs,
			MCPsRemoved:      removedMCPs,
			OverridesAdded:   ovAdded,
			OverridesRemoved: ovRemoved,
			ConflictsAdded:   conflAdded,
			ConflictsRemoved: conflRemoved,
		}

		switch strings.ToLower(reportFormat) {
		case "md", "markdown":
			return report.WriteDriftMD(d, os.Stdout)
		case "csv":
			return report.WriteJSON(d, os.Stdout) // drift is too structured for flat CSV; fall back to JSON
		default:
			return report.WriteJSON(d, os.Stdout)
		}
	},
}

// ── audit ─────────────────────────────────────────────────────────────────────

var reportAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Find stale overrides, command conflicts, and redundant MCPs",
	Long: `Inspects the current project for issues:
  - Stale entries in disabledMcpServers (orphan overrides for MCPs that no longer exist)
  - Slash command conflicts (multiple sources define the same /command)
  - Redundant MCPs (same server defined in multiple scopes simultaneously)

Use --format md or --format csv for machine-readable output.`,
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
		settings, err := config.LoadSettings(p.SettingsJSON)
		if err != nil {
			return err
		}
		installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
		if err != nil {
			return err
		}

		allPluginMCPs := config.ScanAllInstalledPluginMCPs(settings, installed, p.PluginsDir)
		overrides := cj.ProjectDisabledMcpServers(proj)
		userMCPs := cj.UserMCPNames()
		localMCPs := cj.ProjectMCPNames(proj)
		claudeAi := cj.ClaudeAiEverConnected()

		// Stale override classification
		cl := classify.Classify(overrides, userMCPs, localMCPs, claudeAi, stash.Names(), allPluginMCPs)
		var staleItems []report.AuditItem
		for _, k := range cl.OrphanPlugin {
			staleItems = append(staleItems, report.AuditItem{
				Key:    k,
				Kind:   "OrphanPlugin",
				Reason: "plugin not installed",
			})
		}
		for _, k := range cl.OrphanStdio {
			staleItems = append(staleItems, report.AuditItem{
				Key:    k,
				Kind:   "OrphanStdio",
				Reason: "no matching MCP source found",
			})
		}

		// Command conflicts
		dSkills := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		dCmds := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
		conflicts := commands.FindConflicts(dCmds, dSkills)

		// Redundancies: same MCP name in both user and local scope
		userSet := map[string]bool{}
		for _, n := range userMCPs {
			userSet[n] = true
		}
		var redundancies []report.RedundancyItem
		for _, n := range localMCPs {
			if userSet[n] {
				redundancies = append(redundancies, report.RedundancyItem{
					Name:   n,
					Scopes: []string{"user", "local"},
				})
			}
		}

		a := report.AuditReport{
			GeneratedAt:    time.Now(),
			ProjectPath:    proj,
			StaleOverrides: staleItems,
			Conflicts:      conflicts,
			Redundancies:   redundancies,
		}

		switch strings.ToLower(reportFormat) {
		case "md", "markdown":
			return report.WriteAuditMD(a, os.Stdout)
		case "csv":
			return report.WriteAuditCSV(a, os.Stdout)
		default:
			return report.WriteJSON(a, os.Stdout)
		}
	},
}

// ── shared helpers ─────────────────────────────────────────────────────────────

// buildSnapshot collects the full state for the current project.
func buildSnapshot() (report.Snapshot, error) {
	p, err := resolvePaths()
	if err != nil {
		return report.Snapshot{}, err
	}
	proj, err := projectPath()
	if err != nil {
		return report.Snapshot{}, err
	}

	cj, err := config.LoadClaudeJSON(p.ClaudeJSON)
	if err != nil {
		return report.Snapshot{}, err
	}
	stash, err := config.LoadStash(p.Stash)
	if err != nil {
		return report.Snapshot{}, err
	}
	settings, err := config.LoadSettings(p.SettingsJSON)
	if err != nil {
		return report.Snapshot{}, err
	}
	installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
	if err != nil {
		return report.Snapshot{}, err
	}

	pluginMCPs := config.ScanEnabledPluginMCPs(settings, installed, p.PluginsDir)

	var mcpjsonShared []string
	if mcpj, err := config.LoadMCPJson(proj + "/.mcp.json"); err == nil && mcpj != nil {
		mcpjsonShared = mcpj.Names()
	}

	dSkills := skills.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
	dAgents := agents.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
	dCmds := commands.Discover(p.ClaudeConfigDir, proj, settings, installed, p.PluginsDir)
	conflicts := commands.FindConflicts(dCmds, dSkills)

	skillEnabled := 0
	for _, s := range dSkills {
		if s.Enabled {
			skillEnabled++
		}
	}

	pluginsActive, pluginsTotal := 0, 0
	for _, e := range settings.PluginEntries() {
		pluginsTotal++
		if e.Enabled {
			pluginsActive++
		}
	}

	return report.Snapshot{
		GeneratedAt:   time.Now(),
		ProjectPath:   proj,
		UserMCPs:      cj.UserMCPNames(),
		LocalMCPs:     cj.ProjectMCPNames(proj),
		McpjsonShared: mcpjsonShared,
		PluginSourced: config.SortedPluginSources(pluginMCPs),
		ClaudeAi:      cj.ClaudeAiEverConnected(),
		Overrides:     cj.ProjectDisabledMcpServers(proj),
		Stashed:       stash.Names(),
		PluginsActive: pluginsActive,
		PluginsTotal:  pluginsTotal,
		SkillsEnabled: skillEnabled,
		SkillsTotal:   len(dSkills),
		AgentsTotal:   len(dAgents),
		CommandsTotal: len(dCmds),
		Conflicts:     conflicts,
	}, nil
}

// uniqueWithScope merges paired ([]string, scope) lists into a name→scope map.
// First scope wins for duplicates.
func uniqueWithScope(pairs ...any) map[string]string {
	out := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		names, _ := pairs[i].([]string)
		scope, _ := pairs[i+1].(string)
		for _, n := range names {
			if _, exists := out[n]; !exists {
				out[n] = scope
			}
		}
	}
	return out
}

func init() {
	rootCmd.AddCommand(reportCmd)
	reportCmd.PersistentFlags().StringVar(&reportFormat, "format", "json", "output format: json|md|csv")

	// snapshot
	reportCmd.AddCommand(reportSnapshotCmd)
	reportSnapshotCmd.Flags().StringVar(&reportSnapshotOut, "out", "", "write output to this file (default: stdout)")

	// sweep
	reportCmd.AddCommand(reportSweepCmd)
	reportSweepCmd.Flags().StringVar(&sweepBase, "base", "", "only include projects whose path starts with this prefix")

	// drift
	reportCmd.AddCommand(reportDriftCmd)
	reportDriftCmd.Flags().StringVar(&driftFrom, "from", "", "path to a JSON snapshot produced by 'ccmcp report snapshot'")

	// audit
	reportCmd.AddCommand(reportAuditCmd)
}
