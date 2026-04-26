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

// skillView lists every discovered skill and supports space-toggle via skillOverrides.
type skillView struct {
	st    *state
	rows  []skills.Skill
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool
	filterText   string

	flash string
}

func newSkillView(st *state) *skillView {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.CharLimit = 64
	v := &skillView{st: st, filter: ti}
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
	if v.filterActive {
		b.WriteString(v.filter.View() + "\n")
	}
	if len(v.rows) == 0 {
		b.WriteString(styleDim.Render("  (no skills match)"))
		return b.String()
	}
	// Scroll window
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
	return "space: toggle  A/N: bulk  /: filter  c: clear"
}

func (v *skillView) capturingInput() bool { return v.filterActive }
