package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/ringo380/ccmcp/internal/updates"
)

// Run launches the bubbletea TUI.
func Run(p paths.Paths, projectPath string) error {
	st, err := loadState(p, projectPath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	m := newModel(st)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		return err
	}
	return nil
}

// Dump returns the TUI's first render for diagnostic purposes (no TTY, no interaction).
// tab can be "mcps" | "plugins" | "skills" | "agents" | "commands" | "profiles" | "summary" | "doctor".
func Dump(p paths.Paths, projectPath, tab string) (string, error) {
	st, err := loadState(p, projectPath)
	if err != nil {
		return "", fmt.Errorf("load state: %w", err)
	}
	m := newModel(st)
	switch tab {
	case "plugins":
		m.tab = tabPlugins
	case "marketplaces", "markets", "mkt":
		m.tab = tabMarketplaces
	case "skills":
		m.tab = tabSkills
	case "agents":
		m.tab = tabAgents
	case "commands":
		m.tab = tabCommands
	case "profiles":
		m.tab = tabProfiles
	case "summary":
		m.tab = tabSummary
	case "doctor":
		m.tab = tabDoctor
	case "help":
		m.showHelp = true
	default:
		m.tab = tabMCPs
	}
	// bootstrap a size so list views allocate
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return im.View(), nil
}

// --- shared styles ---------------------------------------------------------

var (
	styleTitle     = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	styleTab       = lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("244"))
	styleTabActive = lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("63")).Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleOK        = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleWarn      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleFooter    = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("244"))
	styleSelected  = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("238"))
	styleBadge     = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("63"))
)

// state is the mutable in-memory representation the TUI edits; it gets flushed to disk on Apply.
type state struct {
	paths   paths.Paths
	project string

	cj        *config.ClaudeJSON
	stash     *config.Stash
	settings  *config.Settings
	installed *config.InstalledPlugins
	profiles  *config.Profiles

	// pluginMCPs: map of MCP name -> every installed plugin that registers it
	// (enabled AND disabled, each tagged via PluginMCPSource.Enabled). Disabled-but-installed
	// entries are included so stale `plugin:X:Y` overrides still attribute back to a
	// concrete source rather than falling through to the orphan classifier.
	// Re-scanned whenever dirtySettings or dirtyPlugins flips.
	pluginMCPs map[string][]config.PluginMCPSource

	// claudeAi: full list of "claude.ai <Name>" strings from claudeAiMcpEverConnected
	claudeAi []string

	// updates caches probe results (per session) for marketplaces, plugins, and MCPs.
	updates *updates.Cache

	// change tracking
	dirtyClaude   bool
	dirtyStash    bool
	dirtySettings bool
	dirtyPlugins  bool
	dirtyProfiles bool
}

// rescanPluginMCPs refreshes pluginMCPs from the current enabledPlugins + installed_plugins state.
// Uses ScanAllInstalledPluginMCPs so disabled-but-installed plugins are still represented —
// consumers that care about "what will actually load" filter by PluginMCPSource.Enabled.
func (s *state) rescanPluginMCPs() {
	s.pluginMCPs = config.ScanAllInstalledPluginMCPs(s.settings, s.installed, s.paths.PluginsDir)
}

func loadState(p paths.Paths, project string) (*state, error) {
	cj, err := config.LoadClaudeJSON(p.ClaudeJSON)
	if err != nil {
		return nil, err
	}
	stash, err := config.LoadStash(p.Stash)
	if err != nil {
		return nil, err
	}
	settings, err := config.LoadSettings(p.SettingsJSON)
	if err != nil {
		return nil, err
	}
	installed, err := config.LoadInstalledPlugins(p.InstalledPlugins)
	if err != nil {
		return nil, err
	}
	profiles, err := config.LoadProfiles(p.Profiles)
	if err != nil {
		return nil, err
	}
	st := &state{
		paths:     p,
		project:   project,
		cj:        cj,
		stash:     stash,
		settings:  settings,
		installed: installed,
		profiles:  profiles,
		updates:   updates.NewCache(),
	}
	st.rescanPluginMCPs()
	st.claudeAi = cj.ClaudeAiEverConnected()
	return st, nil
}

func (s *state) save() (summary []string, err error) {
	do := func(dirty bool, path string, saver func() error) {
		if err != nil || !dirty {
			return
		}
		if e := config.Backup(path, s.paths.BackupsDir); e != nil {
			err = e
			return
		}
		if e := saver(); e != nil {
			err = e
			return
		}
		summary = append(summary, "saved "+path)
	}
	do(s.dirtyClaude, s.cj.Path, s.cj.Save)
	do(s.dirtyStash, s.stash.Path, s.stash.Save)
	do(s.dirtySettings, s.settings.Path, s.settings.Save)
	do(s.dirtyPlugins, s.installed.Path, s.installed.Save)
	do(s.dirtyProfiles, s.profiles.Path, s.profiles.Save)
	if err == nil {
		s.dirtyClaude = false
		s.dirtyStash = false
		s.dirtySettings = false
		s.dirtyPlugins = false
		s.dirtyProfiles = false
	}
	return
}

func (s *state) anyDirty() bool {
	return s.dirtyClaude || s.dirtyStash || s.dirtySettings || s.dirtyPlugins || s.dirtyProfiles
}
