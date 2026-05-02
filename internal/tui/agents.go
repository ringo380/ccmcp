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

type agentView struct {
	st    *state
	rows  []agents.Agent
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool
	filterText   string

	// new agent name input
	input       textinput.Model
	inputActive bool

	// move mode
	moveActive bool
	moveName   string
	moveFrom   agents.Scope

	// delete confirm
	pendingRm string

	flash string
}

func newAgentView(st *state) *agentView {
	filter := textinput.New()
	filter.Prompt = "filter: "
	filter.CharLimit = 64

	nameInput := textinput.New()
	nameInput.Prompt = "agent name: "
	nameInput.CharLimit = 64

	v := &agentView{st: st, filter: filter, input: nameInput}
	v.rebuild()
	return v
}

func (v *agentView) rebuild() {
	all := agents.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	if v.filterText == "" {
		v.rows = all
	} else {
		needle := strings.ToLower(v.filterText)
		var filtered []agents.Agent
		for _, a := range all {
			if strings.Contains(strings.ToLower(a.Name), needle) {
				filtered = append(filtered, a)
			}
		}
		v.rows = filtered
	}
	if v.index >= len(v.rows) {
		v.index = len(v.rows) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
}

func (v *agentView) update(msg tea.Msg) tea.Cmd {
	// New agent name input mode.
	if v.inputActive {
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter":
				name := strings.TrimSpace(v.input.Value())
				if name != "" {
					path, err := agents.Scaffold(name, "", "sonnet", agents.ScopeUser, v.st.paths.ClaudeConfigDir, v.st.project)
					if err != nil {
						v.flash = styleErr.Render("scaffold: " + err.Error())
					} else {
						v.flash = styleOK.Render(fmt.Sprintf("created agent %q at %s", name, path))
						v.rebuild()
					}
				}
				v.inputActive = false
				v.input.SetValue("")
				v.input.Blur()
			case "esc":
				v.inputActive = false
				v.input.SetValue("")
				v.input.Blur()
			}
		}
		return cmd
	}

	// Filter input mode.
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

	// Move scope picker mode.
	if v.moveActive {
		k, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		switch k.String() {
		case "u":
			v.doMove(agents.ScopeUser)
		case "p":
			v.doMove(agents.ScopeProject)
		case "esc":
			v.moveActive = false
			v.flash = styleDim.Render("move cancelled")
		}
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	// Any key other than 'd' clears a pending delete.
	if v.pendingRm != "" && key.String() != "d" {
		v.pendingRm = ""
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
		if n := len(v.rows); n > 0 {
			v.index = n - 1
		}
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
	case "n":
		v.inputActive = true
		v.input.Focus()
		return textinput.Blink
	case "m":
		if len(v.rows) == 0 {
			return nil
		}
		cur := v.rows[v.index]
		if cur.Scope == agents.ScopePlugin {
			v.flash = styleWarn.Render("plugin agents are read-only")
			return nil
		}
		v.moveName = cur.Name
		v.moveFrom = cur.Scope
		v.moveActive = true
		v.flash = styleDim.Render(fmt.Sprintf("move %q — target scope: [u]ser / [p]roject / esc: cancel", cur.Name))
	case "d":
		if len(v.rows) == 0 {
			return nil
		}
		cur := v.rows[v.index]
		if cur.Scope == agents.ScopePlugin {
			v.flash = styleWarn.Render("plugin agents are read-only")
			return nil
		}
		if v.pendingRm == cur.Name {
			_, found, err := agents.Remove(cur.Name, cur.Scope, v.st.paths.ClaudeConfigDir, v.st.project)
			if err != nil {
				v.flash = styleErr.Render("remove: " + err.Error())
			} else if !found {
				v.flash = styleWarn.Render(cur.Name + " not found on disk")
			} else {
				v.flash = styleDim.Render("removed " + cur.Name)
			}
			v.pendingRm = ""
			v.rebuild()
		} else {
			v.pendingRm = cur.Name
			v.flash = styleWarn.Render(fmt.Sprintf("press 'd' again to remove %q, any other key cancels", cur.Name))
		}
	}
	return nil
}

func (v *agentView) doMove(to agents.Scope) {
	v.moveActive = false
	if v.moveFrom == to {
		v.flash = styleDim.Render(fmt.Sprintf("%q is already in %s scope", v.moveName, to))
		return
	}
	_, _, err := agents.Move(v.moveName, v.moveFrom, to, v.st.paths.ClaudeConfigDir, v.st.project)
	if err != nil {
		v.flash = styleErr.Render("move: " + err.Error())
	} else {
		v.flash = styleOK.Render(fmt.Sprintf("moved %q → %s", v.moveName, to))
		v.rebuild()
	}
}

func (v *agentView) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agents (%d)", len(v.rows))
	if v.filterText != "" {
		b.WriteString(styleDim.Render(fmt.Sprintf("  filter: %q", v.filterText)))
	}
	b.WriteString("\n")
	if v.inputActive {
		b.WriteString(v.input.View() + "\n")
	} else if v.filterActive {
		b.WriteString(v.filter.View() + "\n")
	}
	if len(v.rows) == 0 {
		b.WriteString(styleDim.Render("  (no agents match — press 'n' to create one)"))
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
	if v.moveActive {
		return "[u]ser / [p]roject / esc: cancel"
	}
	return "n: new  m: move  d: delete  /: filter  c: clear"
}

func (v *agentView) capturingInput() bool { return v.filterActive || v.inputActive }
