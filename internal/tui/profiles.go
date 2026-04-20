package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type profileView struct {
	st *state

	names []string
	index int
	w, h  int

	input       textinput.Model
	inputActive bool
	inputAction string // "new"

	flash string
}

func newProfileView(st *state) *profileView {
	ti := textinput.New()
	ti.Prompt = "profile name: "
	ti.CharLimit = 64
	v := &profileView{st: st, input: ti}
	v.rebuild()
	return v
}

func (v *profileView) rebuild() {
	v.names = v.st.profiles.Names()
	if v.index >= len(v.names) {
		v.index = 0
	}
}

func (v *profileView) update(msg tea.Msg) tea.Cmd {
	if v.inputActive {
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter":
				name := strings.TrimSpace(v.input.Value())
				if name != "" && v.inputAction == "new" {
					// snapshot current project MCPs as the profile's list
					mcps := v.st.cj.ProjectMCPNames(v.st.project)
					if len(mcps) == 0 {
						mcps = v.st.cj.UserMCPNames()
					}
					v.st.profiles.Set(name, mcps)
					v.st.dirtyProfiles = true
					v.flash = styleOK.Render(fmt.Sprintf("created profile %q (%d MCPs, unsaved)", name, len(mcps)))
					v.rebuild()
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
		if v.index < len(v.names)-1 {
			v.index++
		}
	case "n":
		v.inputAction = "new"
		v.inputActive = true
		v.input.Focus()
		return textinput.Blink
	case "d":
		if len(v.names) == 0 {
			return nil
		}
		name := v.names[v.index]
		if v.st.profiles.Delete(name) {
			v.st.dirtyProfiles = true
			v.flash = styleDim.Render("deleted " + name + " (unsaved)")
		}
		v.rebuild()
	case "enter", " ":
		if len(v.names) == 0 {
			return nil
		}
		name := v.names[v.index]
		mcps, _ := v.st.profiles.MCPs(name)
		v.st.cj.ClearProjectMCPs(v.st.project)
		var applied, missing []string
		for _, n := range mcps {
			cfg, ok := pickConfig(v.st, n, mcpRow{})
			if !ok {
				missing = append(missing, n)
				continue
			}
			v.st.cj.SetProjectMCP(v.st.project, n, cfg)
			applied = append(applied, n)
		}
		v.st.dirtyClaude = true
		if len(missing) > 0 {
			v.flash = styleWarn.Render(fmt.Sprintf("applied %d, missing %d: %s (unsaved)", len(applied), len(missing), strings.Join(missing, ",")))
		} else {
			v.flash = styleOK.Render(fmt.Sprintf("applied profile %q (%d MCPs, unsaved)", name, len(applied)))
		}
	}
	return nil
}

func (v *profileView) render() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Profiles (%d)\n", len(v.names)))
	if v.inputActive {
		b.WriteString(v.input.View() + "\n")
	}
	if len(v.names) == 0 {
		b.WriteString(styleDim.Render("  (no profiles; press 'n' to create one from the current project MCPs)"))
		return b.String()
	}
	for i, name := range v.names {
		mcps, _ := v.st.profiles.MCPs(name)
		line := fmt.Sprintf("%-24s  %s", name, styleDim.Render(strings.Join(mcps, ", ")))
		if i == v.index {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (v *profileView) resize(w, h int) { v.w, v.h = w, h }

func (v *profileView) helpText() string {
	return "enter: apply  n: new  d: delete"
}

func (v *profileView) capturingInput() bool { return v.inputActive }
