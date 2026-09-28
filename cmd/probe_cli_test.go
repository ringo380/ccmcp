package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ringo380/ccmcp/internal/mcpprobe"
)

// fakeserver is the stdio MCP fixture that internal/mcpprobe's own tests use.
// It is built once per test binary, on first use rather than in a TestMain, so
// the cmd package's many tests that never probe anything pay nothing for it.
var (
	fakeserverOnce sync.Once
	fakeserverPath string
	fakeserverErr  error
)

func fakeserver(t *testing.T) string {
	t.Helper()
	fakeserverOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ccmcp-fakeserver")
		if err != nil {
			fakeserverErr = err
			return
		}
		fakeserverPath = filepath.Join(dir, "fakeserver")
		if runtime.GOOS == "windows" {
			fakeserverPath += ".exe" // CreateProcess needs the extension
		}
		build := exec.Command("go", "build", "-o", fakeserverPath, "../internal/mcpprobe/testdata/fakeserver")
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

// writeUserMCPs replaces the sandbox's user-scope mcpServers wholesale, so a
// test controls exactly which servers are effective in the project.
func writeUserMCPs(t *testing.T, home string, servers map[string]any) {
	t.Helper()
	path := filepath.Join(home, ".claude.json")
	var doc map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	doc["mcpServers"] = servers
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadProbeCache(t *testing.T, home string) *mcpprobe.Cache {
	t.Helper()
	c, err := mcpprobe.LoadCache(filepath.Join(home, ".claude-mcp-probe-cache.json"))
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	return c
}

// TestCLIProbeWritesCache pins the whole point of the command: after `ccmcp mcp
// probe`, the on-disk cache carries a measured result the context estimator can
// read back without probing again.
func TestCLIProbeWritesCache(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	cfg := map[string]any{
		"command": fakeserver(t),
		"env":     map[string]any{"FAKE_MODE": "ok"},
	}
	writeUserMCPs(t, home, map[string]any{"fake": cfg})

	out, err := runCLI(t, home, "--path", proj, "mcp", "probe")
	if err != nil {
		t.Fatalf("mcp probe: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fake") {
		t.Fatalf("probe output should name the server:\n%s", out)
	}
	if !strings.Contains(out, "≈") {
		t.Fatalf("probe figures must be presented as estimates:\n%s", out)
	}

	target, ok, reason := mcpprobe.TargetFromConfig("fake", cfg)
	if !ok {
		t.Fatalf("fixture should be probeable: %s", reason)
	}
	res, hit := loadProbeCache(t, home).Get(target.Key())
	if !hit {
		t.Fatalf("probe wrote no cache entry for %s:\n%s", target.Key(), out)
	}
	if !res.OK {
		t.Fatalf("cached result should be a success, got err %q", res.Err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("expected the fixture's 2 tools, got %d", len(res.Tools))
	}
	if res.Loaded() <= res.Deferred() {
		t.Fatalf("loaded (%d) should exceed deferred (%d) for a server with real schemas",
			res.Loaded(), res.Deferred())
	}
}

// TestCLIProbeReportsFailureWithoutFailingTheCommand: a server that cannot be
// spoken to is data, not a command failure - and the failure is cached, because
// a cached failure is what keeps a broken server from re-costing the user on
// every later run.
func TestCLIProbeReportsFailureWithoutFailingTheCommand(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	cfg := map[string]any{"command": filepath.Join(proj, "definitely-not-a-real-binary")}
	writeUserMCPs(t, home, map[string]any{"broken": cfg})

	out, err := runCLI(t, home, "--path", proj, "mcp", "probe")
	if err != nil {
		t.Fatalf("a failed probe must not fail the command: %v\n%s", err, out)
	}
	if !strings.Contains(out, "broken") {
		t.Fatalf("output should name the failing server:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "failed") {
		t.Fatalf("output should say the probe failed:\n%s", out)
	}

	target, ok, _ := mcpprobe.TargetFromConfig("broken", cfg)
	if !ok {
		t.Fatal("a bad command path is still a probeable target")
	}
	res, hit := loadProbeCache(t, home).Get(target.Key())
	if !hit {
		t.Fatalf("a failed probe must still be cached:\n%s", out)
	}
	if res.OK {
		t.Fatal("cached result should record the failure")
	}
	if res.Err == "" {
		t.Fatal("cached failure should carry a reason")
	}
}

var assetItemsRE = regexp.MustCompile(`from (\d+) enabled skills/agents/commands`)

func assetItems(t *testing.T, out string) int {
	t.Helper()
	m := assetItemsRE.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("missing the asset-count sentence:\n%s", out)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCLIContextIncludesProbedMCPCost: a cached probe has to show up in `ccmcp
// context` - both in the total and as its own line - and it must NOT inflate the
// "from N enabled skills/agents/commands" count. ctxcost.Breakdown.Items counts
// measured assets AND measured MCP servers, so a naive label reads N+1 the
// moment a server is probed.
func TestCLIContextIncludesProbedMCPCost(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	cfg := map[string]any{"command": "some-server", "args": []any{"--stdio"}}
	writeUserMCPs(t, home, map[string]any{"measured": cfg})

	before, err := runCLI(t, home, "--path", proj, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, before)
	}
	beforeItems := assetItems(t, before)

	target, ok, reason := mcpprobe.TargetFromConfig("measured", cfg)
	if !ok {
		t.Fatalf("fixture should be probeable: %s", reason)
	}
	cache := loadProbeCache(t, home)
	cache.Put(mcpprobe.Result{
		Key:               target.Key(),
		Name:              "measured",
		ProbedAt:          time.Now(),
		OK:                true,
		InstructionTokens: 1200,
		NameTokens:        40,
		Tools:             []mcpprobe.Tool{{Name: "alpha", SchemaTokens: 5000}},
	})
	if err := cache.Save(); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	after, err := runCLI(t, home, "--path", proj, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, after)
	}
	if got := assetItems(t, after); got != beforeItems {
		t.Fatalf("probing a server must not change the asset item count (%d -> %d):\n%s",
			beforeItems, got, after)
	}
	if !strings.Contains(after, "≈6.2k") {
		t.Fatalf("expected the probed server's loaded cost (1200+5000) in the output:\n%s", after)
	}
	if !strings.Contains(after, "1 probed server") {
		t.Fatalf("expected the probed-server count on the MCP line:\n%s", after)
	}

	jsonOut, err := runCLI(t, home, "--path", proj, "context", "--json")
	if err != nil {
		t.Fatalf("context --json: %v\n%s", err, jsonOut)
	}
	var payload struct {
		Project struct {
			MCP struct {
				Loaded   int `json:"loaded"`
				Deferred int `json:"deferred"`
			} `json:"mcp"`
			Unmeasured int `json:"unmeasured"`
		} `json:"project"`
		ByMCP map[string]struct {
			MCP struct {
				Loaded int `json:"loaded"`
			} `json:"mcp"`
		} `json:"byMcp"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, jsonOut)
	}
	if payload.Project.MCP.Loaded != 6200 {
		t.Fatalf("project mcp loaded = %d, want 6200:\n%s", payload.Project.MCP.Loaded, jsonOut)
	}
	if payload.Project.MCP.Deferred != 1240 {
		t.Fatalf("project mcp deferred = %d, want 1240:\n%s", payload.Project.MCP.Deferred, jsonOut)
	}
	if payload.ByMCP["measured"].MCP.Loaded != 6200 {
		t.Fatalf("per-server attribution missing:\n%s", jsonOut)
	}
}

// TestCLIContextStillShowsDashForUnprobedServers is the completeness contract:
// a configured server with no cache entry must be counted as unmeasured and
// rendered as "-", never as a measured zero and never silently omitted (an
// entry ABSENT from ctxcost.Input.MCP adds nothing to the total AND nothing to
// Unmeasured, which prints a confident number that is quietly incomplete).
func TestCLIContextStillShowsDashForUnprobedServers(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	writeUserMCPs(t, home, map[string]any{
		"unprobed": map[string]any{"command": "some-server"},
	})

	out, err := runCLI(t, home, "--path", proj, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, out)
	}
	var line string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "MCP tool schemas") {
			line = ln
			break
		}
	}
	if line == "" {
		t.Fatalf("an unprobed server must still be accounted for on an MCP line:\n%s", out)
	}
	if !strings.Contains(line, "-") {
		t.Fatalf("an unmeasured MCP cost must render as \"-\": %q", line)
	}
	if strings.Contains(line, "≈0") {
		t.Fatalf("an unmeasured MCP cost must never render as a measured zero: %q", line)
	}
	if !strings.Contains(line, "1 server") || !strings.Contains(line, "unmeasured") {
		t.Fatalf("the MCP line should say how many servers are unmeasured: %q", line)
	}

	jsonOut, err := runCLI(t, home, "--path", proj, "context", "--json")
	if err != nil {
		t.Fatalf("context --json: %v\n%s", err, jsonOut)
	}
	var payload struct {
		Project struct {
			Unmeasured int `json:"unmeasured"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, jsonOut)
	}
	if payload.Project.Unmeasured != 1 {
		t.Fatalf("unmeasured = %d, want 1:\n%s", payload.Project.Unmeasured, jsonOut)
	}
}

// TestCLIContextAccountsForUnprobeableServers extends the completeness contract
// to the servers that can never be probed locally - a claude.ai integration and
// a built-in enabled via enabledMcpServers. Both load in the project, so both
// must be counted as unmeasured rather than vanish from the accounting.
func TestCLIContextAccountsForUnprobeableServers(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	doc := map[string]any{
		"anonymousId":              "sandbox",
		"mcpServers":               map[string]any{},
		"claudeAiMcpEverConnected": []any{"claude.ai Gmail"},
		"projects": map[string]any{
			proj: map[string]any{"enabledMcpServers": []any{"computer-use"}},
		},
	}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "--path", proj, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2 server") || !strings.Contains(out, "unmeasured") {
		t.Fatalf("both unprobeable servers must be counted as unmeasured:\n%s", out)
	}
}

// TestCLIContextReportsDuplicateServerNamesAsUnmeasured: ctxcost.Input.MCP is
// keyed by display name, so two servers that load under the same name cannot
// both be represented. Publishing the first one's measurement would read as a
// COMPLETE figure while a second real server vanished from the total with
// nothing saying so - the same failure mode as omitting a server entirely. Both
// configs are seeded into the probe cache here, so a build that measured either
// of them would be caught.
func TestCLIContextReportsDuplicateServerNamesAsUnmeasured(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	userCfg := map[string]any{"command": "server-a"}
	localCfg := map[string]any{"command": "server-b"}

	doc := map[string]any{
		"anonymousId": "sandbox",
		"mcpServers":  map[string]any{"same": userCfg},
		"projects": map[string]any{
			proj: map[string]any{"mcpServers": map[string]any{"same": localCfg}},
		},
	}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	cache := loadProbeCache(t, home)
	for _, cfg := range []map[string]any{userCfg, localCfg} {
		target, ok, reason := mcpprobe.TargetFromConfig("same", cfg)
		if !ok {
			t.Fatalf("fixture should be probeable: %s", reason)
		}
		cache.Put(mcpprobe.Result{
			Key:               target.Key(),
			Name:              "same",
			ProbedAt:          time.Now(),
			OK:                true,
			InstructionTokens: 900,
			NameTokens:        30,
			Tools:             []mcpprobe.Tool{{Name: "alpha", SchemaTokens: 4100}},
		})
	}
	if err := cache.Save(); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	out, err := runCLI(t, home, "--path", proj, "context", "--json")
	if err != nil {
		t.Fatalf("context --json: %v\n%s", err, out)
	}
	var payload struct {
		Project struct {
			MCP struct {
				Loaded int `json:"loaded"`
			} `json:"mcp"`
			Unmeasured int `json:"unmeasured"`
		} `json:"project"`
		MCP map[string]struct {
			Probed bool   `json:"probed"`
			Reason string `json:"reason"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	got, ok := payload.MCP["same"]
	if !ok {
		t.Fatalf("the colliding name must still be accounted for:\n%s", out)
	}
	if got.Probed {
		t.Fatalf("a colliding name must not be published as a measurement:\n%s", out)
	}
	if !strings.Contains(got.Reason, "duplicate server name (2 sources)") {
		t.Fatalf("reason should name the collision, got %q", got.Reason)
	}
	if payload.Project.MCP.Loaded != 0 {
		t.Fatalf("no cost may be attributed to a colliding name, got %d:\n%s",
			payload.Project.MCP.Loaded, out)
	}
	if payload.Project.Unmeasured != 1 {
		t.Fatalf("unmeasured = %d, want 1:\n%s", payload.Project.Unmeasured, out)
	}
}

// TestCLIProbeRejectsANonPositiveTimeout: `--timeout 0` (or a negative value)
// makes every probe fail with a deadline error that reads as the server's fault.
// It must be rejected up front, before any server is started, rather than
// silently coerced to something else.
func TestCLIProbeRejectsANonPositiveTimeout(t *testing.T) {
	for _, val := range []string{"0", "-1s"} {
		home := setupSandbox(t)
		proj := t.TempDir()
		writeUserMCPs(t, home, map[string]any{
			"fake": map[string]any{"command": fakeserver(t), "env": map[string]any{"FAKE_MODE": "ok"}},
		})

		out, err := runCLI(t, home, "--path", proj, "mcp", "probe", "--timeout", val)
		if err == nil {
			t.Fatalf("--timeout %s must be rejected, got success:\n%s", val, out)
		}
		if !strings.Contains(err.Error(), "--timeout") {
			t.Fatalf("--timeout %s: error should name the flag, got %v", val, err)
		}
		if len(loadProbeCache(t, home).Entries) != 0 {
			t.Fatalf("--timeout %s must be rejected before anything is probed or cached", val)
		}
	}
}

// TestCLIProbeDoesNotCacheAFailureUnderAnExplicitTimeout: a probe run under a
// caller-chosen timeout is an experiment, not a measurement of that server. Its
// failure must not overwrite a good cached result nor manufacture a sticky one,
// because `ccmcp context` would then report the user's own flag choice as a
// server failure and only --force could clear it.
func TestCLIProbeDoesNotCacheAFailureUnderAnExplicitTimeout(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	// FAKE_MODE=hang reads input and answers nothing, so this probe cannot
	// succeed on any machine at any speed. The "ok" fixture made this test
	// timing-dependent: on Linux it occasionally completed inside the 1ms bound,
	// and a SUCCESS is cached by design, so the assertions below failed for a
	// reason that had nothing to do with the rule under test. A server that never
	// replies makes the failure a property of the fixture rather than of the
	// host's scheduler.
	cfg := map[string]any{
		"command": fakeserver(t),
		"env":     map[string]any{"FAKE_MODE": "hang"},
	}
	writeUserMCPs(t, home, map[string]any{"fake": cfg})
	target, ok, reason := mcpprobe.TargetFromConfig("fake", cfg)
	if !ok {
		t.Fatalf("fixture should be probeable: %s", reason)
	}

	// A good measurement already on disk.
	cache := loadProbeCache(t, home)
	cache.Put(mcpprobe.Result{
		Key: target.Key(), Name: "fake", ProbedAt: time.Now(), OK: true,
		InstructionTokens: 700, NameTokens: 20,
		Tools: []mcpprobe.Tool{{Name: "alpha", SchemaTokens: 1300}},
	})
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, home, "--path", proj, "mcp", "probe", "--force", "--timeout", "1ms")
	if err != nil {
		t.Fatalf("a timed-out probe must not fail the command: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not cached") {
		t.Fatalf("output must say the failure was not cached:\n%s", out)
	}
	res, hit := loadProbeCache(t, home).Get(target.Key())
	if !hit {
		t.Fatalf("the existing measurement must survive a --timeout experiment:\n%s", out)
	}
	if !res.OK || res.Loaded() != 2000 {
		t.Fatalf("cached result was overwritten by the timeout failure: %+v", res)
	}

	// With nothing cached beforehand, a timeout under an explicit --timeout must
	// not manufacture a sticky failure either.
	fresh := setupSandbox(t)
	writeUserMCPs(t, fresh, map[string]any{"fake": cfg})
	out, err = runCLI(t, fresh, "--path", proj, "mcp", "probe", "--timeout", "1ms")
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	if n := len(loadProbeCache(t, fresh).Entries); n != 0 {
		t.Fatalf("a failure under an explicit --timeout must not be cached; cache has %d entr(ies)", n)
	}

	// The default timeout still caches failures - that is what keeps a broken
	// server from re-costing the user on every run.
	broken := setupSandbox(t)
	writeUserMCPs(t, broken, map[string]any{
		"broken": map[string]any{"command": filepath.Join(proj, "not-a-binary")},
	})
	if out, err := runCLI(t, broken, "--path", proj, "mcp", "probe"); err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	if n := len(loadProbeCache(t, broken).Entries); n != 1 {
		t.Fatalf("without an explicit --timeout a failure must still be cached; cache has %d entr(ies)", n)
	}
}

// TestCLIContextJSONExposesTheAssetItemCount: the human line subtracts probed
// servers from Breakdown.Items to report assets, so a JSON consumer reading the
// raw project.items reproduces the off-by-N the printed sentence avoids. The
// payload must therefore carry the asset count (and the probed-server count that
// explains the difference) explicitly.
func TestCLIContextJSONExposesTheAssetItemCount(t *testing.T) {
	home := setupSandbox(t)
	proj := t.TempDir()
	cfg := map[string]any{"command": "some-server"}
	writeUserMCPs(t, home, map[string]any{"measured": cfg})

	target, ok, reason := mcpprobe.TargetFromConfig("measured", cfg)
	if !ok {
		t.Fatalf("fixture should be probeable: %s", reason)
	}
	cache := loadProbeCache(t, home)
	cache.Put(mcpprobe.Result{
		Key: target.Key(), Name: "measured", ProbedAt: time.Now(), OK: true,
		InstructionTokens: 300, NameTokens: 10,
		Tools: []mcpprobe.Tool{{Name: "alpha", SchemaTokens: 700}},
	})
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}

	human, err := runCLI(t, home, "--path", proj, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, human)
	}
	printed := assetItems(t, human)

	jsonOut, err := runCLI(t, home, "--path", proj, "context", "--json")
	if err != nil {
		t.Fatalf("context --json: %v\n%s", err, jsonOut)
	}
	var payload struct {
		AssetItems    int `json:"assetItems"`
		ProbedServers int `json:"probedServers"`
		Project       struct {
			Items int `json:"items"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, jsonOut)
	}
	if payload.AssetItems != printed {
		t.Fatalf("assetItems = %d but the printed line says %d:\n%s", payload.AssetItems, printed, jsonOut)
	}
	if payload.ProbedServers != 1 {
		t.Fatalf("probedServers = %d, want 1:\n%s", payload.ProbedServers, jsonOut)
	}
	// The raw field keeps its documented ctxcost meaning: assets plus measured
	// servers. Exposing assetItems is an addition, not a redefinition.
	if payload.Project.Items != payload.AssetItems+payload.ProbedServers {
		t.Fatalf("project.items (%d) should be assetItems (%d) + probedServers (%d):\n%s",
			payload.Project.Items, payload.AssetItems, payload.ProbedServers, jsonOut)
	}
}
