package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tabID int

const (
	tabMCPs tabID = iota
	tabPlugins
	tabSkills
	tabAgents
	tabCommands
	tabProfiles
	tabSummary
	tabDoctor
)

var tabs = []struct {
	id    tabID
	label string
}{
	{tabMCPs, "MCPs"},
	{tabPlugins, "Plugins"},
	{tabSkills, "Skills"},
	{tabAgents, "Agents"},
	{tabCommands, "Commands"},
	{tabProfiles, "Profiles"},
	{tabSummary, "Summary"},
	{tabDoctor, "Doctor"},
}

type model struct {
	st       *state
	tab      tabID
	width    int
	height   int
	message  string // transient status message
	showHelp bool   // toggled by `?`; when true, View() renders the legend instead of tabs

	mcps     *mcpView
	plugins  *pluginView
	skills   *skillView
	agents   *agentView
	commands *commandView
	profiles *profileView
	summary  *summaryView
	doctor   *doctorView
}

func newModel(st *state) *model {
	return &model{
		st:       st,
		mcps:     newMCPView(st),
		plugins:  newPluginView(st),
		skills:   newSkillView(st),
		agents:   newAgentView(st),
		commands: newCommandView(st),
		profiles: newProfileView(st),
		summary:  newSummaryView(st),
		doctor:   newDoctorView(st),
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.mcps.resize(msg.Width, msg.Height-reservedHeight)
		m.plugins.resize(msg.Width, msg.Height-reservedHeight)
		m.skills.resize(msg.Width, msg.Height-reservedHeight)
		m.agents.resize(msg.Width, msg.Height-reservedHeight)
		m.commands.resize(msg.Width, msg.Height-reservedHeight)
		m.profiles.resize(msg.Width, msg.Height-reservedHeight)
		m.summary.resize(msg.Width, msg.Height-reservedHeight)
		m.doctor.resize(msg.Width, msg.Height-reservedHeight)
		return m, nil

	case tea.KeyMsg:
		// Help overlay swallows input until dismissed. Only `?` and `esc` close it
		// — matching the footer hint — so keys the user might reflexively press (like
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
			return m, tea.Quit
		case "?":
			m.showHelp = true
			return m, nil
		case "q", "esc":
			if m.st.anyDirty() {
				m.message = styleWarn.Render("unsaved changes — press Q again or `w` to save + quit, `D` to discard + quit")
				return m, nil
			}
			return m, tea.Quit
		case "Q":
			return m, tea.Quit
		case "D":
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
			return m, nil
		case "shift+tab":
			m.tab = (m.tab + tabID(len(tabs)) - 1) % tabID(len(tabs))
			return m, nil
		case "1":
			m.tab = tabMCPs
			return m, nil
		case "2":
			m.tab = tabPlugins
			return m, nil
		case "3":
			m.tab = tabSkills
			return m, nil
		case "4":
			m.tab = tabAgents
			return m, nil
		case "5":
			m.tab = tabCommands
			return m, nil
		case "6":
			m.tab = tabProfiles
			return m, nil
		case "7":
			m.tab = tabSummary
			return m, nil
		case "8":
			m.tab = tabDoctor
			return m, nil
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
	case tabProfiles:
		cmd := m.profiles.update(msg)
		if m.profiles.flash != "" {
			m.message = m.profiles.flash
			m.profiles.flash = ""
		}
		return m, cmd
	case tabSummary:
		return m, m.summary.update(msg)
	case tabDoctor:
		return m, m.doctor.update(msg)
	}
	return m, nil
}

func (m *model) activeView() view {
	switch m.tab {
	case tabMCPs:
		return m.mcps
	case tabPlugins:
		return m.plugins
	case tabSkills:
		return m.skills
	case tabAgents:
		return m.agents
	case tabCommands:
		return m.commands
	case tabProfiles:
		return m.profiles
	case tabSummary:
		return m.summary
	case tabDoctor:
		return m.doctor
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

	body := m.activeView().render()

	footer := styleFooter.Render(m.footerHelp())
	if m.message != "" {
		footer = styleFooter.Render(m.message) + "\n" + footer
	}

	return lipgloss.JoinVertical(lipgloss.Left, header.String(), body, footer)
}

func (m *model) footerHelp() string {
	common := "tab: next  w: save  ?: help  q: quit"
	return m.activeView().helpText() + "  │  " + common
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
