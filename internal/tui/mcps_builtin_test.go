package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/config"
)

// findRow returns the first row whose display name matches, or nil.
func findRow(rows []mcpRow, name string) *mcpRow {
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i]
		}
	}
	return nil
}

// An off-by-default MCP listed in projects[<cwd>].enabledMcpServers (e.g. the
// "computer-use" Claude-in-Chrome built-in) has no source ccmcp enumerates. It must
// still surface as a visible, effective row so the effective view mirrors /mcp.
func TestTUIEnabledMcpServersBuiltinRow(t *testing.T) {
	st, _ := buildState(t)
	st.cj.AddProjectEnabledMcpServer(st.project, "computer-use")

	m := newModel(st)
	row := findRow(m.mcps.rows, "computer-use")
	if row == nil {
		t.Fatal("expected a built-in row for the enabled computer-use MCP")
	}
	if row.Source != config.SourceBuiltin {
		t.Errorf("source: want builtin, got %s", row.Source)
	}
	if !isEffective(*row) {
		t.Error("an enabled built-in should be effective")
	}
	if isHiddenInEffective(*row) {
		t.Error("an enabled built-in loads in the project — it must not be hidden in the effective scope")
	}
}

// Pressing space on a built-in row removes its enabledMcpServers entry (the honest
// inverse of "enabled here") rather than writing the name into disabledMcpServers.
func TestTUIEnabledMcpServersToggleOff(t *testing.T) {
	st, _ := buildState(t)
	st.cj.AddProjectEnabledMcpServer(st.project, "computer-use")

	m := newModel(st)
	drive(m, "/")
	for _, r := range "computer-use" {
		m.mcps.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.mcps.update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(m, " ")

	if got := st.cj.ProjectEnabledMcpServers(st.project); len(got) != 0 {
		t.Errorf("space on a built-in row should clear enabledMcpServers, got %v", got)
	}
	// It must NOT have leaked into disabledMcpServers.
	for _, k := range st.cj.ProjectDisabledMcpServers(st.project) {
		if k == "computer-use" {
			t.Error("built-in toggle must not write the name into disabledMcpServers")
		}
	}
}

// A name present in BOTH an enumerated source and enabledMcpServers must not produce a
// duplicate built-in row — the enumerated source already represents it.
func TestTUIEnabledMcpServersNoDuplicateForKnownSource(t *testing.T) {
	st, _ := buildState(t)
	// "user-only" is seeded as a user-scope MCP in buildState.
	st.cj.AddProjectEnabledMcpServer(st.project, "user-only")

	m := newModel(st)
	var builtinCount int
	for _, r := range m.mcps.rows {
		if r.Name == "user-only" && r.Source == config.SourceBuiltin {
			builtinCount++
		}
	}
	if builtinCount != 0 {
		t.Errorf("a name already enumerated as a user MCP must not also emit a built-in row (got %d)", builtinCount)
	}
}

// A name present in BOTH enabledMcpServers and disabledMcpServers (contradictory config
// with no enumerated source) must not produce two rows — the orphan/disabled-here row
// already represents it, so the built-in loop must skip it.
func TestTUIEnabledMcpServersNoDuplicateWhenAlsoDisabled(t *testing.T) {
	st, _ := buildState(t)
	st.cj.AddProjectEnabledMcpServer(st.project, "computer-use")
	st.cj.AddProjectDisabledMcpServer(st.project, "computer-use")

	m := newModel(st)
	var total, builtin int
	for _, r := range m.mcps.rows {
		if r.Name == "computer-use" {
			total++
			if r.Source == config.SourceBuiltin {
				builtin++
			}
		}
	}
	if total != 1 {
		t.Errorf("a name in both enabled+disabled lists must yield exactly one row, got %d", total)
	}
	if builtin != 0 {
		t.Error("the disabled/orphan row represents the name; no separate built-in row should be emitted")
	}
}
