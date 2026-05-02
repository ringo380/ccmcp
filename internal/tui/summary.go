package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/classify"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/skills"
	"github.com/ringo380/ccmcp/internal/stringslice"
)

// summaryView is the "Summary" tab: bird's-eye overview of every scope, plus
// a redundancies section that flags duplicated MCPs, installed-but-disabled
// plugins, and other inconsistencies worth knowing about.
type summaryView struct {
	st   *state
	w, h int
	top  int

	pendingPrune bool
	flash        string
}

func newSummaryView(st *state) *summaryView {
	return &summaryView{st: st}
}

func (v *summaryView) update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	// Clear pending prune on any key other than 'p'.
	if v.pendingPrune && key.String() != "p" {
		v.pendingPrune = false
	}
	switch key.String() {
	case "up", "k":
		if v.top > 0 {
			v.top--
		}
	case "down", "j":
		v.top++
	case "pgup":
		v.top -= 10
		if v.top < 0 {
			v.top = 0
		}
	case "pgdn":
		v.top += 10
	case "g", "home":
		v.top = 0
	case "p":
		if v.pendingPrune {
			v.doPrune()
		} else {
			v.pendingPrune = true
			v.flash = styleWarn.Render("press 'p' again to prune orphaned overrides, any other key cancels")
		}
	}
	return nil
}

func (v *summaryView) doPrune() {
	v.pendingPrune = false
	overrides := v.st.cj.ProjectDisabledMcpServers(v.st.project)
	if len(overrides) == 0 {
		v.flash = styleDim.Render("no overrides to prune")
		return
	}
	userMCPs := v.st.cj.UserMCPNames()
	projMCPs := v.st.cj.ProjectMCPNames(v.st.project)
	claudeAi := v.st.cj.ClaudeAiEverConnected()
	var stashNames []string
	if v.st.stash != nil {
		stashNames = v.st.stash.Names()
	}
	cls := classify.Classify(overrides, userMCPs, projMCPs, claudeAi, stashNames, v.st.pluginMCPs)
	toRemove := append(cls.OrphanPlugin, cls.OrphanStdio...)
	if len(toRemove) == 0 {
		v.flash = styleDim.Render("no orphaned overrides found")
		return
	}
	remaining := overrides
	for _, k := range toRemove {
		remaining = stringslice.Remove(remaining, k)
	}
	v.st.cj.SetProjectDisabledMcpServers(v.st.project, remaining)
	if err := config.Backup(v.st.cj.Path, v.st.paths.BackupsDir); err != nil {
		v.flash = styleErr.Render("backup: " + err.Error())
		return
	}
	if err := v.st.cj.Save(); err != nil {
		v.flash = styleErr.Render("save: " + err.Error())
		return
	}
	v.flash = styleOK.Render(fmt.Sprintf("pruned %d orphaned entr%s", len(toRemove), classify.PluralY(len(toRemove))))
}

func (v *summaryView) render() string {
	var b strings.Builder

	// --- Updates available (top-of-page so it's hard to miss) ----------
	if v.st.updates != nil {
		mkt, plg, mcp := v.st.updates.CountOutdated()
		if mkt+plg+mcp > 0 {
			b.WriteString(styleTitle.Render("Updates available") + "\n")
			parts := []string{}
			if plg > 0 {
				parts = append(parts, fmt.Sprintf("%d plugin(s)", plg))
			}
			if mkt > 0 {
				parts = append(parts, fmt.Sprintf("%d marketplace(s)", mkt))
			}
			if mcp > 0 {
				parts = append(parts, fmt.Sprintf("%d MCP launcher(s)", mcp))
			}
			b.WriteString("  " + styleWarn.Render("↑ "+strings.Join(parts, ", ")) + "\n\n")
		}
	}

	// --- MCPs -----------------------------------------------------------
	userMCPs := v.st.cj.UserMCPNames()
	projMCPs := v.st.cj.ProjectMCPNames(v.st.project)
	stashed := v.st.stash.Names()
	var mcpjsonNames []string
	if m, err := config.LoadMCPJson(v.st.project + "/.mcp.json"); err == nil {
		mcpjsonNames = m.Names()
	}

	b.WriteString(styleTitle.Render("MCP servers") + "\n")
	row(&b, "  user scope     ", len(userMCPs), truncateList(userMCPs, 6))
	row(&b, "  local scope    ", len(projMCPs), truncateList(projMCPs, 6))
	row(&b, "  .mcp.json      ", len(mcpjsonNames), truncateList(mcpjsonNames, 6))
	// Plugin-registered MCPs: only count those whose plugin is currently ENABLED
	// (v.st.pluginMCPs includes disabled-but-installed plugins too — they don't load).
	pluginSources := make([]string, 0, len(v.st.pluginMCPs))
	for name, srcs := range v.st.pluginMCPs {
		for _, s := range srcs {
			if s.Enabled {
				pluginSources = append(pluginSources, name)
				break
			}
		}
	}
	sort.Strings(pluginSources)
	row(&b, "  via plugins    ", len(pluginSources), truncateList(pluginSources, 6))
	claudeAi := v.st.cj.ClaudeAiEverConnected()
	sort.Strings(claudeAi)
	row(&b, "  claude.ai      ", len(claudeAi), truncateList(claudeAi, 6))
	row(&b, "  stash (parked) ", len(stashed), truncateList(stashed, 6))
	b.WriteString("\n")

	// Per-project overrides
	overrides := v.st.cj.ProjectDisabledMcpServers(v.st.project)
	classified := classify.Classify(overrides, userMCPs, projMCPs, claudeAi, stashed, v.st.pluginMCPs)
	b.WriteString(styleTitle.Render("Per-project overrides (disabledMcpServers)") + "\n")
	if len(overrides) == 0 {
		b.WriteString(styleDim.Render("  (none for " + v.st.project + ")") + "\n\n")
	} else {
		row(&b, "  plugin (active)    ", len(classified.PluginActive), truncateList(classified.PluginActive, 4))
		row(&b, "  plugin (disabled)  ", len(classified.PluginDisabled), truncateList(classified.PluginDisabled, 4))
		row(&b, "  claude.ai          ", len(classified.ClaudeAi), truncateList(classified.ClaudeAi, 4))
		row(&b, "  stdio (live)       ", len(classified.StdioLive), truncateList(classified.StdioLive, 4))
		row(&b, "  stash ghost        ", len(classified.StashGhost), truncateList(classified.StashGhost, 4))
		row(&b, "  orphan (plugin)    ", len(classified.OrphanPlugin), truncateList(classified.OrphanPlugin, 4))
		row(&b, "  orphan (stdio)     ", len(classified.OrphanStdio), truncateList(classified.OrphanStdio, 4))
		b.WriteString("\n")

		// Cleanup suggestions. Bucket 2 (plugin-disabled) is deliberately NOT pruneable
		// because re-enabling the plugin would re-activate the MCP and the user probably
		// wanted it off per-project. Stash ghosts are optional (safe but not always wanted).
		recoverable := len(classified.OrphanPlugin) + len(classified.OrphanStdio)
		withStash := recoverable + len(classified.StashGhost)
		if recoverable > 0 || len(classified.StashGhost) > 0 {
			b.WriteString(styleTitle.Render("Cleanup suggestions") + "\n")
			if recoverable > 0 {
				fmt.Fprintf(&b, "  %s  run  %s  to remove %d orphaned entr%s\n",
					styleOK.Render("•"),
					styleBadge.Render("ccmcp mcp prune"),
					recoverable,
					classify.PluralY(recoverable))
			}
			if len(classified.StashGhost) > 0 {
				fmt.Fprintf(&b, "  %s  add --include-stash-ghosts to also remove %d stash-ghost entr%s (total %d)\n",
					styleDim.Render("•"),
					len(classified.StashGhost),
					classify.PluralY(len(classified.StashGhost)),
					withStash)
			}
			b.WriteString("\n")
		}
	}

	// --- Plugins --------------------------------------------------------
	var enabled, disabled, unknown, installedOnly int
	installedIdx := map[string]config.InstalledPlugin{}
	for _, ip := range v.st.installed.List() {
		installedIdx[ip.ID] = ip
	}
	knownIDs := map[string]bool{}
	for _, e := range v.st.settings.PluginEntries() {
		knownIDs[e.ID] = true
		if e.Enabled {
			enabled++
		} else {
			disabled++
		}
		if _, ok := installedIdx[e.ID]; !ok {
			unknown++
		}
	}
	for id := range installedIdx {
		if !knownIDs[id] {
			installedOnly++
		}
	}
	b.WriteString(styleTitle.Render("Plugins") + "\n")
	fmt.Fprintf(&b, "  enabled               %d\n", enabled)
	fmt.Fprintf(&b, "  disabled (installed)  %d\n", disabled)
	fmt.Fprintf(&b, "  enabled but not installed   %s\n", warnNum(unknown))
	fmt.Fprintf(&b, "  installed but not in settings %s\n", warnNum(installedOnly))
	b.WriteString("\n")

	// --- Marketplaces ---------------------------------------------------
	extras := v.st.settings.ExtraMarketplaces()
	known, _ := config.LoadKnownMarketplaces(v.st.paths.KnownMarkets)
	var knownNames []string
	if known != nil {
		knownNames = known.Names()
	}
	b.WriteString(styleTitle.Render("Marketplaces") + "\n")
	fmt.Fprintf(&b, "  system-known   %d  (%s)\n", len(knownNames), styleDim.Render(strings.Join(knownNames, ", ")))
	fmt.Fprintf(&b, "  extras         %d\n", len(extras))
	b.WriteString("\n")

	// --- Profiles -------------------------------------------------------
	names := v.st.profiles.Names()
	b.WriteString(styleTitle.Render("Profiles") + "\n")
	fmt.Fprintf(&b, "  saved          %d  (%s)\n", len(names), styleDim.Render(strings.Join(names, ", ")))
	b.WriteString("\n")

	// --- Skills / Agents / Commands -------------------------------------
	discoveredSkills := skills.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	discoveredAgents := agents.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	discoveredCmds := commands.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	conflicts := commands.FindConflicts(discoveredCmds, discoveredSkills)
	var skillEnabled, skillPlugin, skillUser int
	for _, s := range discoveredSkills {
		if s.Enabled {
			skillEnabled++
		}
		switch s.Scope {
		case skills.ScopePlugin:
			skillPlugin++
		case skills.ScopeUser:
			skillUser++
		}
	}
	var agentPlugin, agentUser int
	for _, a := range discoveredAgents {
		switch a.Scope {
		case agents.ScopePlugin:
			agentPlugin++
		case agents.ScopeUser:
			agentUser++
		}
	}
	var cmdPlugin, cmdUser int
	for _, c := range discoveredCmds {
		switch c.Scope {
		case commands.ScopePlugin:
			cmdPlugin++
		case commands.ScopeUser:
			cmdUser++
		}
	}
	b.WriteString(styleTitle.Render("Skills / Agents / Commands") + "\n")
	fmt.Fprintf(&b, "  skills     %d enabled / %d total  (user %d, plugin %d)\n", skillEnabled, len(discoveredSkills), skillUser, skillPlugin)
	fmt.Fprintf(&b, "  agents     %d total  (user %d, plugin %d)\n", len(discoveredAgents), agentUser, agentPlugin)
	fmt.Fprintf(&b, "  commands   %d total  (user %d, plugin %d)\n", len(discoveredCmds), cmdUser, cmdPlugin)
	if len(conflicts) > 0 {
		fmt.Fprintf(&b, "  %s  %d slash-command conflict(s)\n", styleWarn.Render("⚠"), len(conflicts))
		for i, c := range conflicts {
			if i >= 3 {
				fmt.Fprintf(&b, "      %s and %d more\n", styleDim.Render("…"), len(conflicts)-3)
				break
			}
			fmt.Fprintf(&b, "      %s  /%s\n", styleDim.Render(string(c.Kind)), c.Effective)
		}
	}
	b.WriteString("\n")

	// --- Redundancies / warnings ---------------------------------------
	var warnings []string
	// same MCP enabled in both user + project (double-loads)
	projSet := map[string]bool{}
	for _, n := range projMCPs {
		projSet[n] = true
	}
	var dup []string
	for _, n := range userMCPs {
		if projSet[n] {
			dup = append(dup, n)
		}
	}
	sort.Strings(dup)
	if len(dup) > 0 {
		warnings = append(warnings, fmt.Sprintf("MCPs active in BOTH user and project scope (will load twice): %s", strings.Join(dup, ", ")))
	}
	// stash entry also active in user scope -> the user forgot to clean up one side
	var stashAndUser []string
	userSet := map[string]bool{}
	for _, n := range userMCPs {
		userSet[n] = true
	}
	for _, n := range stashed {
		if userSet[n] {
			stashAndUser = append(stashAndUser, n)
		}
	}
	if len(stashAndUser) > 0 {
		warnings = append(warnings, fmt.Sprintf("MCPs in BOTH stash and user scope (stash is redundant): %s", strings.Join(stashAndUser, ", ")))
	}
	// Redundancy checks below care about plugins that ACTUALLY load, not
	// installed-but-disabled ones.
	enabledPluginMCPs := map[string]bool{}
	for name, srcs := range v.st.pluginMCPs {
		for _, s := range srcs {
			if s.Enabled {
				enabledPluginMCPs[name] = true
				break
			}
		}
	}
	// Stashed MCPs also registered by an enabled plugin: the stash is useless (plugin provides it)
	var stashedButPluginProvides []string
	for _, n := range stashed {
		if enabledPluginMCPs[n] {
			stashedButPluginProvides = append(stashedButPluginProvides, n)
		}
	}
	if len(stashedButPluginProvides) > 0 {
		warnings = append(warnings, fmt.Sprintf("MCPs in stash that are ALSO provided by an enabled plugin (stash entry is redundant): %s", strings.Join(stashedButPluginProvides, ", ")))
	}
	// user-scope duplicating a plugin-sourced MCP: two copies will try to load
	var userDupPlugin []string
	for _, n := range userMCPs {
		if enabledPluginMCPs[n] {
			userDupPlugin = append(userDupPlugin, n)
		}
	}
	if len(userDupPlugin) > 0 {
		warnings = append(warnings, fmt.Sprintf("user-scope MCPs also registered by plugin (duplicate load): %s", strings.Join(userDupPlugin, ", ")))
	}
	// mcp.json allow-list includes names not actually in .mcp.json
	if len(mcpjsonNames) > 0 {
		mcpjsonSet := map[string]bool{}
		for _, n := range mcpjsonNames {
			mcpjsonSet[n] = true
		}
		var stale []string
		for _, n := range v.st.cj.ProjectMcpjsonEnabled(v.st.project) {
			if !mcpjsonSet[n] {
				stale = append(stale, n)
			}
		}
		for _, n := range v.st.cj.ProjectMcpjsonDisabled(v.st.project) {
			if !mcpjsonSet[n] {
				stale = append(stale, n)
			}
		}
		if len(stale) > 0 {
			warnings = append(warnings, fmt.Sprintf(".mcp.json allow/deny references missing servers: %s", strings.Join(stale, ", ")))
		}
	}
	if unknown > 0 {
		warnings = append(warnings, fmt.Sprintf("%d plugin(s) enabled in settings but not installed on disk — Claude Code will warn on load", unknown))
	}
	if installedOnly > 0 {
		warnings = append(warnings, fmt.Sprintf("%d plugin(s) on disk but not registered in settings — try `plugin install <id> --marketplace <m>`", installedOnly))
	}

	if len(warnings) == 0 {
		b.WriteString(styleOK.Render("Redundancies: (none — everything looks clean)"))
	} else {
		b.WriteString(styleWarn.Render("Redundancies:") + "\n")
		for _, w := range warnings {
			b.WriteString("  • " + w + "\n")
		}
	}

	// scroll
	lines := strings.Split(b.String(), "\n")
	maxH := v.h - 2
	if maxH < 5 {
		maxH = 5
	}
	if v.top > len(lines)-maxH {
		v.top = len(lines) - maxH
	}
	if v.top < 0 {
		v.top = 0
	}
	end := v.top + maxH
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[v.top:end], "\n")
}

func (v *summaryView) resize(w, h int) { v.w, v.h = w, h }

func (v *summaryView) helpText() string {
	return "j/k: scroll  g: top  p: prune orphans"
}

func (v *summaryView) capturingInput() bool { return false }

// --- helpers ---------------------------------------------------------------

func row(b *strings.Builder, label string, count int, sample string) {
	fmt.Fprintf(b, "%s %3d  %s\n", label, count, styleDim.Render(sample))
}

func truncateList(ss []string, max int) string {
	if len(ss) == 0 {
		return "(none)"
	}
	if len(ss) <= max {
		return strings.Join(ss, ", ")
	}
	return strings.Join(ss[:max], ", ") + fmt.Sprintf(", … +%d more", len(ss)-max)
}

func warnNum(n int) string {
	if n == 0 {
		return styleDim.Render("0")
	}
	return styleWarn.Render(fmt.Sprintf("%d", n))
}
