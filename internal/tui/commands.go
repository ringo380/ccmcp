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

// commandView lists discovered slash commands; `!` toggles a conflict-only view,
// `r` opens an inline resolution picker for conflicted rows.
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
	conflictMap   map[string]commands.Conflict // effective → conflict

	// resolve picker: active when user presses 'r' on a conflicted row
	resolveActive    bool
	resolveConflict  *commands.Conflict
	resolveCanDisable bool // true when Kind == SkillVsCommand

	flash string
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
	v.conflictMap = map[string]commands.Conflict{}
	for _, c := range conflicts {
		v.conflictMap[c.Effective] = c
	}
	var filtered []commands.Command
	for _, c := range all {
		if v.conflictsOnly {
			_, byEff := v.conflictMap[c.Effective]
			_, bySlug := v.conflictMap[c.Slug]
			if !byEff && !bySlug {
				continue
			}
		}
		if v.filterText != "" {
			needle := strings.ToLower(v.filterText)
			if !strings.Contains(strings.ToLower(c.Effective), needle) {
				continue
			}
		}
		filtered = append(filtered, c)
	}
	v.rows = filtered
	if v.index >= len(v.rows) {
		v.index = len(v.rows) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
}

func (v *commandView) update(msg tea.Msg) tea.Cmd {
	// Resolve picker mode.
	if v.resolveActive {
		k, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		switch k.String() {
		case "s":
			if v.resolveCanDisable {
				v.applyResolveDisableSkill()
			}
		case "i":
			v.applyResolveIgnore()
		case "esc":
			v.resolveActive = false
			v.resolveConflict = nil
			v.flash = styleDim.Render("resolve cancelled")
		}
		return nil
	}

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
	case "!":
		v.conflictsOnly = !v.conflictsOnly
		v.rebuild()
	case "r":
		if len(v.rows) == 0 {
			return nil
		}
		cur := v.rows[v.index]
		cf, ok := v.conflictMap[cur.Effective]
		if !ok {
			cf, ok = v.conflictMap[cur.Slug]
		}
		if !ok {
			v.flash = styleDim.Render("no conflict on this row")
			return nil
		}
		v.resolveConflict = &cf
		v.resolveCanDisable = cf.Kind == commands.SkillVsCommand
		v.resolveActive = true
		if v.resolveCanDisable {
			v.flash = styleDim.Render(fmt.Sprintf("resolve /%s: [s]kill off / [i]gnore / esc", cur.Effective))
		} else {
			v.flash = styleDim.Render(fmt.Sprintf("resolve /%s: [i]gnore / esc", cur.Effective))
		}
	}
	return nil
}

func (v *commandView) applyResolveDisableSkill() {
	c := v.resolveConflict
	v.resolveActive = false
	v.resolveConflict = nil
	v.st.settings.SetSkillOverride(c.Effective, "off")
	if err := config.Backup(v.st.settings.Path, v.st.paths.BackupsDir); err != nil {
		v.flash = styleErr.Render("backup: " + err.Error())
		return
	}
	if err := v.st.settings.Save(); err != nil {
		v.flash = styleErr.Render("save: " + err.Error())
		return
	}
	v.flash = styleOK.Render(fmt.Sprintf("skill %q disabled via skillOverrides", c.Effective))
	v.rebuild()
}

func (v *commandView) applyResolveIgnore() {
	c := v.resolveConflict
	v.resolveActive = false
	v.resolveConflict = nil
	ig, err := commands.LoadIgnores(v.st.paths.Ignores)
	if err != nil {
		v.flash = styleErr.Render("load ignores: " + err.Error())
		return
	}
	if ig.Has(c.Effective) {
		v.flash = styleDim.Render(fmt.Sprintf("%q was already ignored", c.Effective))
		return
	}
	ig.Add(c.Effective)
	if err := ig.Save(); err != nil {
		v.flash = styleErr.Render("save ignores: " + err.Error())
		return
	}
	v.flash = styleOK.Render(fmt.Sprintf("/%s added to ignore list", c.Effective))
	v.rebuild()
}

func (v *commandView) render() string {
	var b strings.Builder
	mode := ""
	if v.conflictsOnly {
		mode = styleWarn.Render("  [conflicts only]")
	}
	fmt.Fprintf(&b, "Commands (%d)%s", len(v.rows), mode)
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
		_, cfEff := v.conflictMap[c.Effective]
		_, cfSlug := v.conflictMap[c.Slug]
		warn := "   "
		if cfEff || cfSlug {
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
	if v.resolveActive {
		if v.resolveCanDisable {
			return "[s]kill off / [i]gnore / esc: cancel"
		}
		return "[i]gnore / esc: cancel"
	}
	return "/: filter  !: conflicts only  r: resolve  c: clear"
}

func (v *commandView) capturingInput() bool { return v.filterActive }
