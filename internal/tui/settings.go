package tui

import tea "github.com/charmbracelet/bubbletea"

// settingsView is the Settings sub-view of the Tweaks tab. Fleshed out in a
// later task; this stub satisfies the view interface so the hub compiles.
type settingsView struct {
	st    *state
	w, h  int
	flash string
}

func newSettingsView(st *state) *settingsView { return &settingsView{st: st} }

func (v *settingsView) update(msg tea.Msg) tea.Cmd { return nil }
func (v *settingsView) render() string             { return "Settings (coming soon)" }
func (v *settingsView) resize(w, h int)            { v.w, v.h = w, h }
func (v *settingsView) helpText() string           { return "" }
func (v *settingsView) capturingInput() bool       { return false }
