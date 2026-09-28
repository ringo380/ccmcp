package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ringo380/ccmcp/internal/claudecode"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/ctxcost"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/ringo380/ccmcp/internal/updates"
)

// Version is set by the cmd layer before Run/Dump; empty hides the header version (tests).
var Version string

// ClaudeVersion is the detected Claude Code CLI version, set by the cmd layer
// before Run/Dump. Empty (the default, e.g. in headless tests or when undetected)
// hides the "· CC <ver>" header chip.
var ClaudeVersion string

// Caps is the version-calibrated capability set the TUI's lint/fix paths read.
// Defaults to the Baseline so headless tests that never set it keep prior
// behavior; the cmd layer overrides it with CapabilitiesFor(detected version).
var Caps = claudecode.Baseline()

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
// tab can be "mcps" | "plugins" | "marketplaces" (alias: "markets"|"mkt") |
// "discover" (alias: "discovery") | "skills" | "agents" | "commands" |
// "tweaks" | "profiles" | "summary" | "doctor" | "help".
// profiles/summary/doctor are sub-tabs of tweaks.
//
// Note: lazy-loaded update probes (plugins/marketplaces/MCPs "↑ update available"
// indicators) and Discover-tab registry fetches fire from update(), not render(),
// so Dump() will not show them - by design, since Dump is a one-shot diagnostic
// and shouldn't fire network calls.
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
	case "discover", "discovery":
		m.tab = tabDiscover
	case "skills":
		m.tab = tabSkills
	case "agents":
		m.tab = tabAgents
	case "commands":
		m.tab = tabCommands
	case "tweaks":
		m.tab = tabTweaks
	case "profiles":
		m.tab = tabTweaks
		m.tweaks.sub = subProfiles
	case "summary":
		m.tab = tabTweaks
		m.tweaks.sub = subSummary
	case "doctor":
		m.tab = tabTweaks
		m.tweaks.sub = subDoctor
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
	styleProgress  = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
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
	appcfg    *config.AppConfig

	// pluginMCPs: map of MCP name -> every installed plugin that registers it
	// (enabled AND disabled, each tagged via PluginMCPSource.Enabled). Disabled-but-installed
	// entries are included so stale `plugin:X:Y` overrides still attribute back to a
	// concrete source rather than falling through to the orphan classifier.
	// Re-scanned whenever dirtySettings or dirtyPlugins flips.
	pluginMCPs map[string][]config.PluginMCPSource

	// cost caches the per-turn context estimate. Built on first use and
	// invalidated wherever pluginMCPs is re-scanned (dirtySettings /
	// dirtyPlugins), since both derive from the same enabled-plugin state.
	// nil means "not built yet", NOT "zero cost".
	cost *ctxcost.Index

	// costErr is the error from the build that produced `cost`, retained because
	// the fallback empty index is indistinguishable from a genuine zero. Read it
	// through costUnavailable() before rendering any figure.
	costErr error

	// costSettingsGen/costPluginsGen record settingsGen/pluginsGen at the moment
	// `cost` was built. Any change to either means a mutation landed that could
	// move the estimate (skill/agent overrides feed ctxcost's Enabled filter), so
	// the cache is rebuilt. Cheaper and far more robust than calling
	// invalidateCost() at every mutation site - a new site added later is covered
	// automatically, PROVIDED it goes through markSettingsDirty/markPluginsDirty.
	costSettingsGen int
	costPluginsGen  int
	costMCPGen      int

	// measuredVal/measuredOK cache the transcript calibration, which is a
	// directory scan plus a full file scan - too expensive to redo per render
	// frame. Invalidated exactly like `cost`.
	measuredVal         ctxcost.Measured
	measuredOK          bool
	measuredValid       bool
	measuredSettingsGen int
	measuredPluginsGen  int

	// probes is the on-disk MCP probe cache, loaded on first use via
	// probeCache(). Never nil once loaded - mcpprobe.LoadCache self-heals a
	// missing or corrupt file into an empty cache.
	probes *mcpprobe.Cache

	// mcpStates is the per-server context-cost state the MCPs tab publishes for
	// the estimate, keyed by display name. Written only through setMCPStates,
	// which is where the completeness contract on ctxcost.Input.MCP is honored:
	// EVERY server that loads in this project gets an entry, unprobed ones with
	// Probed=false and a reason.
	mcpStates map[string]ctxcost.MCPState

	// mcpStateKey fingerprints mcpStates so setMCPStates can tell a real change
	// from a re-publish of identical state.
	mcpStateKey string

	// mcpGen counts changes to mcpStates and only ever increases; the cost cache
	// keys off it exactly as it keys off settingsGen/pluginsGen. A boolean would
	// be blind to a second consecutive change.
	mcpGen int

	// claudeAi: full list of "claude.ai <Name>" strings from claudeAiMcpEverConnected
	claudeAi []string

	// updates caches probe results (per session) for marketplaces, plugins, and MCPs.
	updates *updates.Cache

	// spinnerFrame is the current animation frame published by the model's spinner.
	// Views read this to render live in-progress indicators alongside their busy flags.
	// Empty during one-shot Dump() since no TickMsg is processed.
	spinnerFrame string

	// change tracking
	dirtyClaude     bool
	dirtyStash      bool
	dirtySettings   bool
	dirtyPlugins    bool
	dirtyProfiles   bool
	dirtyAppConfig  bool

	// settingsGen/pluginsGen count mutations, and only ever increase. The cost
	// cache keys off them rather than off dirtySettings/dirtyPlugins, which are
	// pending-WRITE booleans: once one is true a second mutation sets it true
	// again, so a boolean comparison cannot see it and the cache goes stale from
	// the second mutation onward. Bump them only via markSettingsDirty /
	// markPluginsDirty.
	settingsGen int
	pluginsGen  int

	// pendingCacheGC holds superseded plugin cache dirs from in-memory UpdateInstall calls.
	// They are deleted ONLY after installed_plugins.json saves successfully (see save()),
	// so a discarded/failed apply never strands the on-disk registry pointing at a deleted
	// directory - the "plugin cache does not exist" failure mode.
	pendingCacheGC []string
}

// markSettingsDirty records a pending settings.json write and advances the
// settings generation so every derived cache (currently the context-cost index
// and the transcript calibration) rebuilds. Every mutation site must call this
// instead of assigning dirtySettings directly.
func (s *state) markSettingsDirty() {
	s.dirtySettings = true
	s.settingsGen++
}

// markPluginsDirty is markSettingsDirty's counterpart for installed_plugins.json.
func (s *state) markPluginsDirty() {
	s.dirtyPlugins = true
	s.pluginsGen++
}

// probeCache returns the MCP probe cache, loading it on first use. It is not
// part of the dirty/apply model: a probe result is a measurement of the world,
// not a pending edit, so it is written straight through on completion.
func (s *state) probeCache() *mcpprobe.Cache {
	if s.probes == nil {
		// LoadCache never returns a nil cache and never errors on a corrupt
		// file - it self-heals to empty, so a miss and a corrupt file are
		// indistinguishable here, which is the right behavior for a cache.
		c, _ := mcpprobe.LoadCache(s.paths.ProbeCache)
		s.probes = c
	}
	return s.probes
}

// setMCPStates publishes the MCPs tab's per-server cost state and advances
// mcpGen when it actually changed, so the cost cache rebuilds. This is the ONLY
// invalidation path for the MCP half of the estimate: it covers both a completed
// probe and a config mutation that changes which servers load.
func (s *state) setMCPStates(states map[string]ctxcost.MCPState) {
	key := fingerprintMCPStates(states)
	if s.mcpStates != nil && key == s.mcpStateKey {
		return
	}
	s.mcpStates = states
	s.mcpStateKey = key
	s.mcpGen++
}

// fingerprintMCPStates renders states into a stable string. Values are included,
// not just keys: a completed probe leaves the key set untouched while turning an
// unmeasured entry into a costed one, and a key-only fingerprint would miss it.
func fingerprintMCPStates(states map[string]ctxcost.MCPState) string {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		st := states[name]
		fmt.Fprintf(&b, "%s\x00%t\x00%s\x00%d\x00%d\n",
			name, st.Probed, st.Reason, st.Cost.Loaded, st.Cost.Deferred)
	}
	return b.String()
}

// rescanPluginMCPs refreshes pluginMCPs from the current enabledPlugins + installed_plugins state.
// Uses ScanAllInstalledPluginMCPs so disabled-but-installed plugins are still represented -
// consumers that care about "what will actually load" filter by PluginMCPSource.Enabled.
func (s *state) rescanPluginMCPs() {
	s.pluginMCPs = config.ScanAllInstalledPluginMCPs(s.settings, s.installed, s.paths.PluginsDir)
	s.invalidateCost()
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
	appcfg := config.LoadAppConfig(p.AppConfig)
	st := &state{
		paths:     p,
		project:   project,
		cj:        cj,
		stash:     stash,
		settings:  settings,
		installed: installed,
		profiles:  profiles,
		appcfg:    appcfg,
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
	do(s.dirtyAppConfig, s.appcfg.Path, s.appcfg.Save)
	if err == nil {
		s.dirtyClaude = false
		s.dirtyStash = false
		s.dirtySettings = false
		s.dirtyPlugins = false
		s.dirtyProfiles = false
		s.dirtyAppConfig = false
		// installed_plugins.json is now persisted pointing at the NEW cache dirs, so the
		// superseded dirs are safe to delete. On a failed save we keep them queued (the
		// registry still references them) and retry on the next save.
		for _, sp := range s.pendingCacheGC {
			_ = install.GCStaleCache(sp)
		}
		s.pendingCacheGC = nil
	}
	return
}

func (s *state) anyDirty() bool {
	return s.dirtyClaude || s.dirtyStash || s.dirtySettings || s.dirtyPlugins || s.dirtyProfiles || s.dirtyAppConfig
}
