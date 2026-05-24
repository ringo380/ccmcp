package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/ringo380/ccmcp/internal/updates"
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
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
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
	case "ctrl+g":
		return tea.KeyMsg{Type: tea.KeyCtrlG}
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
	// Stash rows are hidden in the effective view by default — reveal them so this
	// "space on a stash row is a no-op" assertion can actually reach a stash row.
	m.mcps.showHidden = true
	// find a stash row and place cursor there (use visible-row index so update() sees it)
	visible := m.mcps.visibleRows()
	for i, r := range visible {
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
	// Stash entries are HIDDEN by default in the effective scope (they don't load).
	// Pressing `H` reveals them.
	if strings.Contains(view, "stashed-a") {
		t.Error("stashed MCPs should NOT appear in the default effective view (press H to reveal)")
	}
	view = drive(m, "H")
	if !strings.Contains(view, "stashed-a") {
		t.Error("after pressing H, stashed MCPs should appear in the effective view")
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

func TestTUIHelpOverlay(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// `?` opens the help overlay, replacing the tab view.
	view := drive(m, "?")
	if !m.showHelp {
		t.Fatal("`?` should set showHelp = true")
	}
	for _, want := range []string{
		"Source badges",
		"[u]", "[l]", "[p]", "[P]", "[@]", "[s]", "[?]",
		"Row marks",
		"[x]", "[~]", "[!]",
		"MCPs tab", "Plugins tab", "Skills tab", "Agents tab", "Commands tab", "Profiles tab", "Summary tab", "Doctor tab", "Global",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("help overlay missing %q; got:\n%s", want, view)
		}
	}
	// `?` again closes it.
	drive(m, "?")
	if m.showHelp {
		t.Error("second `?` should close the overlay")
	}
	// `esc` also closes it.
	drive(m, "?")
	drive(m, "esc")
	if m.showHelp {
		t.Error("esc should close the overlay")
	}
	// `q` while help is open should NOT close it — matches footer hint of `?/esc` only,
	// and avoids surprising a user who hits `q` expecting to quit the whole app.
	drive(m, "?")
	if !m.showHelp {
		t.Fatal("precondition: help should be open")
	}
	drive(m, "q")
	if !m.showHelp {
		t.Error("`q` inside help should NOT close the overlay (overlay is ?/esc-only)")
	}
}

func TestTUIStashShortcut(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Filter to a user-scope row, then press S → should move it to stash.
	drive(m, "/")
	for _, r := range "user-only" {
		m.mcps.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.mcps.update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(m, "S")

	if _, still := st.cj.UserMCPs()["user-only"]; still {
		t.Error("S on a user-scope row should remove from user scope")
	}
	if _, ok := st.stash.Get("user-only"); !ok {
		t.Error("S on a user-scope row should place into stash")
	}
}

func TestTUIUnstashShortcut(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	// Stash rows are hidden in the effective scope by default — reveal them so the
	// filter can land on `stashed-a`.
	m.mcps.showHidden = true

	// Filter to an existing stash row, press S → should move it to user scope (unstash).
	drive(m, "/")
	for _, r := range "stashed-a" {
		m.mcps.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.mcps.update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(m, "S")

	if _, still := st.stash.Get("stashed-a"); still {
		t.Error("S on a stash row should remove from stash")
	}
	if _, ok := st.cj.UserMCPs()["stashed-a"]; !ok {
		t.Error("S on a stash row should place into user scope (unstash)")
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

// TestTUIEffectiveHidesNoise covers the default-view filtering: stash rows, MCPs from
// installed-but-globally-disabled plugins, and orphan rows for stale `disabledMcpServers`
// keys all stay hidden until the user presses `H`. Other scopes are unaffected.
func TestTUIEffectiveHidesNoise(t *testing.T) {
	st, _ := buildState(t)
	// Inject an orphan override: a plain stdio name that has no source anywhere.
	// classify.Classify treats this as OrphanStdio; rebuild() emits a row with
	// UnknownReason set, which isHiddenInEffective should suppress.
	st.cj.AddProjectDisabledMcpServer(st.project, "ghost-orphan")
	m := newModel(st)
	view := drive(m)

	// Stash rows hidden by default
	if strings.Contains(view, "stashed-a") || strings.Contains(view, "stashed-b") {
		t.Error("stash rows should be hidden by default in effective scope")
	}
	// Orphan hidden by default
	if strings.Contains(view, "ghost-orphan") {
		t.Error("orphan rows should be hidden by default in effective scope")
	}
	// Title should advertise hidden count + the H hint
	if !strings.Contains(view, "hidden") || !strings.Contains(view, "H") {
		t.Errorf("title should include hidden count and `H` hint; got:\n%s", view)
	}

	// Press H — everything reappears
	view = drive(m, "H")
	for _, want := range []string{"stashed-a", "stashed-b", "ghost-orphan"} {
		if !strings.Contains(view, want) {
			t.Errorf("after H, expected %q to appear; got:\n%s", want, view)
		}
	}

	// Press H again — back to hidden
	view = drive(m, "H")
	if strings.Contains(view, "stashed-a") {
		t.Error("second H should re-hide stash rows")
	}

	// Other scopes: stash scope should always show stash rows regardless of showHidden
	view = drive(m, "s", "s", "s", "s") // effective → local → user → project → stash
	if !strings.Contains(view, "stashed-a") {
		t.Errorf("stash scope should always show stash rows; got:\n%s", view)
	}
}

// TestTUIEffectiveSpaceMapsToFilteredVisibleRow exercises the keypress→row mapping in the
// default (showHidden=false) effective view. Catches the regression where update() reads
// visible[v.index] but tests pre-position v.index against the unfiltered v.rows: any change
// to visibleRows() that drops/reorders rows would map space onto the wrong row's
// OverrideKey. The previous TestTUIEffectiveSpaceTogglesPerProjectOverride drives via
// rows[]; this one drives via visibleRows().
func TestTUIEffectiveSpaceMapsToFilteredVisibleRow(t *testing.T) {
	st, _ := buildState(t)
	// Inject an orphan to ensure the filter is actually dropping rows from view —
	// otherwise the test could pass against an unfiltered visible list.
	st.cj.AddProjectDisabledMcpServer(st.project, "ghost-mapping-test")
	m := newModel(st)

	visible := m.mcps.visibleRows()
	if len(visible) == 0 {
		t.Fatal("precondition: at least one visible row in default effective view")
	}
	// Confirm the orphan is hidden — guards the regression direction.
	for _, r := range visible {
		if r.Name == "ghost-mapping-test" {
			t.Fatal("orphan should be hidden from default effective view")
		}
	}

	// Position cursor on the last visible row and press space — should toggle THAT row's
	// override key, not anything from the unfiltered v.rows.
	target := visible[len(visible)-1]
	m.mcps.index = len(visible) - 1
	drive(m, " ")

	if !st.dirtyClaude {
		t.Fatal("space on filtered visible row should dirty claude.json")
	}
	disabled := st.cj.ProjectDisabledMcpServers(st.project)
	found := false
	for _, k := range disabled {
		if k == target.OverrideKey {
			found = true
		}
	}
	if !found {
		t.Errorf("space should have toggled the visible target %q (override %q); got %v",
			target.Name, target.OverrideKey, disabled)
	}
}

// TestTUIHToggleResetsCursor ensures pressing `H` resets index/top so the cursor doesn't
// drift onto an arbitrary row when the visible set changes size.
func TestTUIHToggleResetsCursor(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	// Scroll partway down (need >0 visible rows for this to mean something)
	if len(m.mcps.visibleRows()) < 2 {
		t.Skip("not enough rows in fixture to scroll")
	}
	m.mcps.index = 1
	m.mcps.top = 1

	drive(m, "H")
	if m.mcps.index != 0 || m.mcps.top != 0 {
		t.Errorf("H toggle should reset index/top; got index=%d top=%d", m.mcps.index, m.mcps.top)
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
	if m.tab != tabMarketplaces {
		t.Errorf("tab 2: want marketplaces, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabDiscover {
		t.Errorf("tab 3: want discover, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabSkills {
		t.Errorf("tab 4: want skills, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabAgents {
		t.Errorf("tab 5: want agents, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabCommands {
		t.Errorf("tab 6: want commands, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabProfiles {
		t.Errorf("tab 7: want profiles, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabSummary {
		t.Errorf("tab 8: want summary, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabDoctor {
		t.Errorf("tab 9: want doctor, got %d", m.tab)
	}
	_ = drive(m, "tab")
	if m.tab != tabMCPs {
		t.Errorf("tab 10: want mcps (wrapped), got %d", m.tab)
	}
	// Numeric shortcuts
	_ = drive(m, "3")
	if m.tab != tabMarketplaces {
		t.Errorf("numeric 3: want marketplaces, got %d", m.tab)
	}
	_ = drive(m, "4")
	if m.tab != tabDiscover {
		t.Errorf("numeric 4: want discover, got %d", m.tab)
	}
	_ = drive(m, "5")
	if m.tab != tabSkills {
		t.Errorf("numeric 5: want skills, got %d", m.tab)
	}
	_ = drive(m, "6")
	if m.tab != tabAgents {
		t.Errorf("numeric 6: want agents, got %d", m.tab)
	}
	_ = drive(m, "7")
	if m.tab != tabCommands {
		t.Errorf("numeric 7: want commands, got %d", m.tab)
	}
	_ = drive(m, "8")
	if m.tab != tabProfiles {
		t.Errorf("numeric 8: want profiles, got %d", m.tab)
	}
	_ = drive(m, "9")
	if m.tab != tabSummary {
		t.Errorf("numeric 9: want summary, got %d", m.tab)
	}
	_ = drive(m, "0")
	if m.tab != tabDoctor {
		t.Errorf("numeric 0: want doctor, got %d", m.tab)
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

func TestTUIPluginRemoveTwoStepConfirm(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Switch to plugins tab, position at first row.
	_ = drive(m, "2")
	m.plugins.index = 0

	visible := m.plugins.visibleRows()
	if len(visible) == 0 {
		t.Fatal("no visible plugin rows")
	}
	target := visible[0]
	if target.IsRemote {
		t.Skip("first row is remote — skipping remove test")
	}

	// First x: sets pendingRemove, no removal yet.
	_ = drive(m, "x")
	if m.plugins.pendingRemove != target.ID {
		t.Errorf("pendingRemove should be %q, got %q", target.ID, m.plugins.pendingRemove)
	}
	if st.dirtySettings || st.dirtyPlugins {
		t.Error("first x should not modify state")
	}

	// Second x: confirms removal.
	_ = drive(m, "x")
	if m.plugins.pendingRemove != "" {
		t.Errorf("pendingRemove should be cleared after confirm, got %q", m.plugins.pendingRemove)
	}
	if !st.dirtySettings {
		t.Error("dirtySettings should be set after confirmed remove")
	}
	if !st.dirtyPlugins {
		t.Error("dirtyPlugins should be set after confirmed remove")
	}
	// Plugin should be gone from settings.
	for _, e := range st.settings.PluginEntries() {
		if e.ID == target.ID {
			t.Errorf("plugin %q should have been removed from settings", target.ID)
		}
	}
}

func TestTUIPluginRemoveCancelOnOtherKey(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	_ = drive(m, "2")
	visible := m.plugins.visibleRows()
	if len(visible) == 0 {
		t.Fatal("no visible plugin rows")
	}
	target := visible[0]
	if target.IsRemote {
		t.Skip("first row is remote")
	}

	// First x: starts pending.
	_ = drive(m, "x")
	if m.plugins.pendingRemove != target.ID {
		t.Fatal("pendingRemove not set")
	}

	// Any other key cancels.
	_ = drive(m, "j")
	if m.plugins.pendingRemove != "" {
		t.Errorf("pendingRemove should be cleared after non-x key, got %q", m.plugins.pendingRemove)
	}
	if st.dirtySettings || st.dirtyPlugins {
		t.Error("cancelled remove must not modify state")
	}
}

func TestTUIPluginUpdateResultApplied(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	_ = drive(m, "2")

	// Simulate an async update completing with a new SHA.
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	im, _ = im.Update(pluginUpdateResultMsg{
		id:          "plug-one@mkt",
		oldSha:      "oldshaFull",
		oldInstPath: "/x/1",
		result: &install.Result{
			QualifiedID:  "plug-one@mkt",
			InstallPath:  "/x/1-new",
			Version:      "newsha",
			GitCommitSha: "newshaFull",
		},
	})
	updated := im.(*model)
	if !updated.st.dirtyPlugins {
		t.Error("dirtyPlugins should be set after update result")
	}
	// Check the installed entry was updated.
	list := updated.st.installed.List()
	var found bool
	for _, ip := range list {
		if ip.ID == "plug-one@mkt" && ip.InstallPath == "/x/1-new" {
			found = true
		}
	}
	if !found {
		t.Error("installed entry should reflect new install path after update result")
	}
}

func TestTUIPluginRemoteRowToggle(t *testing.T) {
	st, _ := buildState(t)
	// Add a claude.ai integration to the state.
	st.claudeAi = []string{"claude.ai Stripe"}
	m := newModel(st)

	_ = drive(m, "2")
	m.plugins.rebuild()

	// Find the remote row in visibleRows and place cursor there.
	visible := m.plugins.visibleRows()
	remoteIdx := -1
	for i, r := range visible {
		if r.IsRemote && r.RemoteKey == "claude.ai Stripe" {
			remoteIdx = i
			break
		}
	}
	if remoteIdx < 0 {
		t.Fatal("remote row for claude.ai Stripe not found in visible rows")
	}
	m.plugins.index = remoteIdx

	// Space: should disable the integration for this project.
	_ = drive(m, " ")
	if !st.dirtyClaude {
		t.Error("toggling remote row should dirty claude.json")
	}
	disabled := st.cj.ProjectDisabledMcpServers(st.project)
	found := false
	for _, k := range disabled {
		if k == "claude.ai Stripe" {
			found = true
		}
	}
	if !found {
		t.Errorf("claude.ai Stripe should be in disabledMcpServers, got %v", disabled)
	}

	// Space again: should re-enable.
	_ = drive(m, " ")
	disabled = st.cj.ProjectDisabledMcpServers(st.project)
	for _, k := range disabled {
		if k == "claude.ai Stripe" {
			t.Error("second toggle should have removed claude.ai Stripe from disabledMcpServers")
		}
	}
}

func TestTUIProfileApply(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Profiles tab (now key "8" after Discover insertion), cursor at first profile ("dev"), enter applies it
	_ = drive(m, "8", "enter")

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
	view := drive(m, "9") // switch to Summary tab

	if !strings.Contains(view, "BOTH user and project scope") {
		t.Errorf("summary should flag user+project duplication; got:\n%s", view)
	}
	if !strings.Contains(view, "shared") {
		t.Errorf("summary should mention the duplicated MCP name; got:\n%s", view)
	}
}

func TestTUISkillsTabRenders(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	view := drive(m, "5") // Skills tab
	if !strings.Contains(view, "Skills") {
		t.Errorf("skills tab should show Skills header; got:\n%s", view)
	}
}

func TestTUISkillsTabToggleNoop(t *testing.T) {
	// With no skills on disk the view should render without crash
	st, _ := buildState(t)
	m := newModel(st)

	// Switch to skills, attempt toggle — with empty rows should be a no-op
	_ = drive(m, "5", " ")
	if st.dirtySettings {
		t.Error("toggling in empty skills view should not dirty settings")
	}
}

func TestTUIAgentsTabRenders(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	view := drive(m, "6") // Agents tab
	if !strings.Contains(view, "Agents") {
		t.Errorf("agents tab should show Agents header; got:\n%s", view)
	}
}

func TestTUICommandsTabRenders(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	view := drive(m, "7") // Commands tab
	if !strings.Contains(view, "Commands") {
		t.Errorf("commands tab should show Commands header; got:\n%s", view)
	}
}

func TestTUICommandsConflictToggle(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Switch to commands tab, press ! to toggle conflicts-only
	_ = drive(m, "7")
	if m.commands.conflictsOnly {
		t.Fatal("should start with conflictsOnly=false")
	}
	_ = drive(m, "!")
	if !m.commands.conflictsOnly {
		t.Error("! should enable conflicts-only mode")
	}
	_ = drive(m, "!")
	if m.commands.conflictsOnly {
		t.Error("second ! should disable conflicts-only mode")
	}
}

func TestTUIDoctorTabRenders(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	view := drive(m, "0") // Doctor tab
	if !strings.Contains(view, "Doctor") {
		t.Errorf("doctor tab should contain 'Doctor'; got:\n%s", view)
	}
}

func TestTUIDoctorTabRerun(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// drive() calls View() at the end, which calls render(), which runs lint.
	_ = drive(m, "0")
	if !m.doctor.loaded {
		t.Fatal("doctor.loaded should be true after first render")
	}
	// 'r' resets loaded so the next render re-runs lint.
	m.doctor.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.doctor.loaded {
		t.Error("doctor.loaded should be false after 'r' (re-run deferred to next render)")
	}
	// Calling render() triggers the re-run.
	m.doctor.render()
	if !m.doctor.loaded {
		t.Error("doctor.loaded should be true after render() following 'r'")
	}
}

func TestTUIFilterNarrowsVisible(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	// Stash rows are hidden in the effective scope by default; this test searches for
	// them, so reveal hidden rows so the filter has something to match.
	m.mcps.showHidden = true

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

func TestTUIMarketplacesTabRenders(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	view := drive(m, "3") // Marketplaces tab
	if !strings.Contains(view, "Marketplaces") {
		t.Errorf("marketplaces tab should show Marketplaces header; got:\n%s", view)
	}
	// Three plugins under @mkt all derive a "mkt" row even though no clone exists.
	if !strings.Contains(view, "mkt") {
		t.Errorf("expected to see mkt marketplace; got:\n%s", view)
	}
}

func TestTUIMarketplacesAddRemoveCancel(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Switch to marketplaces tab, press 'a' to enter add mode
	_ = drive(m, "3", "a")
	if !m.marketplaces.addMode {
		t.Fatal("a should put view in add mode")
	}
	// esc should exit
	_ = drive(m, "esc")
	if m.marketplaces.addMode {
		t.Error("esc should exit add mode")
	}
}

// TestTUIPluginBulkUpdateMessageHandlerSetsDirty verifies the bulk-update result
// handler marks dirtyPlugins and rescans MCPs (regression for an earlier version
// that mutated v.st.installed inside the worker goroutine and skipped both flags).
func TestTUIPluginBulkUpdateMessageHandlerSetsDirty(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Simulate the worker goroutine returning two successful updates.
	msg := pluginBulkUpdateResultMsg{
		applied: []bulkUpdateApplied{
			{id: "plug-one@mkt", result: &install.Result{QualifiedID: "plug-one@mkt", InstallPath: "/x/1-new", Version: "2.0", GitCommitSha: "newsha"}, oldInstPath: "/x/1"},
		},
	}
	_ = drive(m, "2") // switch to plugins tab
	_ = m.plugins.update(msg)

	if !st.dirtyPlugins {
		t.Error("bulk update result must mark dirtyPlugins (so 'w' flushes installed_plugins.json)")
	}
}

func TestTUIMarketplacesUpdateIndicator(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Manually populate cache to simulate an outdated marketplace
	st.updates.PutMarketplace("mkt", updates.Status{
		Local: "abc123", Remote: "def456", Outdated: true,
	})
	m.marketplaces.rebuild()
	view := drive(m, "3")
	if !strings.Contains(view, "update available") {
		t.Errorf("expected update-available indicator; got:\n%s", view)
	}
	if !strings.Contains(view, "1 update available") {
		t.Errorf("expected outdated count; got:\n%s", view)
	}
}

// TestTUIPluginUpdateClearsIndicator verifies the full cycle: a plugin marked
// outdated in the update cache renders with "↑ update available", and after a
// successful pluginUpdateResultMsg lands the indicator disappears, the title-bar
// outdated count drops, and a success flash is shown.
func TestTUIPluginUpdateClearsIndicator(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Mark plug-one outdated in the cache.
	st.updates.PutPlugin("plug-one@mkt", updates.Status{
		Local: "oldsha00", Remote: "newsha00", Outdated: true,
	})
	m.plugins.rebuild()

	view := drive(m, "2") // plugins tab
	if !strings.Contains(view, "↑ update available") {
		t.Fatalf("expected '↑ update available' before update; got:\n%s", view)
	}
	if !strings.Contains(view, "(1 update available)") {
		t.Fatalf("expected '(1 update available)' header before update; got:\n%s", view)
	}

	// Simulate worker returning a successful re-fetch. Route through m.Update so
	// the model drains plugins.flash → m.message (rendered in the footer).
	var im tea.Model = m
	im, _ = im.Update(pluginUpdateResultMsg{
		id:          "plug-one@mkt",
		oldSha:      "oldsha00",
		oldInstPath: "/x/1",
		result: &install.Result{
			QualifiedID:  "plug-one@mkt",
			InstallPath:  "/x/1-new",
			Version:      "2.0",
			GitCommitSha: "newsha00",
		},
	})

	view = im.View()
	if strings.Contains(view, "↑ update available") {
		t.Errorf("'↑ update available' should be gone after successful update; got:\n%s", view)
	}
	if strings.Contains(view, "update available)") {
		t.Errorf("title-bar outdated count should not show after update; got:\n%s", view)
	}
	if !strings.Contains(view, "updated plug-one@mkt") {
		t.Errorf("expected success flash; got:\n%s", view)
	}
	if !st.dirtyPlugins {
		t.Error("successful update must mark dirtyPlugins")
	}
	if _, ok := st.updates.Plugin("plug-one@mkt"); ok {
		t.Error("update cache entry should be invalidated after successful update")
	}
}

// TestTUIPluginUpdateErrorPreservesIndicator verifies that a failed update does
// NOT clear the outdated indicator (the user still needs to retry) and surfaces
// an error flash.
func TestTUIPluginUpdateErrorPreservesIndicator(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	st.updates.PutPlugin("plug-one@mkt", updates.Status{
		Local: "oldsha00", Remote: "newsha00", Outdated: true,
	})
	m.plugins.rebuild()
	_ = drive(m, "2")

	var im tea.Model = m
	im, _ = im.Update(pluginUpdateResultMsg{
		id:     "plug-one@mkt",
		oldSha: "oldsha00",
		err:    errFake("boom"),
	})

	view := im.View()
	if !strings.Contains(view, "↑ update available") {
		t.Errorf("indicator should remain after failed update; got:\n%s", view)
	}
	if !strings.Contains(view, "update error: boom") {
		t.Errorf("expected error flash; got:\n%s", view)
	}
	if st.dirtyPlugins {
		t.Error("failed update must NOT mark dirtyPlugins")
	}
	if s, ok := st.updates.Plugin("plug-one@mkt"); !ok || !s.Outdated {
		t.Error("cache entry should be preserved after failed update")
	}
}

// TestTUIPluginBulkUpdateClearsIndicators verifies the bulk-update message
// handler clears the outdated indicator and decrements the title-bar count for
// each plugin in the applied set.
func TestTUIPluginBulkUpdateClearsIndicators(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	st.updates.PutPlugin("plug-one@mkt", updates.Status{Local: "a", Remote: "b", Outdated: true})
	st.updates.PutPlugin("plug-three@mkt", updates.Status{Local: "c", Remote: "d", Outdated: true})
	m.plugins.rebuild()

	view := drive(m, "2")
	if !strings.Contains(view, "(2 update available)") {
		t.Fatalf("expected '(2 update available)' before bulk update; got:\n%s", view)
	}

	var im tea.Model = m
	im, _ = im.Update(pluginBulkUpdateResultMsg{
		applied: []bulkUpdateApplied{
			{id: "plug-one@mkt", result: &install.Result{QualifiedID: "plug-one@mkt", InstallPath: "/x/1-new", Version: "2.0", GitCommitSha: "newone"}, oldInstPath: "/x/1"},
			{id: "plug-three@mkt", result: &install.Result{QualifiedID: "plug-three@mkt", InstallPath: "/x/3-new", Version: "2.0", GitCommitSha: "newthree"}, oldInstPath: "/x/3"},
		},
	})

	view = im.View()
	if strings.Contains(view, "↑ update available") {
		t.Errorf("'↑ update available' should be gone after bulk update; got:\n%s", view)
	}
	if strings.Contains(view, "update available)") {
		t.Errorf("title-bar outdated count should be cleared after bulk update; got:\n%s", view)
	}
	if !strings.Contains(view, "2 updated") {
		t.Errorf("expected bulk-update success summary; got:\n%s", view)
	}
}

// TestTUIPluginUpdateInProgressVisible verifies the in-flight indicator renders
// while v.updating is true and disappears once the result message lands.
func TestTUIPluginUpdateInProgressVisible(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2") // plugins tab

	// Simulate an update in flight: set the busy flag and tick the spinner so
	// state.spinnerFrame is populated, then render.
	m.plugins.updating = true
	var im tea.Model = m
	im, _ = im.Update(m.spinner.Tick())

	view := im.View()
	if !strings.Contains(view, "update in progress…") {
		t.Errorf("expected in-progress indicator while v.updating; got:\n%s", view)
	}
	if st.spinnerFrame == "" {
		t.Error("spinnerFrame should be populated after a TickMsg")
	}

	// After the result lands, the indicator should be gone.
	im, _ = im.Update(pluginUpdateResultMsg{
		id:          "plug-one@mkt",
		oldSha:      "old",
		oldInstPath: "/x/1",
		result: &install.Result{
			QualifiedID: "plug-one@mkt", InstallPath: "/x/1-new", Version: "2.0", GitCommitSha: "new",
		},
	})
	view = im.View()
	if strings.Contains(view, "update in progress…") {
		t.Errorf("in-progress indicator should be gone after result; got:\n%s", view)
	}
}

// TestTUISpinnerLoopsContinuously verifies each TickMsg returns a follow-up tick
// command (the always-ticking loop) so the spinner animation continues.
func TestTUISpinnerLoopsContinuously(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	var im tea.Model = m
	im, cmd := im.Update(m.spinner.Tick())
	if cmd == nil {
		t.Fatal("TickMsg handler must return a follow-up cmd to keep the spinner ticking")
	}
	// Executing the cmd should yield another spinner.TickMsg.
	if _, ok := cmd().(spinner.TickMsg); !ok {
		t.Errorf("follow-up cmd should produce a spinner.TickMsg; got %T", cmd())
	}
	_ = im
}

// TestTUIPluginBulkPerItemProgress verifies the per-item bulk-update flow:
// each pluginBulkItemDoneMsg advances the (N/M) counter, applies the SHA delta
// to the installed manifest live (so each "↑ update available" annotation clears
// as its plugin lands rather than all at once), and the final result message
// produces the bulk-summary flash and clears bulk-progress scratch state.
func TestTUIPluginBulkPerItemProgress(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)

	// Mark all three plugins outdated so we can watch indicators clear one at a time.
	st.updates.PutPlugin("plug-one@mkt", updates.Status{Local: "old1", Remote: "new1", Outdated: true})
	st.updates.PutPlugin("plug-two@mkt", updates.Status{Local: "old2", Remote: "new2", Outdated: true})
	st.updates.PutPlugin("plug-three@mkt", updates.Status{Local: "old3", Remote: "new3", Outdated: true})
	m.plugins.rebuild()
	_ = drive(m, "2") // plugins tab

	// Seed the bulk-update state directly (tests can't run install.Install for real).
	m.plugins.bulkUpdating = true
	m.plugins.bulkTargets = []bulkUpdateTarget{
		{id: "plug-one@mkt", name: "plug-one", mkt: "mkt", oldSha: "old1", oldInstPath: "/x/1"},
		{id: "plug-two@mkt", name: "plug-two", mkt: "mkt", oldSha: "old2", oldInstPath: "/x/2"},
		{id: "plug-three@mkt", name: "plug-three", mkt: "mkt", oldSha: "old3", oldInstPath: "/x/3"},
	}
	m.plugins.bulkIndex = 0
	m.plugins.flash = styleProgress.Render("updating 3 plugin(s)… (0/3)")

	// Tick spinner once so spinnerFrame is populated for the in-progress line.
	var im tea.Model = m
	im, _ = im.Update(m.spinner.Tick())

	view := im.View()
	// (N/M) shows "currently on item N of M", so 1/3 before any items land
	// (we are about to process item 1).
	if !strings.Contains(view, "bulk update in progress… (1/3)") {
		t.Errorf("expected '(1/3)' before any items land; got:\n%s", view)
	}

	// First item lands successfully.
	im, _ = im.Update(pluginBulkItemDoneMsg{
		target: m.plugins.bulkTargets[0],
		result: &install.Result{QualifiedID: "plug-one@mkt", InstallPath: "/x/1-new", Version: "2.0", GitCommitSha: "new1"},
	})
	view = im.View()
	if !strings.Contains(view, "bulk update in progress… (2/3)") {
		t.Errorf("expected '(2/3)' after item 1 (now working on item 2); got:\n%s", view)
	}
	// plug-one's indicator should already be cleared (live invalidation), the others remain.
	if _, ok := st.updates.Plugin("plug-one@mkt"); ok {
		t.Error("plug-one update cache should be invalidated immediately after item 1 lands")
	}
	if s, ok := st.updates.Plugin("plug-two@mkt"); !ok || !s.Outdated {
		t.Error("plug-two should still be outdated mid-batch")
	}
	if len(m.plugins.bulkApplied) != 1 {
		t.Errorf("bulkApplied len = %d; want 1", len(m.plugins.bulkApplied))
	}

	// Second item: skipped (SHA unchanged).
	im, _ = im.Update(pluginBulkItemDoneMsg{
		target: m.plugins.bulkTargets[1],
		result: &install.Result{QualifiedID: "plug-two@mkt", InstallPath: "/x/2", Version: "1.0", GitCommitSha: "old2"},
	})
	if len(m.plugins.bulkSkipped) != 1 || m.plugins.bulkSkipped[0] != "plug-two@mkt" {
		t.Errorf("bulkSkipped = %v; want [plug-two@mkt]", m.plugins.bulkSkipped)
	}

	// Third item: failed.
	im, _ = im.Update(pluginBulkItemDoneMsg{
		target: m.plugins.bulkTargets[2],
		err:    errFake("network down"),
	})
	if len(m.plugins.bulkFailed) != 1 || m.plugins.bulkFailed[0].ID != "plug-three@mkt" {
		t.Errorf("bulkFailed = %v; want one entry for plug-three@mkt", m.plugins.bulkFailed)
	}
	if got := m.plugins.bulkFailed[0].Err; !strings.Contains(got, "network down") {
		t.Errorf("bulkFailed[0].Err = %q; want it to contain the underlying error text", got)
	}
	if m.plugins.bulkFailed[0].Hint == "" {
		t.Errorf("bulkFailed[0].Hint should be populated by classifyUpdateError")
	}

	// After the third item, the next cmd should emit pluginBulkUpdateResultMsg.
	// Drive the runtime past it: by this point bulkIndex == 3 == len(bulkTargets),
	// so bulkRunNextItem returned a cmd that produces the summary message. Run it.
	finalCmd := m.plugins.bulkRunNextItem()
	if finalCmd == nil {
		t.Fatal("expected final summary cmd")
	}
	finalMsg := finalCmd()
	im, _ = im.Update(finalMsg)

	view = im.View()
	if strings.Contains(view, "bulk update in progress…") {
		t.Errorf("in-progress line should be gone after final result; got:\n%s", view)
	}
	if !strings.Contains(view, "1 updated") || !strings.Contains(view, "1 already up to date") || !strings.Contains(view, "1 failed") {
		t.Errorf("expected summary '1 updated, 1 already up to date, 1 failed'; got:\n%s", view)
	}
	if m.plugins.bulkUpdating {
		t.Error("bulkUpdating should be false after final result")
	}
	if len(m.plugins.bulkTargets) != 0 || m.plugins.bulkIndex != 0 {
		t.Error("bulk-progress scratch should be cleared after final result")
	}
	// Per-item handler must have set dirtyPlugins as soon as the first item landed,
	// so an in-flight Q quit-confirmation still prompts to save even if the result
	// handler never runs.
	if !st.dirtyPlugins {
		t.Error("dirtyPlugins should be true after streaming applied at least one item")
	}
	// Streaming-flow result message carries streamed=true so the result handler
	// can skip the redundant UpdateInstall/InvalidatePlugin loop. Verify the flag
	// was set on the final message.
	bulkResult, ok := finalMsg.(pluginBulkUpdateResultMsg)
	if !ok {
		t.Fatalf("final message should be pluginBulkUpdateResultMsg; got %T", finalMsg)
	}
	if !bulkResult.streamed {
		t.Error("streaming path should set streamed=true so the result handler skips redundant apply")
	}
}

// TestTUIPluginBulkResultHandlerStillAppliesForDirectSender verifies the
// non-streaming code path (direct test sender, future CLI integrations) still
// runs the UpdateInstall+InvalidatePlugin loop in the result handler when
// streamed=false. Regression guard for the streamed-flag optimization.
func TestTUIPluginBulkResultHandlerStillAppliesForDirectSender(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	st.updates.PutPlugin("plug-one@mkt", updates.Status{Local: "old", Remote: "new", Outdated: true})
	m.plugins.rebuild()
	_ = drive(m, "2")

	var im tea.Model = m
	im, _ = im.Update(pluginBulkUpdateResultMsg{
		applied: []bulkUpdateApplied{
			{id: "plug-one@mkt", result: &install.Result{
				QualifiedID: "plug-one@mkt", InstallPath: "/x/1-new", Version: "2.0", GitCommitSha: "new",
			}, oldInstPath: "/x/1"},
		},
		// streamed: false (zero value) — handler runs full apply loop
	})
	_ = im
	if !st.dirtyPlugins {
		t.Error("dirtyPlugins should be set even for direct (non-streamed) result senders")
	}
	if _, ok := st.updates.Plugin("plug-one@mkt"); ok {
		t.Error("InvalidatePlugin should run for direct (non-streamed) result senders")
	}
}

// TestTUIPluginBulkNilResultIsTreatedAsFailure verifies the defensive guard
// in the pluginBulkItemDoneMsg switch: a {result: nil, err: nil} payload (which
// should never happen given install.Install's contract, but could from a buggy
// future caller) must not panic; it's classified as a failure instead.
func TestTUIPluginBulkNilResultIsTreatedAsFailure(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2") // plugins tab — routes pluginBulkItemDoneMsg to m.plugins.update
	m.plugins.bulkUpdating = true
	m.plugins.bulkTargets = []bulkUpdateTarget{
		{id: "plug-x@mkt", name: "plug-x", mkt: "mkt"},
	}

	var im tea.Model = m
	// No panic, no err, no result. Must classify as failed.
	im, _ = im.Update(pluginBulkItemDoneMsg{target: m.plugins.bulkTargets[0]})
	_ = im
	if len(m.plugins.bulkFailed) != 1 || m.plugins.bulkFailed[0].ID != "plug-x@mkt" {
		t.Errorf("nil-result payload should classify as failure; bulkFailed = %v", m.plugins.bulkFailed)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

// TestStaleUpdateCheckDiscarded verifies that an in-flight pluginUpdateCheckMsg
// arriving AFTER a local update bumped the SHA is discarded rather than re-poisoning
// the cache. This is the root cause of the "10 updates available" stuck-count bug:
// without this guard, a probe queued at tab-enter (computed against the pre-update
// SHA) lands after bulk update completed and writes Outdated=true back.
func TestStaleUpdateCheckDiscarded(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2")

	// Simulate that plug-one's local SHA has just been bumped to newshaFull,
	// but a pre-bump check is still in flight carrying Local="oldshaFull".
	id := "plug-one@mkt"
	// Hand-mutate installed_plugins SHA to the new value (as install.UpdateInstall would).
	plugins, _ := st.installed.Raw["plugins"].(map[string]any)
	if arr, ok := plugins[id].([]any); ok && len(arr) > 0 {
		if entry, ok := arr[0].(map[string]any); ok {
			entry["gitCommitSha"] = "newshaFull"
		}
	}

	// Confirm the cache is clean.
	st.updates.InvalidatePlugin(id)

	// Deliver the stale check.
	var im tea.Model = m
	im, _ = im.Update(pluginUpdateCheckMsg{
		id: id,
		status: updates.Status{
			Local:    "oldshaFull",
			Remote:   "remotesha",
			Outdated: true,
		},
	})

	// The stale result must NOT have landed in the cache.
	if s, ok := st.updates.Plugin(id); ok && s.Outdated {
		t.Fatalf("stale check should be discarded, but cache shows Outdated=true (Local=%q)", s.Local)
	}
}

// TestFreshUpdateCheckLandsInCache verifies the normal path still works: a check
// whose Local matches the on-disk SHA is stored and used to render Outdated.
func TestFreshUpdateCheckLandsInCache(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2")

	id := "plug-one@mkt"
	// On-disk SHA stays empty (buildState doesn't set one). The check's Local should
	// also be empty to be considered fresh — the stale guard only triggers when
	// status.Local is non-empty AND mismatches.
	var im tea.Model = m
	im, _ = im.Update(pluginUpdateCheckMsg{
		id: id,
		status: updates.Status{
			Local:    "",
			Remote:   "remotesha",
			Outdated: true,
		},
	})

	if s, ok := st.updates.Plugin(id); !ok || !s.Outdated {
		t.Fatalf("fresh check should land in cache, got ok=%v outdated=%v", ok, s.Outdated)
	}
}

// TestBulkUpdateRetainsFailureDetail verifies stderr / error text is captured per
// failure with a hint, and that the panel opens via `F` to show them.
func TestBulkUpdateRetainsFailureDetail(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2")

	m.plugins.bulkUpdating = true
	m.plugins.bulkTargets = []bulkUpdateTarget{
		{id: "plug-one@mkt", name: "plug-one", mkt: "mkt"},
	}

	var im tea.Model = m
	im, _ = im.Update(pluginBulkItemDoneMsg{
		target: m.plugins.bulkTargets[0],
		err:    errFake("git clone https://x.invalid: exit status 128\n--- stderr ---\nfatal: could not resolve host: x.invalid"),
	})
	if len(m.plugins.bulkFailed) != 1 {
		t.Fatalf("want 1 failure, got %d", len(m.plugins.bulkFailed))
	}
	f := m.plugins.bulkFailed[0]
	if !strings.Contains(f.Err, "could not resolve host") {
		t.Errorf("captured Err should include stderr text; got %q", f.Err)
	}
	if !strings.Contains(strings.ToLower(f.Hint), "network") {
		t.Errorf("classifyUpdateError should hint about network for resolve-host errors; got %q", f.Hint)
	}

	// Drive final summary so v.lastFailures is populated.
	finalCmd := m.plugins.bulkRunNextItem()
	im, _ = im.Update(finalCmd())

	// `F` opens the panel.
	view := drive(m, "F")
	if !strings.Contains(view, "Bulk-update failures") {
		t.Errorf("F should open the failures panel; got:\n%s", view)
	}
	if !strings.Contains(view, "plug-one@mkt") {
		t.Errorf("panel should list the failed plugin id; got:\n%s", view)
	}

	// Esc closes.
	_ = drive(m, "esc")
	if m.plugins.mode != "" {
		t.Errorf("esc should close failures panel, got mode=%q", m.plugins.mode)
	}
}

// TestClassifyUpdateErrorHints covers the substring buckets so future text changes
// in upstream git don't silently kill the hint UX.
func TestClassifyUpdateErrorHints(t *testing.T) {
	cases := map[string]string{
		"fatal: could not resolve host: github.com":  "network",
		"Permission denied (publickey)":               "permission",
		"fatal: couldn't find remote ref refs/heads/x": "marketplace",
		"error: 403":                                  "auth",
		"No space left on device":                     "disk",
		"git clone: random unknown error":             "details",
	}
	for input, mustContain := range cases {
		hint := classifyUpdateError(input)
		if !strings.Contains(strings.ToLower(hint), mustContain) {
			t.Errorf("classifyUpdateError(%q) = %q; want it to contain %q", input, hint, mustContain)
		}
	}
}

// TestSaveAndLoadLastFailuresRoundTrip verifies the persistence layer for the
// failures panel: a save followed by a fresh load returns the same set.
func TestSaveAndLoadLastFailuresRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := []bulkUpdateFailure{
		{ID: "plug-a@mkt", Err: "boom", Hint: "retry"},
		{ID: "plug-b@mkt", Err: "splat", Hint: "fix network"},
	}
	if err := saveLastFailures(dir, in); err != nil {
		t.Fatalf("saveLastFailures: %v", err)
	}
	out, ok := loadLastFailures(dir)
	if !ok {
		t.Fatal("loadLastFailures returned ok=false right after save")
	}
	if len(out) != 2 || out[0].ID != "plug-a@mkt" || out[1].Hint != "fix network" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	// Empty save should remove the file (clean run = no stale state).
	if err := saveLastFailures(dir, nil); err != nil {
		t.Fatalf("saveLastFailures(nil): %v", err)
	}
	if _, ok := loadLastFailures(dir); ok {
		t.Error("loadLastFailures should report ok=false after empty save")
	}
}

// TestRetrySuccessClearsFailureRecord: a successful pluginUpdateResultMsg for
// an id that's in lastFailures must remove the entry so the panel auto-empties.
func TestRetrySuccessClearsFailureRecord(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2")

	m.plugins.lastFailures = []bulkUpdateFailure{
		{ID: "plug-one@mkt", Err: "boom", Hint: "retry"},
		{ID: "plug-two@mkt", Err: "splat", Hint: "fix net"},
	}
	m.plugins.lastFailuresLoaded = true

	var im tea.Model = m
	im, _ = im.Update(pluginUpdateResultMsg{
		id:          "plug-one@mkt",
		oldSha:      "old",
		oldInstPath: "/x/1",
		result: &install.Result{
			QualifiedID: "plug-one@mkt", InstallPath: "/x/1-new", Version: "v2", GitCommitSha: "new",
		},
	})

	if len(m.plugins.lastFailures) != 1 || m.plugins.lastFailures[0].ID != "plug-two@mkt" {
		t.Fatalf("plug-one should be dropped from lastFailures, got %+v", m.plugins.lastFailures)
	}
}

// TestStaleUpdateCheckForUninstalledPluginInvalidates: when the probe arrives
// for a plugin that's been uninstalled, the cache entry must be cleared rather
// than poisoned with stale data.
func TestStaleUpdateCheckForUninstalledPluginInvalidates(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	_ = drive(m, "2")

	st.updates.PutPlugin("ghost@mkt", updates.Status{Local: "old", Remote: "new", Outdated: true})

	var im tea.Model = m
	im, _ = im.Update(pluginUpdateCheckMsg{
		id: "ghost@mkt",
		status: updates.Status{Local: "old", Remote: "new", Outdated: true},
	})

	if _, ok := st.updates.Plugin("ghost@mkt"); ok {
		t.Fatalf("uninstalled plugin's cache entry should be cleared, but Plugin() still returns ok")
	}
}

// TestDropFailureHelper covers the ID-removal helper.
func TestDropFailureHelper(t *testing.T) {
	xs := []bulkUpdateFailure{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	if !dropFailure(&xs, "b") {
		t.Fatal("expected dropFailure to report removed=true for present id")
	}
	if len(xs) != 2 || xs[0].ID != "a" || xs[1].ID != "c" {
		t.Fatalf("after drop got %+v", xs)
	}
	if dropFailure(&xs, "missing") {
		t.Fatal("expected removed=false for absent id")
	}
	var nilSlice []bulkUpdateFailure
	if dropFailure(&nilSlice, "x") {
		t.Fatal("nil slice should report removed=false")
	}
}
