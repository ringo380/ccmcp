package claudecode

import (
	"errors"
	"testing"
	"time"

	"github.com/ringo380/ccmcp/internal/paths"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in    string
		known bool
		raw   string
		major int
		minor int
		patch int
	}{
		{"2.1.158 (Claude Code)", true, "2.1.158", 2, 1, 158},
		{"2.1.158\n", true, "2.1.158", 2, 1, 158},
		{"  2.1.152 (Claude Code)\n", true, "2.1.152", 2, 1, 152},
		{"v2.0.0", true, "v2.0.0", 2, 0, 0},
		{"", false, "", 0, 0, 0},
		{"dev", false, "", 0, 0, 0},
		{"garbage output here", false, "", 0, 0, 0},
	}
	for _, c := range cases {
		got := ParseVersion(c.in)
		if got.Known() != c.known {
			t.Errorf("ParseVersion(%q).Known()=%v, want %v", c.in, got.Known(), c.known)
			continue
		}
		if !c.known {
			continue
		}
		if got.Raw != c.raw || got.Major != c.major || got.Minor != c.minor || got.Patch != c.patch {
			t.Errorf("ParseVersion(%q)=%+v, want raw=%s %d.%d.%d", c.in, got, c.raw, c.major, c.minor, c.patch)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	v := ParseVersion("2.1.158")
	if !v.AtLeast("2.1.152") {
		t.Error("2.1.158 should be AtLeast 2.1.152")
	}
	if v.AtLeast("2.1.159") {
		t.Error("2.1.158 should NOT be AtLeast 2.1.159")
	}
	// Unknown is never AtLeast anything.
	if (Version{}).AtLeast("0.0.1") {
		t.Error("unknown version must not be AtLeast")
	}
}

// withStubs swaps the probe seams for the duration of a test.
func withStubs(t *testing.T, lookup func() (string, time.Time, error), run func(string) (string, error)) {
	t.Helper()
	origLookup, origRun := lookupClaude, runClaudeVersion
	lookupClaude, runClaudeVersion = lookup, run
	t.Cleanup(func() { lookupClaude, runClaudeVersion = origLookup, origRun })
}

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", tmp)
	p, err := paths.Resolve()
	if err != nil {
		t.Fatalf("paths.Resolve: %v", err)
	}
	return p
}

func TestDetectParsesAndCaches(t *testing.T) {
	p := testPaths(t)
	mt := time.Now().Add(-time.Hour).Truncate(time.Second)
	runs := 0
	withStubs(t,
		func() (string, time.Time, error) { return "/bin/claude", mt, nil },
		func(string) (string, error) { runs++; return "2.1.158 (Claude Code)\n", nil },
	)

	v := Detect(p)
	if !v.Known() || v.Raw != "2.1.158" {
		t.Fatalf("Detect=%+v, want 2.1.158", v)
	}
	if runs != 1 {
		t.Fatalf("expected 1 probe, got %d", runs)
	}

	// Same binary identity → cache hit, no re-probe even if the spawn would now
	// return a different version.
	withStubs(t,
		func() (string, time.Time, error) { return "/bin/claude", mt, nil },
		func(string) (string, error) { runs++; return "9.9.9 (Claude Code)\n", nil },
	)
	v2 := Detect(p)
	if v2.Raw != "2.1.158" {
		t.Fatalf("cache hit expected 2.1.158, got %s", v2.Raw)
	}
	if runs != 1 {
		t.Fatalf("cache should have prevented a second probe, runs=%d", runs)
	}
}

func TestDetectMtimeInvalidation(t *testing.T) {
	p := testPaths(t)
	mt1 := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	runs := 0
	withStubs(t,
		func() (string, time.Time, error) { return "/bin/claude", mt1, nil },
		func(string) (string, error) { runs++; return "2.1.158 (Claude Code)\n", nil },
	)
	if v := Detect(p); v.Raw != "2.1.158" {
		t.Fatalf("first Detect=%s", v.Raw)
	}

	// Binary mtime changes (simulating an upgrade) → re-probe and pick up the
	// new version.
	mt2 := mt1.Add(time.Hour)
	withStubs(t,
		func() (string, time.Time, error) { return "/bin/claude", mt2, nil },
		func(string) (string, error) { runs++; return "2.1.159 (Claude Code)\n", nil },
	)
	if v := Detect(p); v.Raw != "2.1.159" {
		t.Fatalf("after mtime change Detect=%s, want 2.1.159", v.Raw)
	}
	if runs != 2 {
		t.Fatalf("expected 2 probes across the upgrade, got %d", runs)
	}
}

func TestDetectMissingBinary(t *testing.T) {
	p := testPaths(t)
	withStubs(t,
		func() (string, time.Time, error) { return "", time.Time{}, errors.New("not found in PATH") },
		func(string) (string, error) { t.Fatal("should not spawn when binary missing"); return "", nil },
	)
	if v := Detect(p); v.Known() {
		t.Fatalf("missing claude should yield unknown, got %+v", v)
	}
}

// TestDetectTransientFailureReprobes guards the fix for a stale-cache bug: a
// transient `claude --version` failure must NOT be pinned as authoritative
// "unknown" until the binary mtime changes. Once the spawn recovers (and the
// failure marker is past SoftTTL), Detect must re-probe and pick up the real
// version even though the binary's path+mtime are unchanged.
func TestDetectTransientFailureReprobes(t *testing.T) {
	p := testPaths(t)
	mt := time.Now().Add(-time.Hour).Truncate(time.Second)
	runs := 0

	// First probe fails transiently (e.g. ctx timeout / fork EAGAIN).
	withStubs(t,
		func() (string, time.Time, error) { return "/bin/claude", mt, nil },
		func(string) (string, error) { runs++; return "", errors.New("transient spawn failure") },
	)
	if v := Detect(p); v.Known() {
		t.Fatalf("failed probe should yield unknown, got %+v", v)
	}
	if runs != 1 {
		t.Fatalf("expected 1 probe, got %d", runs)
	}

	// Age the cached failure marker past SoftTTL so the backstop lapses while the
	// binary identity (path+mtime) stays identical.
	stale, ok := loadInfo(CachePath(p))
	if !ok {
		t.Fatal("expected a cached failure marker")
	}
	stale.CheckedAt = time.Now().Add(-2 * SoftTTL)
	saveInfo(CachePath(p), stale)

	// Spawn now recovers; same binary identity must NOT short-circuit to the
	// pinned unknown — Detect must re-probe and surface the real version.
	withStubs(t,
		func() (string, time.Time, error) { return "/bin/claude", mt, nil },
		func(string) (string, error) { runs++; return "2.1.158 (Claude Code)\n", nil },
	)
	if v := Detect(p); !v.Known() || v.Raw != "2.1.158" {
		t.Fatalf("recovered probe Detect=%+v, want 2.1.158", v)
	}
	if runs != 2 {
		t.Fatalf("expected a re-probe after the failure marker lapsed, runs=%d", runs)
	}
}

func TestDetectUnparseableOutput(t *testing.T) {
	p := testPaths(t)
	withStubs(t,
		func() (string, time.Time, error) { return "/bin/claude", time.Now(), nil },
		func(string) (string, error) { return "totally not a version", nil },
	)
	if v := Detect(p); v.Known() {
		t.Fatalf("garbage --version output should yield unknown, got %+v", v)
	}
}
