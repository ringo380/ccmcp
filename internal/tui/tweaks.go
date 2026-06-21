package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type tweaksSub int

const (
	subSettings tweaksSub = iota
	subMaintenance
	subSummary
	subDoctor
	subProfiles
)

var tweaksSubLabels = []string{"Settings", "Maintenance", "Summary", "Doctor", "Profiles"}

// tweaksView is the Tweaks tab: a hub that hosts settings + maintenance ops and
// the folded-in Summary/Doctor/Profiles views. Sub-views are switched with
// [ / ] (and the arrow aliases); the active sub-view keeps all its own keys.
type tweaksView struct {
	st  *state
	sub tweaksSub

	settings *settingsView
	ops      *opsView
	summary  *summaryView
	doctor   *doctorView
	profiles *profileView

	w, h  int
	flash string
}

func newTweaksView(st *state) *tweaksView {
	return &tweaksView{
		st:       st,
		sub:      subSettings,
		settings: newSettingsView(st),
		ops:      newOpsView(st),
		summary:  newSummaryView(st),
		doctor:   newDoctorView(st),
		profiles: newProfileView(st),
	}
}

func (v *tweaksView) activeSub() view {
	switch v.sub {
	case subSettings:
		return v.settings
	case subMaintenance:
		return v.ops
	case subSummary:
		return v.summary
	case subDoctor:
		return v.doctor
	case subProfiles:
		return v.profiles
	}
	return v.settings
}

func (v *tweaksView) update(msg tea.Msg) tea.Cmd {
	// Sub-tab switching is owned by the hub - but only when the active sub-view
	// is not capturing text input (so [ / ] typed into a filter/name field reach
	// the field instead of flipping sub-tabs).
	if k, ok := msg.(tea.KeyMsg); ok && !v.activeSub().capturingInput() {
		switch k.String() {
		case "]", "right":
			v.sub = (v.sub + 1) % tweaksSub(len(tweaksSubLabels))
			return nil
		case "[", "left":
			v.sub = (v.sub + tweaksSub(len(tweaksSubLabels)) - 1) % tweaksSub(len(tweaksSubLabels))
			return nil
		}
	}
	cmd := v.activeSub().update(msg)
	v.harvestFlash()
	return cmd
}

// harvestFlash pulls a flash set by the active sub-view up to the hub so the
// model's status-line plumbing (which reads v.flash) surfaces it.
func (v *tweaksView) harvestFlash() {
	switch v.sub {
	case subSettings:
		if v.settings.flash != "" {
			v.flash, v.settings.flash = v.settings.flash, ""
		}
	case subMaintenance:
		if v.ops.flash != "" {
			v.flash, v.ops.flash = v.ops.flash, ""
		}
	case subSummary:
		if v.summary.flash != "" {
			v.flash, v.summary.flash = v.summary.flash, ""
		}
	case subDoctor:
		if v.doctor.flash != "" {
			v.flash, v.doctor.flash = v.doctor.flash, ""
		}
	case subProfiles:
		if v.profiles.flash != "" {
			v.flash, v.profiles.flash = v.profiles.flash, ""
		}
	}
}

// routeFix forwards an origin-tagged async message (fixDone/chat/stream) to the
// summary or doctor sub-view that started it, regardless of the focused sub-tab.
// Returns (cmd, true) when handled. The doctor/summary views are the only
// execFixCmd callers, so other origins are not handled here.
func (v *tweaksView) routeFix(origin tabID, msg tea.Msg) (tea.Cmd, bool) {
	switch origin {
	case tabDoctor:
		cmd := v.doctor.update(msg)
		if v.doctor.flash != "" {
			v.flash, v.doctor.flash = v.doctor.flash, ""
		}
		return cmd, true
	case tabSummary:
		cmd := v.summary.update(msg)
		if v.summary.flash != "" {
			v.flash, v.summary.flash = v.summary.flash, ""
		}
		return cmd, true
	}
	return nil, false
}

func (v *tweaksView) render() string {
	var b strings.Builder
	for i, lbl := range tweaksSubLabels {
		style := styleTab
		if tweaksSub(i) == v.sub {
			style = styleTabActive
		}
		b.WriteString(style.Render(lbl))
	}
	b.WriteString("\n")
	b.WriteString(v.activeSub().render())
	return b.String()
}

// resize reserves one line for the sub-tab bar, then sizes the active sub-view.
func (v *tweaksView) resize(w, h int) {
	v.w, v.h = w, h
	sub := h - 1
	if sub < 0 {
		sub = 0
	}
	v.settings.resize(w, sub)
	v.ops.resize(w, sub)
	v.summary.resize(w, sub)
	v.doctor.resize(w, sub)
	v.profiles.resize(w, sub)
}

func (v *tweaksView) helpText() string {
	base := "[ / ]: section"
	if sub := v.activeSub().helpText(); sub != "" {
		return sub + "  |  " + base
	}
	return base
}

func (v *tweaksView) capturingInput() bool { return v.activeSub().capturingInput() }

// searchEntries aggregates entries from the sub-views that implement searchProvider.
// All results are tagged with tabTweaks (the containing top-level tab).
func (v *tweaksView) searchEntries() []searchEntry {
	var out []searchEntry
	out = append(out, v.profiles.searchEntries()...)
	out = append(out, v.summary.searchEntries()...)
	out = append(out, v.doctor.searchEntries()...)
	return out
}

// focusSearch navigates to the appropriate sub-view and focuses the target key.
// It tries each sub-view in turn (profiles -> summary -> doctor).
func (v *tweaksView) focusSearch(key string) {
	for _, n := range v.profiles.names {
		if n == key {
			v.sub = subProfiles
			v.profiles.focusSearch(key)
			return
		}
	}
	// Try summary - rebuild rows first
	for _, r := range v.summary.fixableRows() {
		if summaryRowKey(r) == key {
			v.sub = subSummary
			v.summary.focusSearch(key)
			return
		}
	}
	// Try doctor
	v.sub = subDoctor
	v.doctor.focusSearch(key)
}
