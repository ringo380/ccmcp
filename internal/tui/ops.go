package tui

import tea "github.com/charmbracelet/bubbletea"

// opsView is the Maintenance sub-view of the Tweaks tab. Fleshed out in a later
// task; this stub satisfies the view interface so the hub compiles.
type opsView struct {
	st    *state
	w, h  int
	flash string
}

func newOpsView(st *state) *opsView { return &opsView{st: st} }

func (v *opsView) update(msg tea.Msg) tea.Cmd { return nil }
func (v *opsView) render() string             { return "Maintenance (coming soon)" }
func (v *opsView) resize(w, h int)            { v.w, v.h = w, h }
func (v *opsView) helpText() string           { return "" }
func (v *opsView) capturingInput() bool       { return false }
