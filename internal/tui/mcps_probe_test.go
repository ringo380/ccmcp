package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		// `copy nul <path>` takes the path as a separate argument, so a
		// directory with spaces survives cmd.exe's quoting; a redirection
		// (`type nul > path`) does not.
		return map[string]any{
			"command": "cmd.exe",
			"args":    []any{"/c", "copy", "nul", path},
		}
	}
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

// runCmd executes a tea.Cmd and returns its message, draining tea.Batch
// RECURSIVELY so a command nested at any depth still runs.
//
// The depth matters: TestMCPsTabNeverProbesImplicitly claims probing never
// happens implicitly, full stop, and a one-level drainer would let a probe
// hidden inside a batch-of-batches slip past the assertion while the test still
// reported green. `depth` only guards against a pathological self-referential
// batch; real nesting here is one or two levels.
//
// Note tea.Batch collapses a single-command batch to that command, so genuine
// nesting requires two or more commands per level - and tea.Sequence's message
// type is unexported, so a sequenced command cannot be unwrapped this way. The
// implicit paths in this view use Batch, not Sequence.
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	return drainCmd(t, cmd, 0)
}

func drainCmd(t *testing.T, cmd tea.Cmd, depth int) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	// Fails rather than returning nil: silently abandoning a command is the one
	// outcome the implicit-probe test must never produce, and a nest deeper than
	// this is precisely how a probe would slip past it unnoticed. The cap exists
	// only to stop a self-referential batch from recursing forever.
	if depth > 16 {
		t.Fatalf("command nesting exceeded depth %d - a command was left undrained, "+
			"so an implicit probe could run unobserved", depth)
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drainCmd(t, c, depth+1)
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
		runCmd(t, cmd)
	}
	m.mcps.rebuild()
	_ = im.View()
	_ = m.mcps.render()
	runCmd(t, m.tabEnterCmd())
	runCmd(t, m.mcps.initialCheckCmd())

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
	msg := runCmd(t, cmd)
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
		runCmd(t, cmd)
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
		msg := runCmd(t, cmd)
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
	// Without this the test passes vacuously: if `P` stopped prompting at all,
	// there would be nothing to cancel and the sentinel would stay absent for the
	// wrong reason.
	if !m.mcps.bulkProbeConfirm {
		t.Fatal("P must have entered the confirmation state before esc is meaningful")
	}
	if len(m.mcps.bulkProbeTargets) == 0 {
		t.Fatal("the confirmation must have staged at least one target")
	}
	im, cmd := press(im, "esc")
	runCmd(t, cmd)
	if exists(sentinel) {
		t.Fatal("cancelling the bulk-probe confirmation must not probe anything")
	}
	if len(m.mcps.bulkProbeTargets) != 0 {
		t.Fatal("cancelling must drop the staged targets")
	}
	if m.mcps.capturingInput() {
		t.Fatal("esc must leave the confirmation mode")
	}
}

// TestOutOfBandProbeDoesNotAdvanceTheSweep: a sweep must count only its OWN
// probes. Attributing any arriving result to it advanced the index and fired the
// "probed N server(s)" completion while probes were still in flight, so the
// sweep silently lost its one-at-a-time guarantee and then lied about finishing.
// The single-probe key is separately guarded from starting one at all mid-sweep.
func TestOutOfBandProbeDoesNotAdvanceTheSweep(t *testing.T) {
	aCfg := map[string]any{"command": "a-cmd"}
	zCfg := map[string]any{"command": "z-cmd"}
	other := map[string]any{"command": "other-cmd"}
	st, _ := buildProbeState(t, map[string]any{"aaa": aCfg, "zzz": zCfg, "other": other})
	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Stage and confirm a sweep, but do NOT run the command - the sweep is now
	// mid-flight with its first probe outstanding.
	im, _ = press(im, "P")
	im, cmd := press(im, "P")
	if cmd == nil {
		t.Fatal("confirming must start the sweep")
	}
	total := len(m.mcps.bulkProbeTargets)
	if total < 2 {
		t.Fatalf("fixture: need a multi-target sweep, got %d", total)
	}
	idxBefore, inflightBefore := m.mcps.bulkProbeIndex, m.mcps.probeInFlight
	if inflightBefore != 1 {
		t.Fatalf("the sweep must have exactly one probe in flight, got %d", inflightBefore)
	}

	// An untagged result arrives - what a single-row probe would deliver.
	target, _, _ := mcpprobe.TargetFromConfig("other", other)
	im, _ = im.Update(mcpProbeDoneMsg{res: mcpprobe.Result{
		Key: target.Key(), Name: "other", OK: true, InstructionTokens: 10, NameTokens: 1,
	}})

	if m.mcps.bulkProbeIndex != idxBefore {
		t.Fatalf("an out-of-band result must not advance the sweep index (%d -> %d)",
			idxBefore, m.mcps.bulkProbeIndex)
	}
	if len(m.mcps.bulkProbeTargets) != total {
		t.Fatalf("an out-of-band result must not end the sweep (%d targets -> %d)",
			total, len(m.mcps.bulkProbeTargets))
	}
	view := stripANSI(im.View())
	if strings.Contains(view, "probed 2 server(s)") || strings.Contains(view, "probed 3 server(s)") {
		t.Fatalf("the sweep must not claim completion while its probes are outstanding:\n%s", view)
	}

	// And the single-probe key cannot start one mid-sweep in the first place.
	before := m.mcps.probeInFlight
	im, single := press(im, "p")
	if single != nil {
		t.Fatal("p must not start a probe while another is in flight")
	}
	if m.mcps.probeInFlight != before {
		t.Fatalf("a refused probe must not change the in-flight count (%d -> %d)",
			before, m.mcps.probeInFlight)
	}
	if out := stripANSI(im.View()); !strings.Contains(out, "already running") {
		t.Fatalf("refusing a concurrent probe must say why:\n%s", out)
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

// TestDuplicateNameRendersUnmeasuredNotTheFirstRowsCost: Input.MCP is keyed by
// display name, so two effective rows sharing a name cannot both be represented.
// Publishing the first row's measurement would read as a COMPLETE figure while
// the second real server silently vanished from the total - the same failure mode
// as the absent-server contract. The name must read "-" and count as unmeasured.
func TestDuplicateNameRendersUnmeasuredNotTheFirstRowsCost(t *testing.T) {
	userCfg := map[string]any{"command": "dup-user-cmd"}
	st, p := buildProbeState(t, map[string]any{
		"dup":  userCfg,
		"solo": map[string]any{"command": "solo-cmd"},
	})
	// A second effective source for the same display name, with a different
	// config (so it is a genuinely distinct server, not the same one twice).
	localCfg := map[string]any{"command": "dup-local-cmd"}
	st.cj.SetProjectMCP(st.project, "dup", localCfg)
	// BOTH colliding rows are measured, deliberately: with only one of them
	// probed, the test passed even with the collision guard removed, because the
	// row that happens to win sort order was the unprobed one. Seeding both means
	// whichever row wins, a measurement is available to be wrongly published.
	seedProbe(t, p, "dup", userCfg, 500, 400, 20)  // loaded 900
	seedProbe(t, p, "dup", localCfg, 300, 200, 10) // loaded 500
	// A second, uncontested server that IS measured, so the header carries a real
	// total and the unmeasured tally is unambiguous.
	seedProbe(t, p, "solo", map[string]any{"command": "solo-cmd"}, 100, 50, 5)
	st.probes = nil

	m := newModel(st)
	m.mcps.rebuild()

	var effRows int
	for _, r := range m.mcps.rows {
		if r.Name == "dup" && isEffective(r) {
			effRows++
		}
	}
	if effRows != 2 {
		t.Fatalf("fixture: expected 2 effective rows named dup, got %d", effRows)
	}

	idx := st.costIndex()
	b, ok := idx.ByMCP["dup"]
	if !ok {
		t.Fatal("a colliding name must still appear in the accounting")
	}
	if b.Items != 0 || b.Unmeasured != 1 {
		t.Fatalf("a colliding name must be unmeasured, got Items=%d Unmeasured=%d", b.Items, b.Unmeasured)
	}
	// 150 is solo alone. 1050 would mean the colliding name smuggled its
	// surviving row's 900 into a figure that omits the other real server.
	if got := idx.Project.MCP.Loaded; got != 150 {
		t.Fatalf("a colliding name must contribute no cost to the total; want 150 got %d", got)
	}

	out := stripANSI(drive(m, "1"))
	for _, forbidden := range []string{"≈900", "≈500", "≈1.0k", "≈650"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("the surviving row's measurement (%s) must not be presented as the name's cost:\n%s",
				forbidden, out)
		}
	}
	dash := fmt.Sprintf("%-28s %s %8s", "dup", "[u]", "-")
	if !strings.Contains(out, dash) {
		t.Fatalf("a colliding name must render %q:\n%s", dash, out)
	}
	if !strings.Contains(out, "(1 unmeasured)") {
		t.Fatalf("the colliding name must count toward the header's unmeasured tally:\n%s", out)
	}
	// The row must also SAY why it reads "-". A dash the user cannot account for
	// is only half the fix, and the header tally alone does not name the row.
	if !strings.Contains(out, "duplicate server name") {
		t.Fatalf("the colliding row must explain its own dash:\n%s", out)
	}
	// The reason folds into the existing description budget, so the common case
	// keeps showing the command rather than boilerplate.
	if strings.Contains(out, "not probed yet") {
		t.Fatalf("the routine never-probed reason must not crowd out the description:\n%s", out)
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
			// EVERY line, not just the two header lines: the earlier lines[:2]
			// slice left both conditional lines (spinner, bulk confirm) unchecked,
			// and the confirm prompt was 82 columns.
			assertAllLinesFit(t, lines, w)
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

			// Same body with the bulk-probe confirmation open. w=80 is the real
			// working width here, not an edge case, and a confirmation whose only
			// cancel hint is clipped away is a defect however narrow the terminal.
			var im tea.Model = m
			im, _ = im.Update(key("P"))
			if !m.mcps.bulkProbeConfirm {
				t.Fatal("P must open the confirmation")
			}
			cbody := stripANSI(m.mcps.render())
			clines := strings.Split(cbody, "\n")
			assertAllLinesFit(t, clines, w)
			if got := physicalRows(t, cbody, w); got != len(clines) {
				t.Fatalf("w=%d with the prompt open: %d logical lines rendered as %d physical rows",
					w, len(clines), got)
			}
			if len(clines) != want+1 {
				t.Fatalf("w=%d: the prompt must cost exactly one line (%d -> %d)", w, want, len(clines))
			}
			if !strings.Contains(cbody, "esc: cancel") {
				t.Fatalf("w=%d: the cancel hint must survive the width clamp:\n%s", w, cbody)
			}
			if !strings.Contains(cbody, "P: confirm") {
				t.Fatalf("w=%d: the confirm hint must survive the width clamp:\n%s", w, cbody)
			}
			// The per-turn figure must still be there with the prompt open.
			if !strings.Contains(clines[1], "loaded") {
				t.Fatalf("w=%d: the per-turn header line must survive: %q", w, clines[1])
			}
		})
	}
}

// assertAllLinesFit fails if any rendered line exceeds w display columns.
func assertAllLinesFit(t *testing.T, lines []string, w int) {
	t.Helper()
	for i, ln := range lines {
		if cols := lipgloss.Width(ln); cols > w {
			t.Fatalf("line %d is %d columns at w=%d: %q", i, cols, w, ln)
		}
	}
}
