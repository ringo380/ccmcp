package mcpprobe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeserverPath is the built fixture binary, shared by every test in this
// package. Built once in TestMain so the per-test cost is a spawn, not a
// compile.
var fakeserverPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mcpprobe-fixture")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdtemp: %v\n", err)
		os.Exit(1)
	}
	fakeserverPath = filepath.Join(dir, "fakeserver")
	if runtime.GOOS == "windows" {
		// CreateProcess needs the extension; `go build -o` adds nothing.
		fakeserverPath += ".exe"
	}

	build := exec.Command("go", "build", "-o", fakeserverPath, "./testdata/fakeserver")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building fakeserver fixture: %v\n", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeTarget builds a Target pointing at the fixture in the given mode.
func fakeTarget(mode string, env ...string) Target {
	e := map[string]string{"FAKE_MODE": mode}
	for i := 0; i+1 < len(env); i += 2 {
		e[env[i]] = env[i+1]
	}
	return Target{Name: "fake-" + mode, Command: fakeserverPath, Env: e}
}

// shortCtx keeps the timeout paths hermetic and fast - tests must never sit
// out the 10s production default.
func shortCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestProbeCountsToolsAndInstructions(t *testing.T) {
	res := Probe(shortCtx(t, 10*time.Second), fakeTarget("ok"))

	if !res.OK {
		t.Fatalf("Probe failed: %q", res.Err)
	}
	if res.Err != "" {
		t.Fatalf("OK probe carries an error: %q", res.Err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("got %d tools, want 2: %+v", len(res.Tools), res.Tools)
	}
	if res.Tools[0].Name != "align_widget" || res.Tools[1].Name != "burnish_widget" {
		t.Fatalf("unexpected tool names: %+v", res.Tools)
	}
	for _, tl := range res.Tools {
		if tl.SchemaTokens <= 0 {
			t.Fatalf("tool %q measured %d tokens - a zero here means unmeasured, not free", tl.Name, tl.SchemaTokens)
		}
	}
	if res.InstructionTokens <= 0 {
		t.Fatalf("InstructionTokens = %d, want > 0", res.InstructionTokens)
	}
	if res.Key != fakeTarget("ok").Key() {
		t.Fatalf("Key = %q, want the target's key", res.Key)
	}
	if res.Name != "fake-ok" {
		t.Fatalf("Name = %q", res.Name)
	}
	if res.ProbedAt.IsZero() {
		t.Fatal("ProbedAt is zero")
	}
}

func TestProbeDeferredIsMuchSmallerThanLoaded(t *testing.T) {
	res := Probe(shortCtx(t, 10*time.Second), fakeTarget("ok"))
	if !res.OK {
		t.Fatalf("Probe failed: %q", res.Err)
	}

	loaded, deferred := res.Loaded(), res.Deferred()
	if loaded <= 0 || deferred <= 0 {
		t.Fatalf("Loaded=%d Deferred=%d, both must be positive", loaded, deferred)
	}
	if deferred >= loaded {
		t.Fatalf("Deferred=%d must be smaller than Loaded=%d - collapsing schemas to a name list is the whole point", deferred, loaded)
	}
	if deferred*2 > loaded {
		t.Fatalf("Deferred=%d is not much smaller than Loaded=%d", deferred, loaded)
	}

	// Loaded is instructions plus every schema; nothing silently dropped.
	sum := res.InstructionTokens
	for _, tl := range res.Tools {
		sum += tl.SchemaTokens
	}
	if loaded != sum {
		t.Fatalf("Loaded=%d, want instructions+schemas=%d", loaded, sum)
	}
	if deferred <= res.InstructionTokens {
		t.Fatalf("Deferred=%d must exceed the %d instruction tokens by the name list", deferred, res.InstructionTokens)
	}
}

// TestProbeReadsToolSchemasPastTheDefaultLineLimit pins the explicit scanner
// buffer. Real tool schemas routinely exceed bufio's 64KB default, and the
// default's failure mode is silent truncation - a measurement that looks fine
// and is wrong.
func TestProbeReadsToolSchemasPastTheDefaultLineLimit(t *testing.T) {
	res := Probe(shortCtx(t, 10*time.Second), fakeTarget("big"))
	if !res.OK {
		t.Fatalf("Probe failed on an oversized frame: %q", res.Err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "huge_tool" {
		t.Fatalf("unexpected tools: %+v", res.Tools)
	}
	if res.Tools[0].SchemaTokens < 20000 {
		t.Fatalf("SchemaTokens = %d, far below the size of the schema sent - the frame was truncated", res.Tools[0].SchemaTokens)
	}
}

func TestProbeTimesOutAndReportsIt(t *testing.T) {
	const budget = 700 * time.Millisecond

	start := time.Now()
	res := Probe(shortCtx(t, budget), fakeTarget("hang"))
	elapsed := time.Since(start)

	if res.OK {
		t.Fatal("a server that never replies must not report OK")
	}
	if !strings.Contains(res.Err, "timed out") {
		t.Fatalf("Err = %q, want it to mention the timeout", res.Err)
	}
	if elapsed > 2*budget {
		t.Fatalf("Probe took %s, want under %s", elapsed, 2*budget)
	}
	if res.Key == "" || res.Name == "" || res.ProbedAt.IsZero() {
		t.Fatalf("a failed probe is still a cacheable fact and must be identified: %+v", res)
	}
}

func TestProbeSurvivesCrash(t *testing.T) {
	res := Probe(shortCtx(t, 5*time.Second), fakeTarget("crash"))
	if res.OK {
		t.Fatal("a server that exits immediately must not report OK")
	}
	if res.Err == "" {
		t.Fatal("failed probe must carry a reason")
	}
	if len(res.Tools) != 0 || res.InstructionTokens != 0 {
		t.Fatalf("failed probe must not report costs: %+v", res)
	}
}

func TestProbeSurvivesGarbage(t *testing.T) {
	res := Probe(shortCtx(t, 700*time.Millisecond), fakeTarget("garbage"))
	if res.OK {
		t.Fatal("a server emitting non-JSON must not report OK")
	}
	if !strings.Contains(res.Err, "non-JSON") {
		t.Fatalf("Err = %q, want it to name the non-JSON output", res.Err)
	}
}

func TestProbeReportsInitializeFailure(t *testing.T) {
	res := Probe(shortCtx(t, 5*time.Second), fakeTarget("noinit"))
	if res.OK {
		t.Fatal("a server that rejects initialize must not report OK")
	}
	if !strings.Contains(res.Err, "initialize") {
		t.Fatalf("Err = %q, want it to name the initialize step", res.Err)
	}
	if len(res.Tools) != 0 {
		t.Fatalf("must not report tools when initialize failed: %+v", res.Tools)
	}
}

// TestProbeLeavesNoProcessBehind is the one that matters most. The fixture
// spawns a grandchild that outlives its parent - exactly what npx-based MCP
// servers do - so killing only the direct child leaves a live process behind.
func TestProbeLeavesNoProcessBehind(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	target := fakeTarget("hangchild", "FAKE_PIDFILE", pidfile)

	res := Probe(shortCtx(t, 700*time.Millisecond), target)
	if res.OK {
		t.Fatalf("hangchild must not report OK: %+v", res)
	}

	pids := readPids(t, pidfile)
	if len(pids) != 2 {
		t.Fatalf("fixture recorded %d pids, want the server and its child: %v", len(pids), pids)
	}

	for _, pid := range pids {
		if alive := waitForExit(pid, 3*time.Second); alive {
			t.Fatalf("pid %d is still alive after Probe returned - the probe leaked a process", pid)
		}
	}
}

// TestProbeReturnsWhenADescendantEscapesTheProcessGroup pins the WaitDelay.
// cmd.Stderr is io.Discard rather than an *os.File, so os/exec builds a pipe
// for it, and cmd.Wait blocks until every holder of that pipe's write end
// closes it. The escapee fixture spawns a setsid child that inherits stderr:
// the group SIGKILL cannot reach it, so without a WaitDelay bounding Wait the
// probe blocks forever and the timeout it is built around means nothing.
func TestProbeReturnsWhenADescendantEscapesTheProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a kill-on-close Job Object has no escape, so nothing can hold stderr past the kill; the WaitDelay bound is exercised by TestProbeLeavesNoProcessBehind")
	}
	pidfile := filepath.Join(t.TempDir(), "pids")
	target := fakeTarget("escapee", "FAKE_PIDFILE", pidfile)

	// The escapee outlives the probe by design and nothing else will reap it,
	// so registered up front - it must be cleaned up even if this test fails
	// at the bound below.
	t.Cleanup(func() {
		for _, pid := range pidsBestEffort(pidfile) {
			killPid(pid)
		}
	})

	// The probe's own budget; Wait then costs at most one WaitDelay on top.
	ctx := shortCtx(t, 700*time.Millisecond)
	// Comfortably above 700ms + the 2s WaitDelay, and far below any plausible
	// test timeout - so a missing WaitDelay fails here instead of hanging.
	const bound = 6 * time.Second

	done := make(chan Result, 1)
	start := time.Now()
	go func() { done <- Probe(ctx, target) }()

	var res Result
	select {
	case res = <-done:
	case <-time.After(bound):
		t.Fatalf("Probe did not return within %s - cmd.Wait is blocked on a stderr pipe held by an escaped descendant", bound)
	}
	elapsed := time.Since(start)

	if res.OK {
		t.Fatalf("a server that never replies must not report OK: %+v", res)
	}
	if !strings.Contains(res.Err, "timed out") {
		t.Fatalf("Err = %q, want it to mention the timeout", res.Err)
	}

	pids := pidsBestEffort(pidfile)
	if len(pids) != 2 {
		t.Fatalf("fixture recorded %d pids, want the server and its escapee: %v", len(pids), pids)
	}
	if waitForExit(pids[0], 3*time.Second) {
		t.Fatalf("pid %d (the direct child) is still alive - the group kill did not land", pids[0])
	}
	// If this ever fails the fixture stopped escaping, and the test above is
	// no longer exercising the blocked-Wait path it claims to.
	if !processAlive(pids[1]) {
		t.Fatalf("pid %d (the escapee) died with the group - the fixture is not escaping, so this test proves nothing", pids[1])
	}

	t.Logf("Probe returned in %s with a live escaped descendant still holding stderr", elapsed)
}

// TestProbeSucceedsDespiteANonJSONBanner covers the tolerated-junk path: a
// server that prints a banner and THEN speaks protocol is still measurable.
// Without a fixture that does both, sawJunk is only ever observed on failures.
func TestProbeSucceedsDespiteANonJSONBanner(t *testing.T) {
	res := Probe(shortCtx(t, 10*time.Second), fakeTarget("banner"))
	if !res.OK {
		t.Fatalf("a banner line before valid frames must not fail the probe: %q", res.Err)
	}
	if res.Err != "" {
		t.Fatalf("OK probe carries an error: %q", res.Err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("got %d tools, want 2: %+v", len(res.Tools), res.Tools)
	}
	for _, tl := range res.Tools {
		if tl.SchemaTokens <= 0 {
			t.Fatalf("tool %q measured %d tokens", tl.Name, tl.SchemaTokens)
		}
	}
	if res.InstructionTokens <= 0 {
		t.Fatalf("InstructionTokens = %d, want > 0", res.InstructionTokens)
	}
	if res.NameTokens <= 0 {
		t.Fatalf("NameTokens = %d, want > 0", res.NameTokens)
	}
}

// TestProbeRejectsUnreadableInitializeResult pins that a non-object result
// fails the probe. Skipping it silently would report zero instruction tokens
// on an OK result - unmeasured dressed up as measured.
func TestProbeRejectsUnreadableInitializeResult(t *testing.T) {
	res := Probe(shortCtx(t, 5*time.Second), fakeTarget("badinit"))
	if res.OK {
		t.Fatalf("an unreadable initialize result must not report OK: %+v", res)
	}
	if !strings.Contains(res.Err, "initialize") {
		t.Fatalf("Err = %q, want it to name the initialize step", res.Err)
	}
	if res.InstructionTokens != 0 || len(res.Tools) != 0 {
		t.Fatalf("failed probe must not report costs: %+v", res)
	}
}

// pidsBestEffort reads whatever pids the fixture has recorded so far, without
// waiting or failing - for cleanup paths and post-hoc assertions.
func pidsBestEffort(path string) []int {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		if n, convErr := strconv.Atoi(f); convErr == nil {
			pids = append(pids, n)
		}
	}
	return pids
}

// readPids waits briefly for the fixture to record its pids, then parses them.
func readPids(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			var pids []int
			for _, line := range strings.Fields(string(b)) {
				n, convErr := strconv.Atoi(line)
				if convErr != nil {
					t.Fatalf("bad pid %q in %s", line, path)
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

// waitForExit reports whether pid is still alive after waiting up to d for it
// to disappear. A SIGKILLed process can linger for a few milliseconds as a
// zombie before its parent (or init) reaps it, so this polls rather than
// sampling once - but the window is far shorter than the 60s the leaked
// sleeper would otherwise survive for.
func waitForExit(pid int, d time.Duration) (alive bool) {
	deadline := time.Now().Add(d)
	for {
		if !processAlive(pid) {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestProbeHonorsACallerDeadlineLongerThanTheDefault pins that DefaultTimeout is
// a FALLBACK, not a cap. `ccmcp mcp probe --timeout 30s` exists to give a slow
// server longer than the default, and layering DefaultTimeout on top of the
// caller's context unconditionally would silently clamp it back to 10s - a flag
// that quietly does not do what it says.
//
// DefaultTimeout is shrunk here so the "no caller deadline" half of the
// assertion is fast and hermetic rather than a real 10s wait.
func TestProbeHonorsACallerDeadlineLongerThanTheDefault(t *testing.T) {
	orig := DefaultTimeout
	DefaultTimeout = 50 * time.Millisecond
	t.Cleanup(func() { DefaultTimeout = orig })

	target := fakeTarget("slowinit")

	// No caller deadline: the (shrunken) default must bite.
	if res := Probe(context.Background(), target); res.OK {
		t.Fatalf("expected the default timeout to bite with no caller deadline, got a measured result: %+v", res)
	}

	// A caller deadline well past the default must win, and the same server
	// must then measure cleanly.
	res := Probe(shortCtx(t, 10*time.Second), target)
	if !res.OK {
		t.Fatalf("a caller deadline longer than DefaultTimeout must be honored, not clamped: %q", res.Err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(res.Tools))
	}
}
