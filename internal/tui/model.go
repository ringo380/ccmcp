package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tabID int

const (
	tabMCPs tabID = iota
	tabPlugins
	tabMarketplaces
	tabDiscover
	tabSkills
	tabAgents
	tabCommands
	tabTweaks
	// Folded into Tweaks - kept as ids so origin-routing of async fix/chat/stream
	// messages (which tag tabDoctor/tabSummary) still resolves. Not top-level tabs.
	tabSummary
	tabDoctor
	tabProfiles
)

var tabs = []struct {
	id    tabID
	label string
}{
	{tabMCPs, "MCPs"},
	{tabPlugins, "Plugins"},
	{tabMarketplaces, "Marketplaces"},
	{tabDiscover, "Discover"},
	{tabSkills, "Skills"},
	{tabAgents, "Agents"},
	{tabCommands, "Commands"},
	{tabTweaks, "Tweaks"},
}

type model struct {
	st       *state
	tab      tabID
	width    int
	height   int
	message  string // transient status message
	showHelp bool   // toggled by `?`; when true, View() renders the legend instead of tabs

	mcps         *mcpView
	plugins      *pluginView
	marketplaces *marketplaceView
	discover     *discoveryView
	skills       *skillView
	agents       *agentView
	commands     *commandView
	tweaks       *tweaksView

	// spinner drives the live in-progress indicator. Always-ticking; ~10 fps.
	// Frame is republished to state.spinnerFrame on each tick so views can render it.
	spinner spinner.Model

	// globalSearch is the cross-tab search overlay (ctrl+g). When active it
	// captures all input and renders in place of the active view's body.
	globalSearch globalSearchState
}

func newModel(st *state) *model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleProgress
	return &model{
		st:           st,
		mcps:         newMCPView(st),
		plugins:      newPluginView(st),
		marketplaces: newMarketplaceView(st),
		discover:     newDiscoveryView(st),
		skills:       newSkillView(st),
		agents:       newAgentView(st),
		commands:     newCommandView(st),
		tweaks:       newTweaksView(st),
		spinner:      sp,
		globalSearch: newGlobalSearchState(),
	}
}

func (m *model) Init() tea.Cmd { return m.spinner.Tick }

// killActiveFixes best-effort terminates any in-flight `claude --print` fix
// subprocesses spawned by execFixCmd before the TUI quits. Without this, a
// fix can outlive the parent process: pump goroutines block on the full
// channel, cmd.Wait keeps the child alive, and the user sees claude still
// running in their terminal after pressing q. Safe to call when nothing is
// running.
func (m *model) killActiveFixes() {
	killIfRunning(m.tweaks.doctor.fixCmd)
	killIfRunning(m.tweaks.summary.fixCmd)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if t, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(t)
		m.st.spinnerFrame = m.spinner.View()
		return m, cmd
	}
	// fixDoneMsg routes to the view that initiated the fix, regardless of which
	// tab is currently focused. Both doctor and summary share execFixCmd, so
	// without explicit routing a fix started on one tab and observed via tab
	// switch would silently land nowhere.
	if done, ok := msg.(fixDoneMsg); ok {
		if cmd, handled := m.tweaks.routeFix(done.origin, done); handled {
			if m.tweaks.flash != "" {
				m.message = m.tweaks.flash
				m.tweaks.flash = ""
			}
			return m, cmd
		}
		m.message = styleErr.Render(fmt.Sprintf("internal: fixDoneMsg with unhandled origin %d", done.origin))
		return m, nil
	}
	if done, ok := msg.(chatDoneMsg); ok {
		if cmd, handled := m.tweaks.routeFix(done.origin, done); handled {
			if m.tweaks.flash != "" {
				m.message = m.tweaks.flash
				m.tweaks.flash = ""
			}
			return m, cmd
		}
		m.message = styleErr.Render(fmt.Sprintf("internal: chatDoneMsg with unhandled origin %d", done.origin))
		return m, nil
	}
	// cliStreamLineMsg routes the same way - each line carries an origin so
	// the line lands on the originating view's ring buffer regardless of
	// where the user has navigated since starting the fix. Returning
	// `line.next` re-arms the drainer for the following line; the chain ends
	// when execFixCmd emits the terminal fixDoneMsg.
	if line, ok := msg.(cliStreamLineMsg); ok {
		switch line.origin {
		case tabDoctor:
			m.tweaks.doctor.update(line)
		case tabSummary:
			m.tweaks.summary.update(line)
		}
		return m, line.next
	}
	// Global search overlay captures all input (and the textinput's blink
	// ticks) while open. Window-size messages still flow to the resize handler
	// below so the layout stays correct if the terminal is resized mid-search.
	if m.globalSearch.active {
		if _, ok := msg.(tea.WindowSizeMsg); !ok {
			return m.updateGlobalSearch(msg)
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.mcps.resize(msg.Width, msg.Height-reservedHeight)
		m.plugins.resize(msg.Width, msg.Height-reservedHeight)
		m.marketplaces.resize(msg.Width, msg.Height-reservedHeight)
		m.discover.resize(msg.Width, msg.Height-reservedHeight)
		m.skills.resize(msg.Width, msg.Height-reservedHeight)
		m.agents.resize(msg.Width, msg.Height-reservedHeight)
		m.commands.resize(msg.Width, msg.Height-reservedHeight)
		m.tweaks.resize(msg.Width, msg.Height-reservedHeight)
		return m, nil

	case tea.KeyMsg:
		// Help overlay swallows input until dismissed. Only `?` and `esc` close it
		// - matching the footer hint - so keys the user might reflexively press (like
		// `q` to "quit") don't silently do something different from the rest of the app.
		if m.showHelp {
			switch msg.String() {
			case "?", "esc":
				m.showHelp = false
			}
			return m, nil
		}
		// Global keys first, but defer to the active view if it's in an input mode (filter).
		if m.activeView().capturingInput() {
			return m.updateActive(msg)
		}
		switch msg.String() {
		case "ctrl+c":
			m.killActiveFixes()
			return m, tea.Quit
		case "?":
			m.showHelp = true
			return m, nil
		case "ctrl+g":
			return m, m.openGlobalSearch()
		case "q", "esc":
			if m.st.anyDirty() {
				m.message = styleWarn.Render("unsaved changes - press Q again or `w` to save + quit, `D` to discard + quit")
				return m, nil
			}
			m.killActiveFixes()
			return m, tea.Quit
		case "Q":
			m.killActiveFixes()
			return m, tea.Quit
		case "D":
			m.killActiveFixes()
			return m, tea.Quit // discard on exit
		case "w":
			if !m.st.anyDirty() {
				m.message = styleDim.Render("no changes to save")
				return m, nil
			}
			summary, err := m.st.save()
			if err != nil {
				m.message = styleErr.Render("save failed: " + err.Error())
				return m, nil
			}
			m.message = styleOK.Render(fmt.Sprintf("saved %d file(s)", len(summary)))
			return m, nil
		case "tab":
			m.tab = (m.tab + 1) % tabID(len(tabs))
			m.message = ""
			return m, m.tabEnterCmd()
		case "shift+tab":
			m.tab = (m.tab + tabID(len(tabs)) - 1) % tabID(len(tabs))
			m.message = ""
			return m, m.tabEnterCmd()
		case "1":
			m.tab = tabMCPs
			m.message = ""
			return m, m.mcps.initialCheckCmd()
		case "2":
			m.tab = tabPlugins
			m.message = ""
			return m, m.tabEnterCmd()
		case "3":
			m.tab = tabMarketplaces
			m.message = ""
			return m, m.tabEnterCmd()
		case "4":
			m.tab = tabDiscover
			m.message = ""
			return m, m.discover.initialCheckCmd()
		case "5":
			m.tab = tabSkills
			m.message = ""
			return m, nil
		case "6":
			m.tab = tabAgents
			m.message = ""
			return m, nil
		case "7":
			m.tab = tabCommands
			m.message = ""
			return m, nil
		case "t":
			m.tab = tabTweaks
			m.tweaks.sub = subSettings
			m.message = ""
			return m, m.tabEnterCmd()
		}
	}
	return m.updateActive(msg)
}

func (m *model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabMCPs:
		cmd := m.mcps.update(msg)
		if m.mcps.flash != "" {
			m.message = m.mcps.flash
			m.mcps.flash = ""
		}
		return m, cmd
	case tabPlugins:
		cmd := m.plugins.update(msg)
		if m.plugins.flash != "" {
			m.message = m.plugins.flash
			m.plugins.flash = ""
		}
		return m, cmd
	case tabMarketplaces:
		cmd := m.marketplaces.update(msg)
		if m.marketplaces.flash != "" {
			m.message = m.marketplaces.flash
			m.marketplaces.flash = ""
		}
		return m, cmd
	case tabDiscover:
		cmd := m.discover.update(msg)
		if m.discover.flash != "" {
			m.message = m.discover.flash
			m.discover.flash = ""
		}
		return m, cmd
	case tabSkills:
		cmd := m.skills.update(msg)
		if m.skills.flash != "" {
			m.message = m.skills.flash
			m.skills.flash = ""
		}
		return m, cmd
	case tabAgents:
		cmd := m.agents.update(msg)
		if m.agents.flash != "" {
			m.message = m.agents.flash
			m.agents.flash = ""
		}
		return m, cmd
	case tabCommands:
		cmd := m.commands.update(msg)
		if m.commands.flash != "" {
			m.message = m.commands.flash
			m.commands.flash = ""
		}
		return m, cmd
	case tabTweaks:
		cmd := m.tweaks.update(msg)
		if m.tweaks.flash != "" {
			m.message = m.tweaks.flash
			m.tweaks.flash = ""
		}
		return m, cmd
	}
	return m, nil
}

func (m *model) activeView() view {
	switch m.tab {
	case tabMCPs:
		return m.mcps
	case tabPlugins:
		return m.plugins
	case tabMarketplaces:
		return m.marketplaces
	case tabDiscover:
		return m.discover
	case tabSkills:
		return m.skills
	case tabAgents:
		return m.agents
	case tabCommands:
		return m.commands
	case tabTweaks:
		return m.tweaks
	}
	return m.mcps
}

func (m *model) View() string {
	if m.width == 0 {
		return "initialising..."
	}
	if m.showHelp {
		// Render the legend full-screen. Still keep a tiny footer so the user knows
		// how to exit.
		body := renderHelp(m.width)
		footer := styleFooter.Render("? or esc: close help")
		return lipgloss.JoinVertical(lipgloss.Left, body, footer)
	}
	var header strings.Builder
	header.WriteString(styleTitle.Render("ccmcp"))
	if Version != "" {
		header.WriteString(" ")
		v := Version
		if v[0] >= '0' && v[0] <= '9' {
			v = "v" + v
		}
		header.WriteString(styleDim.Render(v))
	}
	if ClaudeVersion != "" {
		header.WriteString(" ")
		header.WriteString(styleDim.Render("· CC " + ClaudeVersion))
	}
	header.WriteString("  ")
	header.WriteString(styleDim.Render(m.st.project))
	if m.st.anyDirty() {
		header.WriteString("  ")
		header.WriteString(styleBadge.Render("UNSAVED"))
	}
	header.WriteString("\n")

	var tabLine strings.Builder
	for _, t := range tabs {
		style := styleTab
		if t.id == m.tab {
			style = styleTabActive
		}
		tabLine.WriteString(style.Render(t.label))
	}
	header.WriteString(tabLine.String())
	header.WriteString("\n")

	var body string
	if m.globalSearch.active {
		body = m.renderGlobalSearch()
	} else {
		body = m.activeView().render()
	}

	// Safety net: never let a view's body overflow past the available height into
	// the terminal's native scrollback. Per-view rendering keeps the cursor in
	// view; this only trims excess from a miscounting (or future) view.
	if avail := m.height - reservedHeight; avail > 0 {
		if lines := strings.Split(body, "\n"); len(lines) > avail {
			body = strings.Join(lines[:avail], "\n")
		}
	}

	footer := styleFooter.Render(m.footerHelp())
	if m.message != "" {
		footer = styleFooter.Render(m.message) + "\n" + footer
	}

	return lipgloss.JoinVertical(lipgloss.Left, header.String(), body, footer)
}

func (m *model) footerHelp() string {
	if m.globalSearch.active {
		return "↑/↓: move  enter: go  esc: close"
	}
	common := "tab: next  ctrl+g: search  w: save  ?: help  q: quit"
	return m.activeView().helpText() + "  │  " + common
}

// tabEnterCmd returns a one-shot Cmd to fire when the user navigates to a tab that
// has lazy-loaded background work (e.g. update probes). Most tabs return nil.
func (m *model) tabEnterCmd() tea.Cmd {
	switch m.tab {
	case tabMarketplaces:
		// Rebuild from shared state so a marketplace added from the Discover tab
		// shows up here (these views cache v.rows and don't otherwise refresh on
		// tab switch).
		m.marketplaces.rebuild()
		return m.marketplaces.initialCheckCmd()
	case tabDiscover:
		return m.discover.initialCheckCmd()
	case tabPlugins:
		// Rebuild so a plugin installed from the Discover tab appears here.
		m.plugins.rebuild()
		return m.plugins.initialCheckCmd()
	case tabMCPs:
		// Rebuild so plugin enable/disable changes made on the Plugins tab (which
		// re-scans state.pluginMCPs) are reflected in the effective list here.
		m.mcps.rebuild()
		return m.mcps.initialCheckCmd()
	}
	return nil
}

// reservedHeight is how much vertical space the header + footer + padding take.
const reservedHeight = 5

// view is the common contract each tab implements.
type view interface {
	update(msg tea.Msg) tea.Cmd
	render() string
	resize(w, h int)
	helpText() string
	capturingInput() bool
}
