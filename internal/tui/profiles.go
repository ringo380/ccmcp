package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// shareableProfile mirrors cmd.ShareableProfile without importing cmd/.
type shareableProfile struct {
	Version int            `json:"version"`
	Name    string         `json:"name"`
	MCPs    []string       `json:"mcps"`
	Configs map[string]any `json:"configs,omitempty"`
}

type profileView struct {
	st *state

	names []string
	index int
	top   int // scroll offset (first visible row)
	w, h  int

	input       textinput.Model
	inputActive bool
	inputAction string // "new" | "export" | "import"

	flash string
}

func newProfileView(st *state) *profileView {
	ti := textinput.New()
	ti.Prompt = "profile name: "
	ti.CharLimit = 128
	v := &profileView{st: st, input: ti}
	v.rebuild()
	return v
}

func (v *profileView) rebuild() {
	v.names = v.st.profiles.Names()
	if v.index >= len(v.names) {
		v.index = 0
	}
	v.top = 0
}

func (v *profileView) update(msg tea.Msg) tea.Cmd {
	if v.inputActive {
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter":
				val := strings.TrimSpace(v.input.Value())
				switch v.inputAction {
				case "new":
					if val != "" {
						mcps := v.st.cj.ProjectMCPNames(v.st.project)
						if len(mcps) == 0 {
							mcps = v.st.cj.UserMCPNames()
						}
						v.st.profiles.Set(val, mcps)
						v.st.dirtyProfiles = true
						v.flash = styleOK.Render(fmt.Sprintf("created profile %q (%d MCPs, unsaved)", val, len(mcps)))
						v.rebuild()
					}
				case "export":
					if val != "" {
						v.doExport(val)
					}
				case "import":
					if val != "" {
						v.doImport(val)
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
		v.input.Prompt = "profile name: "
		v.inputActive = true
		v.input.Focus()
		return textinput.Blink
	case "e":
		if len(v.names) == 0 {
			return nil
		}
		v.inputAction = "export"
		v.input.Prompt = "export to file: "
		v.input.SetValue(v.names[v.index] + ".json")
		v.inputActive = true
		v.input.Focus()
		return textinput.Blink
	case "i":
		v.inputAction = "import"
		v.input.Prompt = "import from file: "
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

func (v *profileView) doExport(path string) {
	if len(v.names) == 0 {
		v.flash = styleErr.Render("no profile selected")
		return
	}
	name := v.names[v.index]
	mcps, ok := v.st.profiles.MCPs(name)
	if !ok {
		v.flash = styleErr.Render(fmt.Sprintf("profile %q not found", name))
		return
	}
	sp := shareableProfile{Version: 1, Name: name, MCPs: mcps}
	b, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		v.flash = styleErr.Render("marshal: " + err.Error())
		return
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		v.flash = styleErr.Render("write: " + err.Error())
		return
	}
	v.flash = styleOK.Render(fmt.Sprintf("exported %q to %s", name, path))
}

func (v *profileView) doImport(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		v.flash = styleErr.Render("read: " + err.Error())
		return
	}
	var sp shareableProfile
	if err := json.Unmarshal(data, &sp); err != nil {
		v.flash = styleErr.Render("parse: " + err.Error())
		return
	}
	if sp.Name == "" {
		v.flash = styleErr.Render("invalid profile file: missing name")
		return
	}
	v.st.profiles.Set(sp.Name, sp.MCPs)
	v.st.dirtyProfiles = true
	v.flash = styleOK.Render(fmt.Sprintf("imported %q (%d MCPs, unsaved)", sp.Name, len(sp.MCPs)))
	v.rebuild()
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
	if v.index >= len(v.names) {
		v.index = len(v.names) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
	// Single line per row; clamp the scroll window so it never overflows the body.
	headerLines := strings.Count(b.String(), "\n")
	listHeight := v.h - headerLines - 1
	if listHeight < 3 {
		listHeight = 3
	}
	if v.index < v.top {
		v.top = v.index
	}
	if v.index >= v.top+listHeight {
		v.top = v.index - listHeight + 1
	}
	if v.top > len(v.names)-listHeight {
		v.top = len(v.names) - listHeight
	}
	if v.top < 0 {
		v.top = 0
	}
	end := v.top + listHeight
	if end > len(v.names) {
		end = len(v.names)
	}
	for i := v.top; i < end; i++ {
		name := v.names[i]
		mcps, _ := v.st.profiles.MCPs(name)
		line := fmt.Sprintf("%-24s  %s", name, styleDim.Render(strings.Join(mcps, ", ")))
		if i == v.index {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	if len(v.names) > listHeight {
		b.WriteString(styleDim.Render(fmt.Sprintf("  [%d-%d of %d]", v.top+1, end, len(v.names))))
	}
	return b.String()
}

func (v *profileView) resize(w, h int) { v.w, v.h = w, h }

func (v *profileView) helpText() string {
	return "enter: apply  n: new  e: export  i: import  d: delete"
}

func (v *profileView) capturingInput() bool { return v.inputActive }
