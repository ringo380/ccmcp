package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
	"github.com/ringo380/ccmcp/internal/paths"
)

// buildProbeState builds a sandboxed state whose user scope holds exactly the
// given MCP entries. Separate from buildState because every probe test needs to
// control the command each server runs (and therefore what a probe would do to
// the filesystem), which buildState's fixed fixture cannot express.
func buildProbeState(t *testing.T, userMCPs map[string]any) (*state, paths.Paths) {
	t.Helper()
	home := t.TempDir()
	write := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".claude.json"), map[string]any{
		"anonymousId": "sandbox",
		"mcpServers":  userMCPs,
	})
	write(filepath.Join(home, ".claude-mcp-stash.json"), map[string]any{
		"userMcpServers": map[string]any{
			"stashed-only": map[string]any{"command": "never-run"},
		},
	})
	write(filepath.Join(home, ".claude-mcp-profiles.json"), map[string]any{})
	write(filepath.Join(home, ".claude-mcp-config.json"), map[string]any{})
	write(filepath.Join(home, ".claude", "settings.json"), map[string]any{})
	write(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{},
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
		ProbeCache:       filepath.Join(home, ".claude-mcp-probe-cache.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
		AppConfig:        filepath.Join(home, ".claude-mcp-config.json"),
	}
	st, err := loadState(p, filepath.Join(home, "project"))
	if err != nil {
		t.Fatal(err)
	}
	return st, p
}

// sentinelServer is an MCP config whose "server" does nothing but create a file
// and exit. Probing it fails (it never speaks JSON-RPC), which is the point: the
// sentinel records that a subprocess was spawned at all.
func sentinelServer(path string) map[string]any {
	return map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "touch " + path},
	}
}

// exists reports whether path is present.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runCmd executes a tea.Cmd and returns its message, unwrapping one level of
// tea.Batch so a batched command's children run too.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				c()
			}
		}
		return nil
	}
	return msg
}

// press sends one key and returns the command the model produced, so a test can
// see whether work was scheduled and run it deliberately.
func press(im tea.Model, k string) (tea.Model, tea.Cmd) {
	return im.Update(key(k))
}

// TestMCPsTabNeverProbesImplicitly is the guard that matters most in this
// feature: a probe spawns a real subprocess from user config, so it must happen
// only when the user asks. Every implicit path the tab has - first render,
// resize, filter, scope cycle, toggle, bulk toggle, hidden-row reveal,
// navigation, tab re-entry and rebuild - is driven here against a server whose
// command touches a sentinel file, and any spawn at all leaves that file behind.
func TestMCPsTabNeverProbesImplicitly(t *testing.T) {
	st, p := buildProbeState(t, nil)
	sentinel := filepath.Join(p.Home, "implicit-probe-ran")
	st.cj.SetUserMCP("sentinel", sentinelServer(sentinel))
	st.cj.SetUserMCP("second", sentinelServer(sentinel+"-2"))

	m := newModel(st)
	var im tea.Model = m
	// Every message below is an implicit path; commands they return are executed
	// so a probe scheduled as a tea.Cmd is caught too, not just an inline one.
	steps := []tea.Msg{
		tea.WindowSizeMsg{Width: 120, Height: 40},
		key("1"),
		tea.WindowSizeMsg{Width: 80, Height: 24},
		key("/"), key("s"), key("enter"), key("c"),
		key("j"), key("k"), key("G"), key("g"), key("pgdn"), key("pgup"),
		key("H"), key("H"),
		key(" "), key(" "),
		key("A"), key("N"),
		key("s"), key("s"), key("s"), key("s"), key("s"),
		key("R"), key("m"), key("esc"),
		key("2"), key("1"),
	}
	for _, msg := range steps {
		var cmd tea.Cmd
		im, cmd = im.Update(msg)
		runCmd(cmd)
	}
	m.mcps.rebuild()
	_ = im.View()
	_ = m.mcps.render()
	runCmd(m.tabEnterCmd())
	runCmd(m.mcps.initialCheckCmd())

	if exists(sentinel) || exists(sentinel+"-2") {
		t.Fatalf("the MCPs tab spawned an MCP server without the user asking: %s", sentinel)
	}
}

func TestProbeKeyProbesOnlySelectedRow(t *testing.T) {
	st, p := buildProbeState(t, nil)
	aSentinel := filepath.Join(p.Home, "ran-aaa")
	bSentinel := filepath.Join(p.Home, "ran-zzz")
	st.cj.SetUserMCP("aaa", sentinelServer(aSentinel))
	st.cj.SetUserMCP("zzz", sentinelServer(bSentinel))

	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	visible := m.mcps.visibleRows()
	if len(visible) < 2 || visible[0].Name != "aaa" {
		t.Fatalf("fixture: expected aaa first, got %+v", visible)
	}
	im, cmd := press(im, "p")
	if cmd == nil {
		t.Fatal("p must schedule a probe for the selected row")
	}
	msg := runCmd(cmd)
	if !exists(aSentinel) {
		t.Fatal("p did not probe the selected row")
	}
	if exists(bSentinel) {
		t.Fatal("p probed a row that was not selected")
	}
	done, ok := msg.(mcpProbeDoneMsg)
	if !ok {
		t.Fatalf("probe must report back as mcpProbeDoneMsg, got %T", msg)
	}
	if done.res.Name != "aaa" {
		t.Fatalf("probe result is for the wrong server: %q", done.res.Name)
	}
	im, _ = im.Update(done)
	if _, hit := st.probeCache().Get(done.res.Key); !hit {
		t.Fatal("a completed probe must be recorded in the cache")
	}
	_ = im
}

func TestBulkProbeAsksForConfirmationFirst(t *testing.T) {
	st, p := buildProbeState(t, nil)
	aSentinel := filepath.Join(p.Home, "bulk-aaa")
	bSentinel := filepath.Join(p.Home, "bulk-zzz")
	st.cj.SetUserMCP("aaa", sentinelServer(aSentinel))
	st.cj.SetUserMCP("zzz", sentinelServer(bSentinel))

	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	im, cmd := press(im, "P")
	if cmd != nil {
		runCmd(cmd)
	}
	if exists(aSentinel) || exists(bSentinel) {
		t.Fatal("P must not spawn anything before the user confirms")
	}
	out := stripANSI(im.View())
	if !strings.Contains(out, "confirm") {
		t.Fatalf("P must ask for confirmation first:\n%s", out)
	}

	// Confirming runs the sweep, one server at a time: each result chains the next.
	im, cmd = press(im, "P")
	for i := 0; i < 5 && cmd != nil; i++ {
		msg := runCmd(cmd)
		if msg == nil {
			break
		}
		im, cmd = im.Update(msg)
	}
	if !exists(aSentinel) || !exists(bSentinel) {
		t.Fatalf("confirmed bulk probe must probe every visible server (aaa=%v zzz=%v)",
			exists(aSentinel), exists(bSentinel))
	}
}

func TestBulkProbeCancelDoesNotProbe(t *testing.T) {
	st, p := buildProbeState(t, nil)
	sentinel := filepath.Join(p.Home, "cancelled")
	st.cj.SetUserMCP("aaa", sentinelServer(sentinel))

	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	im, _ = press(im, "P")
	im, cmd := press(im, "esc")
	runCmd(cmd)
	if exists(sentinel) {
		t.Fatal("cancelling the bulk-probe confirmation must not probe anything")
	}
	if m.mcps.capturingInput() {
		t.Fatal("esc must leave the confirmation mode")
	}
}

// seedProbe records a successful probe for name's config so the row renders a
// measured cost without spawning anything.
func seedProbe(t *testing.T, p paths.Paths, name string, cfg any, instr, schema, names int) mcpprobe.Result {
	t.Helper()
	target, ok, reason := mcpprobe.TargetFromConfig(name, cfg)
	if !ok {
		t.Fatalf("fixture config for %s is not probeable: %s", name, reason)
	}
	res := mcpprobe.Result{
		Key:               target.Key(),
		Name:              name,
		OK:                true,
		InstructionTokens: instr,
		Tools:             []mcpprobe.Tool{{Name: "t1", SchemaTokens: schema}},
		NameTokens:        names,
	}
	c, err := mcpprobe.LoadCache(p.ProbeCache)
	if err != nil {
		t.Fatal(err)
	}
	c.Put(res)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestProbedRowShowsCostUnprobedShowsDash(t *testing.T) {
	probedCfg := map[string]any{"command": "probed-cmd"}
	cfgs := map[string]any{
		"probed":   probedCfg,
		"unprobed": map[string]any{"command": "unprobed-cmd"},
	}
	st, p := buildProbeState(t, cfgs)
	seedProbe(t, p, "probed", probedCfg, 120, 80, 10)
	st.probes = nil // force a reload of the freshly seeded cache

	m := newModel(st)
	m.mcps.rebuild()
	out := stripANSI(drive(m, "1"))

	if !strings.Contains(out, "≈200") {
		t.Fatalf("the probed row must show its measured loaded cost (≈200):\n%s", out)
	}
	if strings.Contains(out, "≈0") {
		t.Fatalf("nothing may render as a confident ≈0:\n%s", out)
	}
	dash := fmt.Sprintf("%-28s %s %8s", "unprobed", "[u]", "-")
	if !strings.Contains(out, dash) {
		t.Fatalf("an unprobed row must render %q, never a figure:\n%s", dash, out)
	}
	// A row that is not in the accounting at all (stash never loads, so it is
	// excluded from Input.MCP) must also read as unmeasured rather than zero:
	// a missing ByMCP key is a zero-value Breakdown whose Total() is 0.
	m.mcps.showHidden = true
	out = stripANSI(m.mcps.render())
	stash := fmt.Sprintf("%-28s %s %8s", "stashed-only", "[s]", "-")
	if !strings.Contains(out, stash) {
		t.Fatalf("a row absent from the accounting must render %q:\n%s", stash, out)
	}
}

func TestRemoteRowRefusesProbeWithReason(t *testing.T) {
	cfgs := map[string]any{
		"remote": map[string]any{"type": "http", "url": "https://example.invalid/mcp"},
	}
	st, _ := buildProbeState(t, cfgs)
	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	visible := m.mcps.visibleRows()
	if len(visible) == 0 || visible[0].Name != "remote" {
		t.Fatalf("fixture: expected the remote row first, got %+v", visible)
	}
	im, cmd := press(im, "p")
	if cmd != nil {
		t.Fatal("a remote row must not schedule a probe")
	}
	out := stripANSI(im.View())
	if !strings.Contains(out, "cannot probe locally") {
		t.Fatalf("refusing to probe a remote row must say why:\n%s", out)
	}
	if !strings.Contains(out, "remote") {
		t.Fatalf("the refusal must name the row:\n%s", out)
	}
}

func TestProbeBumpsTheCostGeneration(t *testing.T) {
	aCfg := map[string]any{"command": "a-cmd"}
	zCfg := map[string]any{"command": "z-cmd"}
	st, _ := buildProbeState(t, map[string]any{"aaa": aCfg, "zzz": zCfg})
	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	before := st.mcpGen
	st.costIndex() // warm the cache so a missed invalidation serves stale data

	deliver := func(name string, cfg any, loaded int) int {
		target, ok, _ := mcpprobe.TargetFromConfig(name, cfg)
		if !ok {
			t.Fatalf("fixture %s not probeable", name)
		}
		im, _ = im.Update(mcpProbeDoneMsg{res: mcpprobe.Result{
			Key: target.Key(), Name: name, OK: true,
			InstructionTokens: loaded,
			NameTokens:        1,
		}})
		return st.costIndex().Project.MCP.Loaded
	}

	firstTotal := deliver("aaa", aCfg, 100)
	afterFirst := st.mcpGen
	if afterFirst <= before {
		t.Fatalf("a completed probe must advance the MCP generation (%d -> %d)", before, afterFirst)
	}
	if firstTotal != 100 {
		t.Fatalf("after the first probe the total must be 100, got %d", firstTotal)
	}

	// The second consecutive probe is the one a boolean dirty flag cannot see.
	secondTotal := deliver("zzz", zCfg, 50)
	if st.mcpGen <= afterFirst {
		t.Fatalf("a second consecutive probe must advance the generation again (%d -> %d)",
			afterFirst, st.mcpGen)
	}
	if secondTotal != 150 {
		t.Fatalf("after the second probe the total must be 150, got %d (stale cache)", secondTotal)
	}
}

// TestProbeResultLandsWhileAnotherTabIsActive: updateActive() only dispatches to
// the focused tab, so a probe started on MCPs and completed after a tab switch
// would otherwise be dropped - the cache never written and a bulk sweep stalled.
func TestProbeResultLandsWhileAnotherTabIsActive(t *testing.T) {
	cfg := map[string]any{"command": "a-cmd"}
	st, _ := buildProbeState(t, map[string]any{"aaa": cfg})
	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	im, _ = press(im, "2") // switch to Plugins while the probe is in flight

	target, _, _ := mcpprobe.TargetFromConfig("aaa", cfg)
	im, _ = im.Update(mcpProbeDoneMsg{res: mcpprobe.Result{
		Key: target.Key(), Name: "aaa", OK: true, InstructionTokens: 42, NameTokens: 1,
	}})
	if _, hit := st.probeCache().Get(target.Key()); !hit {
		t.Fatal("a probe result must be recorded even when another tab is focused")
	}
}

// TestUnprobedServerStaysInTheAccounting pins the completeness contract on
// ctxcost.Input.MCP: a configured server that is absent from the map is
// invisible to Build - it adds nothing to the total AND nothing to Unmeasured -
// so populating only cache hits would print a confident headline that silently
// omits every unprobed server.
func TestUnprobedServerStaysInTheAccounting(t *testing.T) {
	probedCfg := map[string]any{"command": "probed-cmd"}
	cfgs := map[string]any{
		"probed":   probedCfg,
		"unprobed": map[string]any{"command": "unprobed-cmd"},
		"remote":   map[string]any{"type": "http", "url": "https://example.invalid/mcp"},
	}
	st, p := buildProbeState(t, cfgs)
	seedProbe(t, p, "probed", probedCfg, 120, 80, 10)
	st.probes = nil

	m := newModel(st)
	m.mcps.rebuild()
	idx := st.costIndex()

	for _, name := range []string{"probed", "unprobed", "remote"} {
		if _, ok := idx.ByMCP[name]; !ok {
			t.Fatalf("configured server %q went missing from the accounting: %+v", name, idx.ByMCP)
		}
	}
	if got := idx.Project.Unmeasured; got != 2 {
		t.Fatalf("both unprobed servers must be counted as unmeasured, got %d", got)
	}
	if got := idx.Project.MCP.Loaded; got != 200 {
		t.Fatalf("only the probed server contributes cost; want 200 got %d", got)
	}
	out := stripANSI(m.mcps.render())
	if !strings.Contains(out, "2 unmeasured") {
		t.Fatalf("the header must report the unmeasured servers:\n%s", out)
	}
}

// TestMCPsHeaderGeometryWithProbeData proves the header change numerically: the
// per-turn line now carries a loaded/deferred pair plus an unmeasured count, and
// the height budget has no slack for a wrapped line.
func TestMCPsHeaderGeometryWithProbeData(t *testing.T) {
	probedCfg := map[string]any{"command": "probed-cmd"}
	for _, w := range []int{80, 100} {
		t.Run(fmt.Sprintf("w%d", w), func(t *testing.T) {
			st, p := buildProbeState(t, map[string]any{
				"probed":   probedCfg,
				"unprobed": map[string]any{"command": "unprobed-cmd"},
			})
			seedProbe(t, p, "probed", probedCfg, 1200, 8000, 100)
			st.probes = nil

			m := newModel(st)
			m.mcps.rebuild()
			m.Update(tea.WindowSizeMsg{Width: w, Height: 30})

			body := stripANSI(m.mcps.render())
			lines := strings.Split(body, "\n")
			// title + per-turn context line, then the rows. No transcript exists
			// in the sandbox, so there is no session-start line.
			if len(lines) < 2 {
				t.Fatalf("header is missing lines:\n%s", body)
			}
			if !strings.Contains(lines[1], "loaded") || !strings.Contains(lines[1], "deferred") {
				t.Fatalf("the header must render the loaded/deferred pair: %q", lines[1])
			}
			for i, ln := range lines[:2] {
				if cols := lipgloss.Width(ln); cols > w {
					t.Fatalf("header line %d is %d columns at w=%d: %q", i, cols, w, ln)
				}
			}
			if got := physicalRows(t, body, w); got != len(lines) {
				t.Fatalf("w=%d: %d logical lines rendered as %d physical rows", w, len(lines), got)
			}
			// Exact line accounting: 2 header lines, one line per visible row, and
			// the trailing newline the row loop leaves behind. Anything else means
			// the header grew or shrank a line the height budget did not pay for.
			headerRows := 2
			want := headerRows + len(m.mcps.visibleRows()) + 1
			if len(lines) != want {
				t.Fatalf("w=%d: expected %d lines (%d header + %d rows + trailing), got %d:\n%s",
					w, want, headerRows, len(m.mcps.visibleRows()), len(lines), body)
			}
		})
	}
}
