package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fillPluginRowsWithRemote is fillPluginRows plus claude.ai rows, which make the
// render loop emit a "─── Remote (claude.ai) ───" separator LINE on top of the
// row lines. fillPluginRows creates no remote rows, which is exactly why the
// existing geometry tests could not see the separator overflowing the budget.
func fillPluginRowsWithRemote(v *pluginView, local, remote int) {
	rows := make([]pluginRowView, 0, local+remote)
	for i := 0; i < local; i++ {
		rows = append(rows, pluginRowView{
			ID:        fmt.Sprintf("filler-%02d@mkt", i),
			Version:   "1.0",
			Known:     true,
			Installed: true,
			Enabled:   true,
		})
	}
	for i := 0; i < remote; i++ {
		rows = append(rows, pluginRowView{
			ID:        fmt.Sprintf("claude.ai Remote%02d", i),
			IsRemote:  true,
			RemoteKey: fmt.Sprintf("claude.ai Remote%02d", i),
		})
	}
	v.rows = rows
	v.index = 0
	v.top = 0
}

// physicalRows returns the number of terminal rows body occupies at width w,
// and fails if any single line would wrap (which neither headerLines nor
// model.View()'s clamp - both logical-line counters - can see).
func physicalRows(t *testing.T, body string, w int) int {
	t.Helper()
	total := 0
	for i, ln := range strings.Split(body, "\n") {
		cols := lipgloss.Width(ln)
		if cols > w {
			t.Fatalf("line %d is %d columns wide at w=%d and will wrap: %q", i, cols, w, ln)
		}
		rows := 1
		if cols > 0 {
			rows = (cols + w - 1) / w
		}
		total += rows
	}
	return total
}

// TestPluginsTabBudgetsTheRemoteSeparator is the regression for the separator
// line the list budget did not pay for. With the cursor at the bottom of a list
// that has remote rows, the loop emitted listHeight rows PLUS the separator -
// one line more than fits - and model.View() clamped from the BOTTOM, taking
// the "[a-b of N]" scroll indicator and the last row with it.
func TestPluginsTabBudgetsTheRemoteSeparator(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	drive(m, "2")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	fillPluginRowsWithRemote(m.plugins, 40, 2)

	visible := m.plugins.visibleRows()
	m.plugins.index = len(visible) - 1 // cursor at the bottom, where the separator is in view

	body := stripANSI(m.plugins.render())
	avail := m.height - reservedHeight
	got := physicalRows(t, body, 120)
	if got > avail {
		t.Fatalf("body emitted %d physical rows but only %d fit; model.View() would clamp %d off the bottom:\n%s",
			got, avail, got-avail, body)
	}
	if !strings.Contains(body, "Remote (claude.ai)") {
		t.Fatalf("the remote separator must still render:\n%s", body)
	}
	// The two things the bottom clamp ate: the scroll indicator and the last row.
	if !strings.Contains(body, fmt.Sprintf("of %d]", len(visible))) {
		t.Fatalf("the [a-b of N] scroll indicator must survive:\n%s", body)
	}
	if !strings.Contains(body, "claude.ai Remote01") {
		t.Fatalf("the selected last row must survive:\n%s", body)
	}
}

// TestPluginsTitleIsClampedToWidth pins finding #2 numerically: the tab TITLE
// was the one header line never passed through fitWidth, so at 80 columns a
// long count summary plus an update badge rendered 92 columns wide and wrapped
// into a physical row the budget never allocated.
func TestPluginsTitleIsClampedToWidth(t *testing.T) {
	for _, w := range []int{80, 100} {
		t.Run(fmt.Sprintf("w%d", w), func(t *testing.T) {
			st, _ := buildState(t)
			m := newModel(st)
			drive(m, "2")
			m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
			fillPluginRowsWithRemote(m.plugins, 45, 2)
			for i := 0; i < 3; i++ {
				m.plugins.rows[i].Outdated = true
			}

			body := stripANSI(m.plugins.render())
			lines := strings.Split(body, "\n")
			if cols := lipgloss.Width(lines[0]); cols > w {
				t.Fatalf("title is %d columns at w=%d: %q", cols, w, lines[0])
			}
			got := physicalRows(t, body, w)
			if got != len(lines) {
				t.Fatalf("w=%d: %d logical lines rendered as %d physical rows", w, len(lines), got)
			}
			if avail := m.height - reservedHeight; got > avail {
				t.Fatalf("w=%d: %d physical rows exceeds the %d-row budget", w, got, avail)
			}
		})
	}
}

// TestMCPsTitleIsClampedToWidth is the same assertion for the MCPs tab, whose
// title carries a scope badge plus a scope description plus four counts.
func TestMCPsTitleIsClampedToWidth(t *testing.T) {
	for _, w := range []int{80, 100} {
		t.Run(fmt.Sprintf("w%d", w), func(t *testing.T) {
			st, _ := buildState(t)
			m := newModel(st)
			m.Update(tea.WindowSizeMsg{Width: w, Height: 30})

			body := stripANSI(m.mcps.render())
			lines := strings.Split(body, "\n")
			if cols := lipgloss.Width(lines[0]); cols > w {
				t.Fatalf("title is %d columns at w=%d: %q", cols, w, lines[0])
			}
			got := physicalRows(t, body, w)
			if got != len(lines) {
				t.Fatalf("w=%d: %d logical lines rendered as %d physical rows", w, len(lines), got)
			}
		})
	}
}

// TestMCPsContextCaptionIsQualified pins finding #7: the identical "per-turn
// context" caption meant different quantities on the two tabs - the whole asset
// estimate on Plugins, MCP tool schemas alone (structurally zero in Phase 1) on
// MCPs - so switching tabs showed a figure and then "-" under the same label.
func TestMCPsContextCaptionIsQualified(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})

	out := stripANSI(m.mcps.render())
	if !strings.Contains(out, "per-turn context (MCP tool schemas)") {
		t.Fatalf("the MCPs caption must say which quantity it reports:\n%s", out)
	}
}
