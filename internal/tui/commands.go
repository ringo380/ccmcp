package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/skills"
)

// commandView lists discovered slash commands; `!` toggles a conflict-only view.
type commandView struct {
	st    *state
	rows  []commands.Command
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool
	filterText   string

	conflictsOnly bool
	conflictSet   map[string]commands.ConflictKind
}

func newCommandView(st *state) *commandView {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.CharLimit = 64
	v := &commandView{st: st, filter: ti}
	v.rebuild()
	return v
}

func (v *commandView) rebuild() {
	all := commands.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	skls := skills.Discover(v.st.paths.ClaudeConfigDir, v.st.project, v.st.settings, v.st.installed, v.st.paths.PluginsDir)
	conflicts := commands.FindConflicts(all, skls)
	v.conflictSet = map[string]commands.ConflictKind{}
	for _, c := range conflicts {
		v.conflictSet[c.Effective] = c.Kind
	}
	v.rows = v.rows[:0]
	for _, c := range all {
		if v.conflictsOnly {
			if _, ok := v.conflictSet[c.Effective]; !ok {
				// Also consider slug matches (SkillVsCommand keys by skill name / command slug)
				if _, ok2 := v.conflictSet[c.Slug]; !ok2 {
					continue
				}
			}
		}
		if v.filterText != "" {
			needle := strings.ToLower(v.filterText)
			if !strings.Contains(strings.ToLower(c.Effective), needle) {
				continue
			}
		}
		v.rows = append(v.rows, c)
	}
	if v.index >= len(v.rows) {
		v.index = len(v.rows) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
}

func (v *commandView) update(msg tea.Msg) tea.Cmd {
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
	case "!":
		v.conflictsOnly = !v.conflictsOnly
		v.rebuild()
	}
	return nil
}

func (v *commandView) render() string {
	var b strings.Builder
	total := len(v.rows)
	mode := ""
	if v.conflictsOnly {
		mode = styleWarn.Render("  [conflicts only]")
	}
	fmt.Fprintf(&b, "Commands (%d)%s", total, mode)
	if v.filterText != "" {
		b.WriteString(styleDim.Render(fmt.Sprintf("  filter: %q", v.filterText)))
	}
	b.WriteString("\n")
	if v.filterActive {
		b.WriteString(v.filter.View() + "\n")
	}
	if len(v.rows) == 0 {
		b.WriteString(styleDim.Render("  (no commands match)"))
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
		c := v.rows[i]
		src := string(c.Scope)
		if c.Scope == commands.ScopePlugin {
			pname, _ := config.ParsePluginID(c.PluginID)
			src = "P:" + pname
		}
		warn := "   "
		if _, ok := v.conflictSet[c.Effective]; ok {
			warn = styleWarn.Render(" ⚠ ")
		} else if _, ok := v.conflictSet[c.Slug]; ok {
			warn = styleWarn.Render(" ⚠ ")
		}
		desc := assets.Truncate(c.Description, 50)
		line := fmt.Sprintf("%s /%-40s  %-22s  %s", warn, c.Effective, src, styleDim.Render(desc))
		if i == v.index {
			b.WriteString(styleSelected.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (v *commandView) resize(w, h int) { v.w, v.h = w, h }

func (v *commandView) helpText() string {
	return "/: filter  !: conflicts only  c: clear"
}

func (v *commandView) capturingInput() bool { return v.filterActive }
