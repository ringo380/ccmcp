package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
)

// fakeserverBin builds the mcpprobe stdio fixture once per test binary. The TUI
// probe-cancellation tests need a server that really hangs and really spawns a
// child, because the thing under test is whether a PROCESS died - a bool on a
// struct proves nothing about that.
var (
	fakeserverOnce sync.Once
	fakeserverPath string
	fakeserverErr  error
)

func fakeserverBin(t *testing.T) string {
	t.Helper()
	fakeserverOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ccmcp-tui-fakeserver")
		if err != nil {
			fakeserverErr = err
			return
		}
		fakeserverPath = filepath.Join(dir, "fakeserver")
		if runtime.GOOS == "windows" {
			fakeserverPath += ".exe" // CreateProcess needs the extension
		}
		build := exec.Command("go", "build", "-o", fakeserverPath, "../mcpprobe/testdata/fakeserver")
		if out, err := build.CombinedOutput(); err != nil {
			fakeserverErr = err
			t.Logf("building fakeserver: %s", out)
		}
	})
	if fakeserverErr != nil {
		t.Fatalf("cannot build the fakeserver fixture: %v", fakeserverErr)
	}
	return fakeserverPath
}

// pidsFromFixture waits for the fixture to record its pid and its child's.
func pidsFromFixture(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			var pids []int
			for _, f := range strings.Fields(string(b)) {
				n, convErr := strconv.Atoi(f)
				if convErr != nil {
					t.Fatalf("bad pid %q in %s", f, path)
				}
				pids = append(pids, n)
			}
			if len(pids) == 2 {
				return pids
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture never wrote two pids to %s (err=%v)", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// processGone and killPid live in proc_unix_test.go / proc_windows_test.go:
// liveness is a per-OS question (signal-zero plus /proc zombie state on Unix,
// GetExitCodeProcess on Windows).

// waitProcessGone polls processGone for up to d.
//
// The bound absorbs reaping and scheduling latency ONLY, and cannot let a broken
// teardown pass: with the quit path's wait removed, the server is genuinely
// RUNNING for the rest of mcpprobe's 10s timeout - two orders of magnitude
// beyond this window - and the zero-grace session.done assertion in the test is
// unchanged. Both mutations were re-run on linux/amd64 and darwin/arm64 with
// this helper in place and still fail.
func waitProcessGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if processGone(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hangingServer is an MCP entry pointing at the fixture in a mode where it reads
// input, answers nothing, and spawns a child that outlives it - the shape of a
// real npx-style server that wedges.
func hangingServer(bin, pidfile string) map[string]any {
	return map[string]any{
		"command": bin,
		"env":     map[string]any{"FAKE_MODE": "hangchild", "FAKE_PIDFILE": pidfile},
	}
}

// TestSweepCanBeCancelledMidFlight: a confirmed `P` sweep was uninterruptible -
// esc/q were wired only inside the confirmation block, which clears the moment
// the sweep starts, and probeDone chained into the next target unconditionally.
// A ten-server sweep therefore held the user for up to ten probe timeouts.
func TestSweepCanBeCancelledMidFlight(t *testing.T) {
	st, p := buildProbeState(t, nil)
	first := filepath.Join(p.Home, "sweep-aaa")
	second := filepath.Join(p.Home, "sweep-zzz")
	st.cj.SetUserMCP("aaa", sentinelServer(first))
	st.cj.SetUserMCP("zzz", sentinelServer(second))

	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	im, _ = press(im, "P")
	im, cmd := press(im, "P") // confirm - the sweep is now running its first probe
	if cmd == nil {
		t.Fatal("confirming must start the sweep")
	}
	if !m.mcps.sweepActive() {
		t.Fatal("the sweep must be active after confirmation")
	}
	if len(m.mcps.probeSessions) != 1 {
		t.Fatalf("the running probe must be tracked so it can be cancelled, got %d sessions",
			len(m.mcps.probeSessions))
	}

	// esc while the sweep runs stops it. It must reach the view rather than
	// quitting the app, which is what the global key handler would otherwise do.
	im, _ = press(im, "esc")
	if m.mcps.sweepActive() {
		t.Fatal("esc must stop the sweep")
	}
	if len(m.mcps.bulkProbeTargets) != 0 {
		t.Fatal("cancelling must drop the remaining targets so nothing chains")
	}
	if !m.mcps.probeSessions[0].cancelled {
		t.Fatal("the in-flight probe must be cancelled, not merely abandoned")
	}
	view := stripANSI(im.View())
	if !strings.Contains(view, "cancelled") {
		t.Fatalf("cancelling must say so:\n%s", view)
	}

	// The in-flight probe's result arrives after the cancellation. It must not
	// chain into the second server, and must not be cached: it describes the
	// cancellation, not the server.
	msg := runCmd(t, cmd)
	done, ok := msg.(mcpProbeDoneMsg)
	if !ok {
		t.Fatalf("expected a probe result, got %T", msg)
	}
	im, next := im.Update(done)
	if next != nil {
		if follow := runCmd(t, next); follow != nil {
			t.Fatalf("a cancelled sweep must not chain another probe, got %T", follow)
		}
	}
	if exists(second) {
		t.Fatal("cancelling must stop the sweep before it spawns the next server")
	}
	if _, hit := st.probeCache().Get(done.res.Key); hit {
		t.Fatal("a cancelled probe is not a measurement of the server - it must not be cached")
	}
	if len(m.mcps.probeSessions) != 0 {
		t.Fatalf("a finished probe's session must be dropped, got %d", len(m.mcps.probeSessions))
	}
}

// TestProbeContextIsCancellable pins that the probe actually runs under the
// session's context. probeCmd previously passed context.Background(), so there
// was no cancel handle at all and the only bound was mcpprobe's own 10s timeout.
// Cancelling before the command runs must make it return far sooner than that.
func TestProbeContextIsCancellable(t *testing.T) {
	bin := fakeserverBin(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	st, _ := buildProbeState(t, map[string]any{"hang": hangingServer(bin, pidfile)})
	v := newMCPView(st)

	target, ok, reason := mcpprobe.TargetFromConfig("hang", hangingServer(bin, pidfile))
	if !ok {
		t.Fatalf("fixture should be probeable: %s", reason)
	}
	cmd := v.startProbe(target, false)
	if len(v.probeSessions) != 1 {
		t.Fatalf("startProbe must register a session, got %d", len(v.probeSessions))
	}
	if n := v.cancelProbes(false); n != 1 {
		t.Fatalf("cancelProbes should have cancelled 1 probe, got %d", n)
	}

	start := time.Now()
	msg := cmd()
	elapsed := time.Since(start)

	done, ok := msg.(mcpProbeDoneMsg)
	if !ok {
		t.Fatalf("expected a probe result, got %T", msg)
	}
	if done.res.OK {
		t.Fatal("a cancelled probe must not report a measurement")
	}
	// mcpprobe.DefaultTimeout is 10s; an uncancellable probe of a hanging server
	// takes all of it. The bound is generous enough to survive a loaded CI box
	// and still far below the un-threaded case.
	if elapsed > 5*time.Second {
		t.Fatalf("a cancelled probe took %s - the context is not threaded through to mcpprobe.Probe", elapsed)
	}
}

// TestQuitKillsAnInFlightProbesProcessGroup is the one that protects the user's
// machine. mcpprobe puts each server in its own process group (so a terminal
// SIGINT never reaches it) and issues the group SIGKILL from a defer inside the
// probe goroutine - which never runs if tea.Quit exits the process first.
//
// Asserting a flag would prove nothing here, so this follows Task 3's approach:
// the fixture records its own pid and its child's, and the test asserts both
// processes are gone after the quit path runs.
func TestQuitKillsAnInFlightProbesProcessGroup(t *testing.T) {
	bin := fakeserverBin(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	cfg := hangingServer(bin, pidfile)
	st, _ := buildProbeState(t, map[string]any{"hang": cfg})

	m := newModel(st)
	m.mcps.rebuild()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	target, ok, reason := mcpprobe.TargetFromConfig("hang", cfg)
	if !ok {
		t.Fatalf("fixture should be probeable: %s", reason)
	}
	cmd := m.mcps.startProbe(target, false)

	// Run the probe the way bubbletea would - on its own goroutine - so it is
	// genuinely in flight while the quit path runs.
	msgs := make(chan tea.Msg, 1)
	go func() { msgs <- cmd() }()

	pids := pidsFromFixture(t, pidfile)
	// Registered up front: if the assertions below fail, these must not be left
	// running on the developer's machine.
	t.Cleanup(func() {
		for _, pid := range pids {
			killPid(pid)
		}
	})
	if processGone(pids[0]) || processGone(pids[1]) {
		t.Fatal("fixture: both the server and its child should be running before the quit path")
	}

	// The quit path. It must WAIT for the teardown, not merely request it: a
	// cancel that merely ASKS leaves tea.Quit free to exit the process while the
	// group SIGKILL is still pending.
	session := m.mcps.probeSessions[0]
	start := time.Now()
	m.killActiveProbes()
	waited := time.Since(start)

	select {
	case <-session.done:
	default:
		t.Fatal("killActiveProbes returned before the probe goroutine finished - " +
			"tea.Quit can then exit the process mid-teardown and orphan the server")
	}

	// reapWindow covers the asynchronous reaping of the reparented grandchild,
	// which Linux does more slowly than macOS. It is not a grace period for the
	// teardown itself: a process that the quit path failed to kill is RUNNING,
	// not a zombie, and stays that way for the remainder of mcpprobe's 10s
	// timeout.
	const reapWindow = 2 * time.Second
	for i, pid := range pids {
		if !waitProcessGone(pid, reapWindow) {
			which := "the server"
			if i == 1 {
				which = "its child"
			}
			t.Fatalf("pid %d (%s) is still alive after the quit path - quitting orphaned an MCP server process", pid, which)
		}
	}
	if waited > probeTeardownWait+2*time.Second {
		t.Fatalf("the quit path blocked for %s - the teardown wait is not bounded", waited)
	}

	// Drain so the goroutine cannot outlive the test.
	select {
	case <-msgs:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled probe never returned")
	}
	_ = im
}

// startSweep stages and confirms a `P` sweep over two servers WITHOUT running
// the returned command, so the sweep is left genuinely mid-flight with its first
// probe outstanding - the state in which the cancel keys are live.
func startSweep(t *testing.T, m *model) tea.Model {
	t.Helper()
	var im tea.Model = m
	im, _ = im.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	im, _ = press(im, "P")
	im, cmd := press(im, "P")
	if cmd == nil {
		t.Fatal("confirming must start the sweep")
	}
	if !m.mcps.sweepActive() {
		t.Fatal("the sweep must be active after confirmation")
	}
	return im
}

// TestFilterDuringSweepKeepsItsKeystrokes: the sweep's cancel keys must not be
// stolen from a sub-mode that owns the keyboard. Typing a literal `q` into the
// filter is ordinary, and before this gate it cancelled the sweep and was
// swallowed - the filter stayed focused having received nothing.
func TestFilterDuringSweepKeepsItsKeystrokes(t *testing.T) {
	st, p := buildProbeState(t, nil)
	st.cj.SetUserMCP("aaa", sentinelServer(filepath.Join(p.Home, "s-aaa")))
	st.cj.SetUserMCP("zzz", sentinelServer(filepath.Join(p.Home, "s-zzz")))
	m := newModel(st)
	m.mcps.rebuild()

	im := startSweep(t, m)
	im, _ = press(im, "/")
	if !m.mcps.filterActive {
		t.Fatal("`/` must open the filter even while a sweep runs")
	}
	im, _ = press(im, "q")

	if got := m.mcps.filter.Value(); got != "q" {
		t.Fatalf("the filter must receive the keystroke, got %q", got)
	}
	if !m.mcps.sweepActive() {
		t.Fatal("typing into the filter must not cancel the sweep")
	}
	if !m.mcps.filterActive {
		t.Fatal("the filter must still be focused")
	}
}

// TestMovePickerDuringSweepOwnsEsc: esc inside the move picker means "close the
// picker". Before this gate it cancelled the sweep instead and left the picker
// open - the opposite of what esc means there.
func TestMovePickerDuringSweepOwnsEsc(t *testing.T) {
	st, p := buildProbeState(t, nil)
	st.cj.SetUserMCP("aaa", sentinelServer(filepath.Join(p.Home, "m-aaa")))
	st.cj.SetUserMCP("zzz", sentinelServer(filepath.Join(p.Home, "m-zzz")))
	m := newModel(st)
	m.mcps.rebuild()

	im := startSweep(t, m)
	im, _ = press(im, "m")
	if !m.mcps.moveActive {
		t.Fatal("`m` must open the move picker even while a sweep runs")
	}
	im, _ = press(im, "esc")

	if m.mcps.moveActive {
		t.Fatal("esc inside the move picker must close it")
	}
	if !m.mcps.sweepActive() {
		t.Fatal("esc inside the move picker must not cancel the sweep")
	}

	// And with no sub-mode open, esc still cancels the sweep.
	im, _ = press(im, "esc")
	if m.mcps.sweepActive() {
		t.Fatal("esc with no sub-mode open must still cancel the sweep")
	}
}
