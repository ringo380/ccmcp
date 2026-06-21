package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestGlobalSearchOpensAndFilters opens the overlay from a non-default tab,
// types a query, and asserts the result list narrows to matching rows with a
// tab badge.
func TestGlobalSearchOpensAndFilters(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Open from the Tweaks/Profiles sub-tab to prove it works from any tab.
	out := drive(m, "t", "]", "]", "]", "]", "ctrl+g")
	if !m.globalSearch.active {
		t.Fatal("ctrl+g should activate the global search overlay")
	}
	if !strings.Contains(stripANSI(out), "Global search") {
		t.Errorf("overlay header missing; got:\n%s", out)
	}

	// Type a query that only matches the user-scope MCP fixture. Fresh model so
	// the keys aren't replayed into an already-open overlay.
	st2, _ := buildState(t)
	m2 := newModel(st2)
	out = drive(m2, "t", "]", "]", "]", "]", "ctrl+g", "u", "s", "e", "r")
	clean := stripANSI(out)
	if !strings.Contains(clean, "user-only") {
		t.Errorf("expected user-only in results; got:\n%s", clean)
	}
	if !strings.Contains(clean, "mcps") {
		t.Errorf("expected mcps badge in results; got:\n%s", clean)
	}
	// stashed-* / profile rows shouldn't match "user".
	if strings.Contains(clean, "stashed-b") {
		t.Errorf("stashed-b should not match query 'user'; got:\n%s", clean)
	}
}

// TestGlobalSearchEnterJumpsToTab selects a result and confirms the model
// switches to that result's tab and closes the overlay.
func TestGlobalSearchEnterJumpsToTab(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Start on Tweaks/Profiles, search for an MCP, select it.
	drive(m, "t", "]", "]", "]", "]", "ctrl+g", "u", "s", "e", "r")
	var im tea.Model = m
	im, _ = im.Update(key("enter"))
	mm := im.(*model)

	if mm.globalSearch.active {
		t.Error("enter should close the overlay")
	}
	if mm.tab != tabMCPs {
		t.Errorf("enter should switch to the MCPs tab; got tab %d", mm.tab)
	}
	// The MCPs view should have positioned its cursor on a row.
	vis := mm.mcps.visibleRows()
	if mm.mcps.index < 0 || mm.mcps.index >= len(vis) {
		t.Fatalf("mcps cursor out of range: %d (rows=%d)", mm.mcps.index, len(vis))
	}
	if got := vis[mm.mcps.index].Name; got != "user-only" {
		t.Errorf("cursor should land on user-only; got %q", got)
	}
}

// TestGlobalSearchEscCloses confirms esc closes the overlay without changing
// the active tab.
func TestGlobalSearchEscCloses(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	drive(m, "t", "]", "]", "]", "]") // navigate to Tweaks -> Profiles sub-tab
	drive(m, "ctrl+g", "d", "e", "v")
	if !m.globalSearch.active {
		t.Fatal("overlay should be active before esc")
	}
	var im tea.Model = m
	im, _ = im.Update(key("esc"))
	mm := im.(*model)
	if mm.globalSearch.active {
		t.Error("esc should close the overlay")
	}
	if mm.tab != tabTweaks {
		t.Errorf("esc should leave the active tab unchanged; got %d", mm.tab)
	}
}

// TestGlobalSearchIndexesMultipleTabs confirms entries are collected from more
// than one tab (the cross-tab promise of the feature).
func TestGlobalSearchIndexesMultipleTabs(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	drive(m, "ctrl+g")

	tabsSeen := map[tabID]bool{}
	for _, e := range m.globalSearch.all {
		tabsSeen[e.tab] = true
	}
	if len(tabsSeen) < 2 {
		t.Errorf("expected entries from multiple tabs; saw %d", len(tabsSeen))
	}
	// MCPs has "user-only"; Tweaks hub aggregates profiles (has "dev").
	if !tabsSeen[tabMCPs] || !tabsSeen[tabTweaks] {
		t.Errorf("expected both mcps and tweaks indexed; saw %v", tabsSeen)
	}
}

func TestSubsequenceMatch(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        bool
	}{
		{"dropbox", "dbx", true},
		{"dropbox", "drop", true},
		{"dropbox", "xbd", false},
		{"anything", "", true},
		{"abc", "abcd", false},
	}
	for _, c := range cases {
		if got := subsequenceMatch(c.hay, c.needle); got != c.want {
			t.Errorf("subsequenceMatch(%q,%q)=%v want %v", c.hay, c.needle, got, c.want)
		}
	}
}
