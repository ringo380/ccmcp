package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/discovery"
)

// bodyLineCount returns the number of physical lines a view's render() emits.
func bodyLineCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// TestDiscoverListWindowsToHeight: with far more multi-line marketplace entries
// than fit on screen, the rendered body must not exceed the view height (each
// entry is two physical lines, so a row-count clamp would overflow).
func TestDiscoverListWindowsToHeight(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "4") // switch to Discover so resize() runs

	rows := make([]discovery.RemoteMarketplace, 0, 60)
	for i := 0; i < 60; i++ {
		rows = append(rows, discovery.RemoteMarketplace{
			Name:        fmt.Sprintf("market-%02d", i),
			Source:      "github",
			Repo:        fmt.Sprintf("owner/repo-%02d", i),
			Description: "a fairly long description that occupies the secondary line under each row",
			Tags:        []string{"agents", "commands", "productivity"},
			Stars:       1000 + i,
		})
	}
	m.discover.rows = rows
	m.discover.loaded = true
	m.discover.mode = modeList

	got := bodyLineCount(stripANSI(m.discover.render()))
	if got > m.discover.h {
		t.Fatalf("discover body has %d lines, exceeds view height %d", got, m.discover.h)
	}

	// Jump to the bottom; the selected row must still be inside the window.
	m.discover.index = len(rows) - 1
	out := stripANSI(m.discover.render())
	if got := bodyLineCount(out); got > m.discover.h {
		t.Fatalf("after G, discover body has %d lines, exceeds height %d", got, m.discover.h)
	}
	if !strings.Contains(out, "market-59") {
		t.Errorf("bottom row market-59 should be visible after jumping to end; got:\n%s", out)
	}
}

// TestProfilesWindowsToHeight: a long profile list must scroll-window rather than
// dumping every row past the bottom of the terminal.
func TestProfilesWindowsToHeight(t *testing.T) {
	st, _ := buildState(t)
	for i := 0; i < 60; i++ {
		st.profiles.Set(fmt.Sprintf("profile-%02d", i), []string{"a", "b"})
	}
	m := newModel(st)
	_ = drive(m, "8") // Profiles tab
	m.profiles.rebuild()

	got := bodyLineCount(stripANSI(m.profiles.render()))
	if got > m.profiles.h {
		t.Fatalf("profiles body has %d lines, exceeds view height %d", got, m.profiles.h)
	}

	// Bottom of the list stays windowed and visible.
	m.profiles.index = len(m.profiles.names) - 1
	out := stripANSI(m.profiles.render())
	if got := bodyLineCount(out); got > m.profiles.h {
		t.Fatalf("after jump, profiles body has %d lines, exceeds height %d", got, m.profiles.h)
	}
	if !strings.Contains(out, "profile-59") {
		t.Errorf("bottom profile should be visible after jump; got:\n%s", out)
	}
}

// TestFailuresPanelWindowsToHeight: the bulk-update failures panel (3+ lines per
// entry) must window so many failures don't overflow the terminal.
func TestFailuresPanelWindowsToHeight(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2") // Plugins tab

	fs := make([]bulkUpdateFailure, 0, 30)
	for i := 0; i < 30; i++ {
		fs = append(fs, bulkUpdateFailure{
			ID:   fmt.Sprintf("plug-%02d@mkt", i),
			Err:  "something went wrong during update",
			Hint: "retry or check network",
		})
	}
	m.plugins.lastFailures = fs
	m.plugins.lastFailuresLoaded = true
	m.plugins.mode = "failures"

	got := bodyLineCount(stripANSI(m.plugins.renderFailures()))
	if got > m.plugins.h {
		t.Fatalf("failures panel has %d lines, exceeds view height %d", got, m.plugins.h)
	}
}

// TestModelBodyNeverOverflows: the model-level safety clamp guarantees the full
// View() never exceeds the terminal height, even if a view miscounts.
func TestModelBodyNeverOverflows(t *testing.T) {
	st, _ := buildState(t)
	for i := 0; i < 80; i++ {
		st.profiles.Set(fmt.Sprintf("profile-%02d", i), []string{"x"})
	}
	m := newModel(st)
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	im, _ = im.Update(key("8")) // Profiles
	out := im.View()
	if got := bodyLineCount(out); got > 40 {
		t.Fatalf("full View() has %d lines, exceeds terminal height 40", got)
	}
}

// TestSummaryPanelPromptSurvivesShortTerminal: a tall fix-preview panel on a
// short terminal must not have its confirm prompt trimmed off by the model-level
// height clamp — the panel is capped so list+panel always fit the body.
func TestSummaryPanelPromptSurvivesShortTerminal(t *testing.T) {
	st, _ := buildState(t)
	longDiff := strings.Repeat("+ added line of a long diff\n", 40)
	for _, h := range []int{8, 10, 14, 20, 35} {
		v := newSummaryView(st)
		v.resize(120, h)
		v.pendingFix = &fixProposal{summary: "rewrite description"}
		v.previewBody = longDiff
		out := v.render()
		if got := bodyLineCount(out); got > h {
			t.Errorf("h=%d: render body %d lines exceeds height", h, got)
		}
		if !strings.Contains(stripANSI(out), "Apply?") {
			t.Errorf("h=%d: confirm prompt 'Apply?' missing from panel:\n%s", h, stripANSI(out))
		}
	}
}

// TestDoctorPanelPromptSurvivesShortTerminal: same guarantee for the Doctor view.
func TestDoctorPanelPromptSurvivesShortTerminal(t *testing.T) {
	st, _ := buildState(t)
	longDiff := strings.Repeat("- removed\n+ added\n", 30)
	for _, h := range []int{10, 14, 20, 35} {
		v := newDoctorView(st)
		v.resize(120, h)
		v.pendingFix = &fixProposal{summary: "fix issue"}
		v.previewDiff = longDiff
		out := v.render()
		if got := bodyLineCount(out); got > h {
			t.Errorf("h=%d: doctor render body %d lines exceeds height", h, got)
		}
		if !strings.Contains(stripANSI(out), "Apply?") {
			t.Errorf("h=%d: confirm prompt 'Apply?' missing from doctor panel:\n%s", h, stripANSI(out))
		}
	}
}

// TestProfilesJumpKeys: g/G move the cursor to the first/last profile.
func TestProfilesJumpKeys(t *testing.T) {
	st, _ := buildState(t)
	for i := 0; i < 40; i++ {
		st.profiles.Set(fmt.Sprintf("profile-%02d", i), []string{"a"})
	}
	m := newModel(st) // constructor's rebuild() populates names from seeded profiles
	_ = drive(m, "8", "G")
	if m.profiles.index != len(m.profiles.names)-1 {
		t.Fatalf("G should jump to last profile, got index %d", m.profiles.index)
	}

	m2 := newModel(st)
	_ = drive(m2, "8", "G", "g")
	if m2.profiles.index != 0 {
		t.Fatalf("g should jump to first profile, got index %d", m2.profiles.index)
	}
}

// TestWindowLines exercises the shared scroll helper directly.
func TestWindowLines(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	// Fits entirely.
	vis, top := windowLines(lines[:5], 0, 0, 10)
	if len(vis) != 5 || top != 0 {
		t.Fatalf("short list should return all lines, top 0; got %d lines top %d", len(vis), top)
	}
	// Cursor at bottom scrolls the window down and keeps it visible.
	vis, top = windowLines(lines, 19, 0, 10)
	if len(vis) != 10 {
		t.Fatalf("expected 10 visible lines, got %d", len(vis))
	}
	if vis[len(vis)-1] != "line-19" {
		t.Fatalf("cursor line should be visible at bottom, got last=%q", vis[len(vis)-1])
	}
	if top != 10 {
		t.Fatalf("expected top=10 for cursor at 19 with pageH 10, got %d", top)
	}
}
