package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/config"
)

// agentView is a read-only listing. Toggling isn't wired yet because Claude Code
// has no native agent-override mechanism; CRUD happens via the CLI.
type agentView struct {
	st    *state
	rows  []agents.Agent
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool
	filterText   string
}

func newAgentView(st *state) *agentView {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.CharLimit = 64
	v := &agentView{st: st, filter: ti}
	v.rebuild()
	return v
}

func (v *agentView) rebuild() {
	all := agents.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	if v.filterText == "" {
		v.rows = all
	} else {
		needle := strings.ToLower(v.filterText)
		v.rows = v.rows[:0]
		for _, a := range all {
			if strings.Contains(strings.ToLower(a.Name), needle) {
				v.rows = append(v.rows, a)
			}
		}
	}
	if v.index >= len(v.rows) {
		v.index = len(v.rows) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
}

func (v *agentView) update(msg tea.Msg) tea.Cmd {
	if v.filterActive {
		var cmd tea.Cmd
		v.filter, cmd = v.filter.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter":
				v.filterText = strings.TrimSpace(v.filter.Value())
				v.filterActive = false
				v.filter.Blur()
				v.rebuild()
			case "esc":
				v.filterActive = false
				v.filter.SetValue("")
				v.filter.Blur()
			default:
				v.filterText = v.filter.Value()
				v.rebuild()
			}
		}
		return cmd
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "up", "k":
		if v.index > 0 {
			v.index--
		}
	case "down", "j":
		if v.index < len(v.rows)-1 {
			v.index++
		}
	case "g", "home":
		v.index = 0
		v.top = 0
	case "G", "end":
		v.index = len(v.rows) - 1
	case "pgup":
		v.index -= 10
		if v.index < 0 {
			v.index = 0
		}
	case "pgdn":
		v.index += 10
		if v.index >= len(v.rows) {
			v.index = len(v.rows) - 1
		}
	case "/":
		v.filterActive = true
		v.filter.Focus()
		return textinput.Blink
	case "c":
		v.filterText = ""
		v.filter.SetValue("")
		v.rebuild()
	}
	return nil
}

func (v *agentView) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agents (%d)", len(v.rows))
	if v.filterText != "" {
		b.WriteString(styleDim.Render(fmt.Sprintf("  filter: %q", v.filterText)))
	}
	b.WriteString("\n")
	if v.filterActive {
		b.WriteString(v.filter.View() + "\n")
	}
	if len(v.rows) == 0 {
		b.WriteString(styleDim.Render("  (no agents match)"))
		return b.String()
	}
	pageH := v.h - 4
	if pageH < 4 {
		pageH = 4
	}
	if v.index < v.top {
		v.top = v.index
	}
	if v.index >= v.top+pageH {
		v.top = v.index - pageH + 1
	}
	end := v.top + pageH
	if end > len(v.rows) {
		end = len(v.rows)
	}
	for i := v.top; i < end; i++ {
		a := v.rows[i]
		src := string(a.Scope)
		if a.Scope == agents.ScopePlugin {
			pname, _ := config.ParsePluginID(a.PluginID)
			src = "P:" + pname
		}
		model := a.Model
		if model == "" {
			model = "-"
		}
		desc := assets.Truncate(a.Description, 50)
		line := fmt.Sprintf("  %-30s  %-22s  %-8s  %s", a.Name, src, model, styleDim.Render(desc))
		if i == v.index {
			b.WriteString(styleSelected.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (v *agentView) resize(w, h int) { v.w, v.h = w, h }

func (v *agentView) helpText() string {
	return "/: filter  c: clear  (CRUD via CLI: ccmcp agent new|move|rm|show)"
}

func (v *agentView) capturingInput() bool { return v.filterActive }
