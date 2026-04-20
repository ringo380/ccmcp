package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/config"
)

type pluginView struct {
	st *state

	rows  []pluginRowView
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool

	showOnly string // "" | "enabled" | "disabled"

	flash string
}

type pluginRowView struct {
	ID        string
	Enabled   bool
	Known     bool
	Installed bool
	Version   string
}

func newPluginView(st *state) *pluginView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	v := &pluginView{st: st, filter: ti}
	v.rebuild()
	return v
}

func (v *pluginView) rebuild() {
	installedIdx := map[string]config.InstalledPlugin{}
	for _, ip := range v.st.installed.List() {
		installedIdx[ip.ID] = ip
	}
	seen := map[string]bool{}
	var rows []pluginRowView
	for _, e := range v.st.settings.PluginEntries() {
		seen[e.ID] = true
		ip := installedIdx[e.ID]
		rows = append(rows, pluginRowView{ID: e.ID, Enabled: e.Enabled, Known: true, Installed: ip.InstallPath != "", Version: ip.Version})
	}
	for _, ip := range v.st.installed.List() {
		if seen[ip.ID] {
			continue
		}
		rows = append(rows, pluginRowView{ID: ip.ID, Installed: true, Version: ip.Version})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	v.rows = rows
	if v.index >= len(rows) {
		v.index = 0
	}
}

func (v *pluginView) update(msg tea.Msg) tea.Cmd {
	if v.filterActive {
		var cmd tea.Cmd
		v.filter, cmd = v.filter.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter", "esc":
				v.filterActive = false
				v.filter.Blur()
			}
		}
		return cmd
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	visible := v.visibleRows()
	switch key.String() {
	case "up", "k":
		if v.index > 0 {
			v.index--
		}
	case "down", "j":
		if v.index < len(visible)-1 {
			v.index++
		}
	case "g":
		v.index = 0
	case "G":
		v.index = len(visible) - 1
	case "pgup":
		v.index -= 10
		if v.index < 0 {
			v.index = 0
		}
	case "pgdn":
		v.index += 10
		if v.index >= len(visible) {
			v.index = len(visible) - 1
		}
	case " ":
		if len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		newState := !r.Enabled
		v.st.settings.SetPluginEnabled(r.ID, newState)
		v.st.dirtySettings = true
		// Toggling a plugin may change the set of plugin-registered MCPs — rescan so the
		// MCPs tab reflects reality immediately.
		v.st.rescanPluginMCPs()
		if newState {
			v.flash = styleOK.Render(r.ID + " → enabled")
		} else {
			v.flash = styleDim.Render(r.ID + " → disabled")
		}
		v.rebuild()
	case "f":
		switch v.showOnly {
		case "":
			v.showOnly = "enabled"
		case "enabled":
			v.showOnly = "disabled"
		default:
			v.showOnly = ""
		}
	case "/":
		v.filterActive = true
		v.filter.Focus()
		return textinput.Blink
	case "c":
		v.filter.SetValue("")
	case "A":
		for _, r := range visible {
			if !r.Enabled {
				v.st.settings.SetPluginEnabled(r.ID, true)
			}
		}
		v.st.dirtySettings = true
		v.st.rescanPluginMCPs()
		v.flash = styleOK.Render(fmt.Sprintf("enabled %d plugins (unsaved)", len(visible)))
		v.rebuild()
	case "N":
		for _, r := range visible {
			if r.Enabled {
				v.st.settings.SetPluginEnabled(r.ID, false)
			}
		}
		v.st.dirtySettings = true
		v.st.rescanPluginMCPs()
		v.flash = styleDim.Render(fmt.Sprintf("disabled %d plugins (unsaved)", len(visible)))
		v.rebuild()
	}
	return nil
}

func (v *pluginView) visibleRows() []pluginRowView {
	q := strings.ToLower(strings.TrimSpace(v.filter.Value()))
	out := make([]pluginRowView, 0, len(v.rows))
	for _, r := range v.rows {
		switch v.showOnly {
		case "enabled":
			if !r.Enabled {
				continue
			}
		case "disabled":
			if r.Enabled || !r.Known {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(r.ID), q) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (v *pluginView) render() string {
	visible := v.visibleRows()
	var enabled, disabled int
	for _, r := range v.rows {
		if r.Enabled {
			enabled++
		} else if r.Known {
			disabled++
		}
	}
	mode := "all"
	if v.showOnly != "" {
		mode = v.showOnly
	}
	title := fmt.Sprintf("Plugins — %s  (showing %d/%d ; %d enabled, %d disabled)", mode, len(visible), len(v.rows), enabled, disabled)
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	if v.filterActive || v.filter.Value() != "" {
		b.WriteString(v.filter.View() + "\n")
	}

	if len(visible) == 0 {
		b.WriteString(styleDim.Render("  (no entries)"))
		return b.String()
	}
	if v.index >= len(visible) {
		v.index = len(visible) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
	listHeight := v.h - 4
	if listHeight < 5 {
		listHeight = 5
	}
	if v.index < v.top {
		v.top = v.index
	}
	if v.index >= v.top+listHeight {
		v.top = v.index - listHeight + 1
	}
	end := v.top + listHeight
	if end > len(visible) {
		end = len(visible)
	}
	for i := v.top; i < end; i++ {
		r := visible[i]
		mark := "[ ]"
		switch {
		case r.Enabled:
			mark = styleOK.Render("[x]")
		case !r.Known:
			mark = styleDim.Render("[?]")
		}
		line := fmt.Sprintf("%s %s", mark, r.ID)
		if r.Version != "" {
			line += "  " + styleDim.Render("v"+r.Version)
		}
		if i == v.index {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	if len(visible) > listHeight {
		b.WriteString(styleDim.Render(fmt.Sprintf("  [%d-%d of %d]", v.top+1, end, len(visible))))
	}
	return b.String()
}

func (v *pluginView) resize(w, h int) { v.w, v.h = w, h }

func (v *pluginView) helpText() string {
	return "space: toggle  f: filter-mode  A/N: all on/off  /: search"
}

func (v *pluginView) capturingInput() bool { return v.filterActive }
