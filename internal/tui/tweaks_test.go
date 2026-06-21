package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// pressKey sends a single key to the model and returns it for chaining.
func pressKey(m *model, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestHeaderHasEightTabsAndTweaks(t *testing.T) {
	if len(tabs) != 8 {
		t.Fatalf("want 8 top-level tabs, got %d", len(tabs))
	}
	last := tabs[len(tabs)-1]
	if last.label != "Tweaks" {
		t.Fatalf("last tab = %q, want Tweaks", last.label)
	}
	for _, tb := range tabs {
		if tb.label == "Summary" || tb.label == "Doctor" || tb.label == "Profiles" {
			t.Fatalf("%q should be folded into Tweaks, not top-level", tb.label)
		}
	}
}

func TestTKeyJumpsToTweaksOnSettings(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	m.width, m.height = 100, 40
	pressKey(m, "t")
	if m.tab != tabTweaks {
		t.Fatalf("after t: tab=%d want tabTweaks", m.tab)
	}
	if m.tweaks.sub != subSettings {
		t.Fatalf("landing sub=%d want subSettings", m.tweaks.sub)
	}
}

func TestTweaksSubTabCycling(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	m.width, m.height = 100, 40
	pressKey(m, "t")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if m.tweaks.sub != subMaintenance {
		t.Fatalf("after ]: sub=%d want subMaintenance", m.tweaks.sub)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if m.tweaks.sub != subSettings {
		t.Fatalf("after [: sub=%d want subSettings", m.tweaks.sub)
	}
	// wrap backwards from Settings -> Profiles
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if m.tweaks.sub != subProfiles {
		t.Fatalf("wrap back: sub=%d want subProfiles", m.tweaks.sub)
	}
}

func TestTweaksHeaderRendersSubTabBar(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	m.width, m.height = 100, 40
	pressKey(m, "t")
	out := m.View()
	for _, lbl := range []string{"Settings", "Maintenance", "Summary", "Doctor", "Profiles"} {
		if !strings.Contains(out, lbl) {
			t.Fatalf("sub-tab bar missing %q", lbl)
		}
	}
}
