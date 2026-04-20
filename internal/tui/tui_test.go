package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/paths"
)

// buildState mirrors loadState but against a sandboxed fake-home so the real
// config is never touched. Returns paths.Paths so tests can probe on-disk state
// after calling state.save().
func buildState(t *testing.T) (*state, paths.Paths) {
	t.Helper()
	home := t.TempDir()
	must := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(home, ".claude.json"), map[string]any{
		"anonymousId": "sandbox",
		"mcpServers": map[string]any{
			"user-only": map[string]any{"command": "u"},
			"shared":    map[string]any{"command": "shared-u"},
		},
	})
	must(filepath.Join(home, ".claude-mcp-stash.json"), map[string]any{
		"userMcpServers": map[string]any{
			"stashed-a": map[string]any{"command": "a"},
			"stashed-b": map[string]any{"command": "b"},
		},
	})
	must(filepath.Join(home, ".claude-mcp-profiles.json"), map[string]any{
		"profiles": map[string]any{
			"dev": []any{"stashed-a", "user-only"},
		},
	})
	must(filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"plug-one@mkt": true,
			"plug-two@mkt": false,
			"plug-three@mkt": true,
		},
	})
	must(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			"plug-one@mkt":   []any{map[string]any{"scope": "user", "installPath": "/x/1", "version": "1.0"}},
			"plug-two@mkt":   []any{map[string]any{"scope": "user", "installPath": "/x/2", "version": "1.0"}},
			"plug-three@mkt": []any{map[string]any{"scope": "user", "installPath": "/x/3", "version": "1.0"}},
		},
	})

	p := paths.Paths{
		Home:             home,
		ClaudeConfigDir:  filepath.Join(home, ".claude"),
		ClaudeJSON:       filepath.Join(home, ".claude.json"),
		SettingsJSON:     filepath.Join(home, ".claude", "settings.json"),
		SettingsLocal:    filepath.Join(home, ".claude", "settings.local.json"),
		PluginsDir:       filepath.Join(home, ".claude", "plugins"),
		InstalledPlugins: filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		KnownMarkets:     filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"),
		Stash:            filepath.Join(home, ".claude-mcp-stash.json"),
		Profiles:         filepath.Join(home, ".claude-mcp-profiles.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
	}
	st, err := loadState(p, filepath.Join(home, "project"))
	if err != nil {
		t.Fatal(err)
	}
	return st, p
}

// key synthesizes a tea.KeyMsg for a given character/key.
func key(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drive feeds a sequence of keys into the model and returns the final View.
func drive(m *model, keys ...string) string {
	// bootstrap with a size so list views allocate
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	for _, k := range keys {
		im, _ = im.Update(key(k))
	}
	return im.View()
}

func TestTUIMCPToggleInLocalScope(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Default scope is "effective" (read-only). 's' cycles to local. Toggle on first entry.
	view := drive(m, "s", " ")

	if !st.dirtyClaude {
		t.Error("dirtyClaude should be set after toggling an MCP")
	}
	if !strings.Contains(view, "UNSAVED") {
		t.Errorf("view should show UNSAVED badge after mutation; got:\n%s", view)
	}
	if len(st.cj.ProjectMCPNames(st.project)) == 0 {
		t.Error("toggling should have added an MCP to local scope")
	}
}

func TestTUIEffectiveSpaceTogglesPerProjectOverride(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Default scope is effective. Press space on the first effective row.
	// It should be added to disabledMcpServers for this project.
	rows := m.mcps.rows
	var first mcpRow
	for _, r := range rows {
		if isEffective(r) {
			first = r
			break
		}
	}
	if first.Name == "" {
		t.Fatal("no effective row to toggle")
	}
	// Position cursor at that row
	for i, r := range m.mcps.rows {
		if r.RowKey() == first.RowKey() {
			m.mcps.index = i
			break
		}
	}
	drive(m, " ")
	if !st.dirtyClaude {
		t.Error("toggling in effective view should dirty claude.json")
	}
	disabled := st.cj.ProjectDisabledMcpServers(st.project)
	found := false
	for _, k := range disabled {
		if k == first.OverrideKey {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in disabledMcpServers, got %v", first.OverrideKey, disabled)
	}
	// Toggle again — should remove
	drive(m, " ")
	disabled = st.cj.ProjectDisabledMcpServers(st.project)
	for _, k := range disabled {
		if k == first.OverrideKey {
			t.Errorf("second toggle should have removed %q from disabledMcpServers, still present", first.OverrideKey)
		}
	}
}

func TestTUIEffectiveSpaceOnStashHints(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	// find a stash row and place cursor there
	for i, r := range m.mcps.rows {
		if r.Source == "stash" {
			m.mcps.index = i
			break
		}
	}
	drive(m, " ")
	if st.dirtyClaude || st.dirtyStash {
		t.Error("space in effective view on a stash row should not mutate state")
	}
}

func TestTUIEffectiveViewShowsUserAndLocalMCPs(t *testing.T) {
	st, _ := buildState(t)
	st.cj.SetProjectMCP(st.project, "local-only", map[string]any{"command": "L"})

	m := newModel(st)
	view := drive(m)

	for _, name := range []string{"user-only", "shared", "local-only"} {
		if !strings.Contains(view, name) {
			t.Errorf("effective view should list %q", name)
		}
	}
	if !strings.Contains(view, "stashed-a") {
		t.Error("stashed MCPs should still appear in the list (as inactive)")
	}
}

func TestTUIMoveFromUserToLocal(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Filter to narrow cursor to "user-only"
	drive(m, "/")
	for _, r := range "user-only" {
		m.mcps.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.mcps.update(tea.KeyMsg{Type: tea.KeyEnter})
	// Now: single visible row "user-only". Press 'm' to start move, then 'l' for local.
	drive(m, "m", "l")

	if _, still := st.cj.UserMCPs()["user-only"]; still {
		t.Error("user-only should have been removed from user scope after move→local")
	}
	if _, ok := st.cj.ProjectMCPs(st.project)["user-only"]; !ok {
		t.Error("user-only should now be in local scope")
	}
	if !st.dirtyClaude {
		t.Error("move should dirty claude.json")
	}
}

func TestTUIBulkDisableInEffective(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Default scope is effective. Press N — should add every effective row's OverrideKey
	// to disabledMcpServers. In the sandbox that's "shared" + "user-only" (both user-scope).
	drive(m, "N")

	if !st.dirtyClaude {
		t.Fatal("bulk disable should dirty claude.json")
	}
	disabled := st.cj.ProjectDisabledMcpServers(st.project)
	// Both user-scope MCPs should be disabled now
	want := map[string]bool{"shared": true, "user-only": true}
	got := map[string]bool{}
	for _, k := range disabled {
		got[k] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("expected %q in disabledMcpServers, got %v", k, disabled)
		}
	}
	// Now bulk-enable — should clear all
	drive(m, "A")
	disabled = st.cj.ProjectDisabledMcpServers(st.project)
	if len(disabled) != 0 {
		t.Errorf("bulk enable should clear overrides, still got: %v", disabled)
	}
}

func TestTUIBulkDisableRespectsFilter(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Filter to just "user-only", then bulk disable → only that one should be touched.
	drive(m, "/")
	for _, r := range "user-only" {
		m.mcps.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.mcps.update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(m, "N")

	disabled := st.cj.ProjectDisabledMcpServers(st.project)
	if len(disabled) != 1 || disabled[0] != "user-only" {
		t.Errorf("filter should limit bulk scope; got: %v", disabled)
	}
}

func TestTUIMoveToStash(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	drive(m, "/")
	for _, r := range "shared" {
		m.mcps.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.mcps.update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(m, "m", "s")

	if _, still := st.cj.UserMCPs()["shared"]; still {
		t.Error("shared should have been removed from user scope after move→stash")
	}
	if _, ok := st.stash.Get("shared"); !ok {
		t.Error("shared should now be in stash")
	}
}

func TestTUIMCPScopeCycling(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Starts at effective; 's' cycles: effective → local → user → project → stash → effective.
	if m.mcps.scope != "effective" {
		t.Errorf("default scope: want effective, got %s", m.mcps.scope)
	}
	for _, want := range []string{"local", "user", "project", "stash", "effective"} {
		drive(m, "s")
		if m.mcps.scope != want {
			t.Errorf("cycle: want %s, got %s", want, m.mcps.scope)
		}
	}
}

func TestTUITabSwitching(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	_ = drive(m, "tab")
	if m.tab != tabPlugins {
		t.Errorf("tab 1: want plugins, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabProfiles {
		t.Errorf("tab 2: want profiles, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabSummary {
		t.Errorf("tab 3: want summary, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabMCPs {
		t.Errorf("tab 4: want mcps (wrapped), got %d", m.tab)
	}
	// Numeric shortcuts
	_ = drive(m, "3")
	if m.tab != tabProfiles {
		t.Errorf("numeric 3: want profiles, got %d", m.tab)
	}
	_ = drive(m, "4")
	if m.tab != tabSummary {
		t.Errorf("numeric 4: want summary, got %d", m.tab)
	}
}

func TestTUIPluginBulkDisable(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Switch to Plugins tab and bulk-disable all
	_ = drive(m, "2", "N")

	if !st.dirtySettings {
		t.Fatal("dirtySettings should be set")
	}
	// All three entries should now be false
	for _, e := range st.settings.PluginEntries() {
		if e.Enabled {
			t.Errorf("%s should be disabled after N", e.ID)
		}
	}

	// Bulk-enable all back
	_ = drive(m, "A")
	for _, e := range st.settings.PluginEntries() {
		if !e.Enabled {
			t.Errorf("%s should be enabled after A", e.ID)
		}
	}
}

func TestTUIPluginFilterMode(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	_ = drive(m, "2") // Plugins tab
	// Cycle through filter modes: "" -> enabled -> disabled -> ""
	if m.plugins.showOnly != "" {
		t.Fatalf("initial filter should be empty, got %q", m.plugins.showOnly)
	}
	_ = drive(m, "f")
	if m.plugins.showOnly != "enabled" {
		t.Errorf("after 1x f: want enabled, got %q", m.plugins.showOnly)
	}
	_ = drive(m, "f")
	if m.plugins.showOnly != "disabled" {
		t.Errorf("after 2x f: want disabled, got %q", m.plugins.showOnly)
	}
	_ = drive(m, "f")
	if m.plugins.showOnly != "" {
		t.Errorf("after 3x f: want empty, got %q", m.plugins.showOnly)
	}
}

func TestTUIProfileApply(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Profiles tab, cursor at first profile ("dev"), enter applies it
	_ = drive(m, "3", "enter")

	if !st.dirtyClaude {
		t.Fatal("dirtyClaude should be set after applying profile")
	}
	mcps := st.cj.ProjectMCPNames(st.project)
	// Profile "dev" = [stashed-a, user-only]. Both should be sourced:
	//   stashed-a from stash, user-only from user scope.
	wantSet := map[string]bool{"stashed-a": true, "user-only": true}
	if len(mcps) != len(wantSet) {
		t.Errorf("want %d project MCPs, got %d: %v", len(wantSet), len(mcps), mcps)
	}
	for _, n := range mcps {
		if !wantSet[n] {
			t.Errorf("unexpected MCP %q applied by profile", n)
		}
	}
}

func TestTUISaveFlushesToDisk(t *testing.T) {
	st, p := buildState(t)
	m := newModel(st)

	// Toggle something to dirty the state, then save with 'w'.
	// Cycle to 'local' scope first — the effective-view toggle writes to
	// disabledMcpServers (per-project override), but this test asserts a direct
	// mutation of projects[...].mcpServers, which only the local-scope toggle produces.
	_ = drive(m, "s", " ", "w")

	if st.anyDirty() {
		t.Error("after w, no dirty flags should remain")
	}
	// A backup should exist
	entries, err := os.ReadDir(p.BackupsDir)
	if err != nil {
		t.Fatalf("backups dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one backup file")
	}
	// And the claude.json should reflect the project-scope addition
	b, err := os.ReadFile(p.ClaudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	if err := json.Unmarshal(b, &disk); err != nil {
		t.Fatal(err)
	}
	projects, _ := disk["projects"].(map[string]any)
	node, ok := projects[st.project].(map[string]any)
	if !ok {
		t.Fatalf("project node missing on disk after save: %v", projects)
	}
	if _, ok := node["mcpServers"].(map[string]any); !ok {
		t.Error("project mcpServers should be on disk after save")
	}
	// Unknown field should still be there
	if disk["anonymousId"] != "sandbox" {
		t.Errorf("anonymousId lost across TUI save: %#v", disk["anonymousId"])
	}
}

func TestTUIQuitBlockedWhenDirty(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Cycle to local scope, toggle to get dirty state, then try to quit with 'q'.
	view := drive(m, "s", " ", "q")
	if !st.anyDirty() {
		t.Fatal("precondition: should be dirty")
	}
	// First 'q' should be rejected with a warning; message is displayed
	if !strings.Contains(view, "unsaved changes") {
		t.Errorf("first q should warn about unsaved changes; got:\n%s", view)
	}
}

func TestTUISummaryDetectsRedundancy(t *testing.T) {
	st, _ := buildState(t)
	// Simulate "shared" MCP being active in BOTH user and project scope
	cfg, _ := st.cj.UserMCPs()["shared"]
	st.cj.SetProjectMCP(st.project, "shared", cfg)

	m := newModel(st)
	view := drive(m, "4") // switch to Summary tab

	if !strings.Contains(view, "BOTH user and project scope") {
		t.Errorf("summary should flag user+project duplication; got:\n%s", view)
	}
	if !strings.Contains(view, "shared") {
		t.Errorf("summary should mention the duplicated MCP name; got:\n%s", view)
	}
}

func TestTUIFilterNarrowsVisible(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Open filter, type "stashed", press enter (filterActive=false now, value stays)
	_ = drive(m, "/")
	if !m.mcps.filterActive {
		t.Fatal("filter should be active after /")
	}
	// Simulate typing characters — textinput.Model handles runes
	for _, r := range "stashed" {
		m.mcps.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.mcps.update(tea.KeyMsg{Type: tea.KeyEnter})

	visible := m.mcps.visibleRows()
	for _, r := range visible {
		if !strings.Contains(r.Name, "stashed") {
			t.Errorf("filter miss: %q should have been filtered out", r.Name)
		}
	}
	if len(visible) < 2 {
		t.Errorf("want at least 2 matches (stashed-a, stashed-b), got %d", len(visible))
	}
}
