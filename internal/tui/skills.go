package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/skills"
)

type skillView struct {
	st    *state
	rows  []skills.Skill
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool
	filterText   string

	// new skill input
	input       textinput.Model
	inputActive bool

	// move mode: picking target scope
	moveActive bool
	moveName   string
	moveFrom   skills.Scope

	// delete confirm
	pendingRm string

	flash string
}

func newSkillView(st *state) *skillView {
	filter := textinput.New()
	filter.Prompt = "filter: "
	filter.CharLimit = 64

	nameInput := textinput.New()
	nameInput.Prompt = "skill name: "
	nameInput.CharLimit = 64

	v := &skillView{st: st, filter: filter, input: nameInput}
	v.rebuild()
	return v
}

func (v *skillView) rebuild() {
	all := skills.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	if v.filterText == "" {
		v.rows = all
	} else {
		needle := strings.ToLower(v.filterText)
		var filtered []skills.Skill
		for _, s := range all {
			if strings.Contains(strings.ToLower(s.Name), needle) {
				filtered = append(filtered, s)
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

func (v *skillView) update(msg tea.Msg) tea.Cmd {
	// New skill name input mode.
	if v.inputActive {
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter":
				name := strings.TrimSpace(v.input.Value())
				if name != "" {
					path, err := skills.Scaffold(name, "", skills.ScopeUser, v.st.paths.ClaudeConfigDir, v.st.project)
					if err != nil {
						v.flash = styleErr.Render("scaffold: " + err.Error())
					} else {
						v.flash = styleOK.Render(fmt.Sprintf("created skill %q at %s", name, path))
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
			v.doMove(skills.ScopeUser)
		case "p":
			v.doMove(skills.ScopeProject)
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
		if v.filterText != "" {
			v.filterText = ""
			v.filter.SetValue("")
			v.rebuild()
		}
	case " ":
		if len(v.rows) == 0 {
			return nil
		}
		cur := v.rows[v.index]
		v.toggle(cur)
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
		if cur.Scope == skills.ScopePlugin {
			v.flash = styleWarn.Render("plugin skills are read-only — copy to user scope to customise")
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
		if cur.Scope == skills.ScopePlugin {
			v.flash = styleWarn.Render("plugin skills are read-only — disable the plugin instead")
			return nil
		}
		if v.pendingRm == cur.Name {
			_, found, err := skills.Remove(cur.Name, cur.Scope, v.st.paths.ClaudeConfigDir, v.st.project)
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
	case "A":
		for _, s := range v.rows {
			v.st.settings.RemoveSkillOverride(s.Name)
		}
		v.st.dirtySettings = true
		v.flash = styleOK.Render(fmt.Sprintf("enabled %d skill(s) (unsaved)", len(v.rows)))
		v.rebuild()
	case "N":
		n := 0
		for _, s := range v.rows {
			if s.Scope == skills.ScopePlugin || s.Scope == skills.ScopeUser || s.Scope == skills.ScopeProject {
				v.st.settings.SetSkillOverride(s.Name, "off")
				n++
			}
		}
		v.st.dirtySettings = true
		v.flash = styleWarn.Render(fmt.Sprintf("disabled %d skill(s) (unsaved)", n))
		v.rebuild()
	}
	return nil
}

func (v *skillView) doMove(to skills.Scope) {
	v.moveActive = false
	if v.moveFrom == to {
		v.flash = styleDim.Render(fmt.Sprintf("%q is already in %s scope", v.moveName, to))
		return
	}
	_, _, err := skills.Move(v.moveName, v.moveFrom, to, v.st.paths.ClaudeConfigDir, v.st.project)
	if err != nil {
		v.flash = styleErr.Render("move: " + err.Error())
	} else {
		v.flash = styleOK.Render(fmt.Sprintf("moved %q → %s", v.moveName, to))
		v.rebuild()
	}
}

func (v *skillView) toggle(s skills.Skill) {
	if s.Enabled {
		v.st.settings.SetSkillOverride(s.Name, "off")
		v.flash = styleWarn.Render(fmt.Sprintf("disabled %s (unsaved)", s.Name))
	} else {
		v.st.settings.RemoveSkillOverride(s.Name)
		v.flash = styleOK.Render(fmt.Sprintf("enabled %s (unsaved)", s.Name))
	}
	v.st.dirtySettings = true
}

func (v *skillView) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Skills (%d)", len(v.rows))
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
		b.WriteString(styleDim.Render("  (no skills match — press 'n' to create one)"))
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
		s := v.rows[i]
		mark := "[x]"
		if !s.Enabled {
			mark = "[ ]"
		}
		src := string(s.Scope)
		if s.Scope == skills.ScopePlugin {
			pname, _ := config.ParsePluginID(s.PluginID)
			src = "P:" + pname
		}
		desc := assets.Truncate(s.Description, 60)
		line := fmt.Sprintf("  %s %-30s  %-22s  %s", mark, s.Name, src, styleDim.Render(desc))
		if i == v.index {
			b.WriteString(styleSelected.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (v *skillView) resize(w, h int) { v.w, v.h = w, h }

func (v *skillView) helpText() string {
	if v.moveActive {
		return "[u]ser / [p]roject / esc: cancel"
	}
	return "space: toggle  n: new  m: move  d: delete  A/N: bulk  /: filter"
}

func (v *skillView) capturingInput() bool { return v.filterActive || v.inputActive }
