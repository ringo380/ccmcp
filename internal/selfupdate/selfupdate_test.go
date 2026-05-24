package selfupdate

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ringo380/ccmcp/internal/paths"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.7.0", "0.6.0", 1},
		{"v0.7.0", "0.6.0", 1},
		{"0.6.0", "0.7.0", -1},
		{"0.7.0", "0.7.0", 0},
		{"v0.7.0", "v0.7.0", 0},
		{"1.0.0", "0.9.99", 1},
		{"0.7.1", "0.7.0", 1},
		// pre-release suffix is stripped — equivalent to base
		{"0.7.0-rc1", "0.7.0", 0},
		// missing patch defaults to 0
		{"0.7", "0.7.0", 0},
	}
	for _, c := range cases {
		if got := CompareSemver(c.a, c.b); got != c.want {
			t.Errorf("CompareSemver(%q, %q) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestStatusHasUpdate(t *testing.T) {
	s := Status{LatestVersion: "0.8.0"}
	if !s.HasUpdate("0.7.0") {
		t.Error("0.8.0 > 0.7.0 should be an update")
	}
	if s.HasUpdate("0.8.0") {
		t.Error("0.8.0 == 0.8.0 should NOT be an update")
	}
	if s.HasUpdate("0.9.0") {
		t.Error("0.8.0 < 0.9.0 should NOT be an update")
	}
}

func TestStatusIsFresh(t *testing.T) {
	now := time.Now().UTC()
	if !(Status{CheckedAt: now}).IsFresh(time.Hour) {
		t.Error("just-checked status should be fresh within 1h")
	}
	if (Status{CheckedAt: now.Add(-2 * time.Hour)}).IsFresh(time.Hour) {
		t.Error("2h-old status should NOT be fresh within 1h")
	}
	if (Status{}).IsFresh(time.Hour) {
		t.Error("zero-CheckedAt should never be fresh")
	}
}

func TestChooseRefresh(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name string
		age  time.Duration
		zero bool
		want refreshMode
	}{
		{"no cache", 0, true, refreshSync},
		{"just checked", 5 * time.Minute, false, refreshNone},
		{"under soft ttl", SoftTTL - time.Minute, false, refreshNone},
		{"at soft ttl", SoftTTL, false, refreshAsync},
		{"between soft and fresh", 6 * time.Hour, false, refreshAsync},
		{"just under fresh ttl", FreshTTL - time.Minute, false, refreshAsync},
		{"at fresh ttl", FreshTTL, false, refreshSync},
		{"well past fresh ttl", 48 * time.Hour, false, refreshSync},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Status{CheckedAt: now.Add(-tc.age)}
			if tc.zero {
				s = Status{}
			}
			if got := chooseRefresh(s, now); got != tc.want {
				t.Errorf("chooseRefresh(age=%s) = %d, want %d", tc.age, got, tc.want)
			}
		})
	}
}

func TestStatusIsDismissed(t *testing.T) {
	now := time.Now().UTC()
	s := Status{LatestVersion: "0.8.0", DismissedVersion: "0.8.0", DismissedAt: now}
	if !s.IsDismissed() {
		t.Error("matching version + recent dismissal should be dismissed")
	}
	// Newer version re-arms
	s.LatestVersion = "0.9.0"
	if s.IsDismissed() {
		t.Error("LatestVersion bumped past DismissedVersion must re-arm prompt")
	}
	// Old dismissal expires
	s = Status{LatestVersion: "0.8.0", DismissedVersion: "0.8.0", DismissedAt: now.Add(-48 * time.Hour)}
	if s.IsDismissed() {
		t.Error("dismissal older than DismissTTL should expire")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	in := Status{
		CheckedAt:        time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC),
		CurrentVersion:   "0.7.0",
		LatestVersion:    "0.8.0",
		LatestURL:        "https://example.com/release",
		Body:             "- new thing\n- another thing",
		DismissedAt:      time.Date(2026, 5, 11, 1, 0, 0, 0, time.UTC),
		DismissedVersion: "0.8.0",
	}
	if err := SaveCache(path, in); err != nil {
		t.Fatal(err)
	}
	out := LoadCache(path)
	if out.LatestVersion != in.LatestVersion || out.DismissedVersion != in.DismissedVersion {
		t.Errorf("round-trip mismatch:\nwant %+v\n got %+v", in, out)
	}
}

func TestLoadCacheMissingFile(t *testing.T) {
	s := LoadCache(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if s.LatestVersion != "" {
		t.Errorf("missing file should return zero Status, got %+v", s)
	}
}

func TestUpgradeCommand(t *testing.T) {
	if got := UpgradeCommand(MethodBrew); len(got) == 0 || got[0] != "brew" {
		t.Errorf("brew upgrade should start with 'brew'; got %v", got)
	}
	if got := UpgradeCommand(MethodGo); len(got) == 0 || got[0] != "go" {
		t.Errorf("go upgrade should start with 'go'; got %v", got)
	}
	if got := UpgradeCommand(MethodBinary); len(got) != 0 {
		t.Errorf("binary install has no automatic upgrade; got %v", got)
	}
}

func TestMethodHintIncludesURL(t *testing.T) {
	got := MethodHint(MethodBinary, "https://example.com/v1")
	if !strings.Contains(got, "https://example.com/v1") {
		t.Errorf("binary hint should include release URL; got %q", got)
	}
}

func TestSkipCheckDevBuild(t *testing.T) {
	t.Setenv("CCMCP_NO_UPDATE_CHECK", "")
	if !SkipCheck("dev") {
		t.Error("dev build must skip update check")
	}
	if !SkipCheck("") {
		t.Error("empty version must skip update check")
	}
}

func TestSkipCheckEnvVar(t *testing.T) {
	t.Setenv("CCMCP_NO_UPDATE_CHECK", "1")
	// Note: SkipCheck also returns true when stdin/stdout aren't TTYs (always
	// the case under `go test`), so this assertion mainly exercises the env-var
	// branch — it'd skip anyway.
	if !SkipCheck("0.7.0") {
		t.Error("CCMCP_NO_UPDATE_CHECK=1 must skip update check")
	}
}

func TestPromptYesRunsCommand(t *testing.T) {
	// Stub `true` (always-succeeds shell builtin) as the upgrade command so we
	// don't actually run brew/go on the dev machine.
	tmp := t.TempDir()
	p := paths.Paths{PluginsDir: tmp}

	in := bytes.NewBufferString("Y\n")
	var out bytes.Buffer
	d := prompt(p, "0.7.0", Status{LatestVersion: "0.8.0"}, MethodBrew, []string{"true"}, in, &out)
	if !d.Updated || !d.ExitAfter {
		t.Errorf("Y answer with successful command must set Updated+ExitAfter; got %+v", d)
	}
	gotOut := out.String()
	if !strings.Contains(gotOut, "Update now? [Y/n]") {
		t.Errorf("prompt should show Y/n question; got:\n%s", gotOut)
	}
	if !strings.Contains(gotOut, "Running: true") {
		t.Errorf("prompt should run the upgrade command; got:\n%s", gotOut)
	}
	if !strings.Contains(gotOut, "Upgrade complete") {
		t.Errorf("prompt should confirm success; got:\n%s", gotOut)
	}
}

func TestPromptDeclineDoesNotRun(t *testing.T) {
	tmp := t.TempDir()
	p := paths.Paths{PluginsDir: tmp}

	in := bytes.NewBufferString("n\n")
	var out bytes.Buffer
	d := prompt(p, "0.7.0", Status{LatestVersion: "0.8.0"}, MethodBrew, []string{"true"}, in, &out)
	if d.Updated || d.ExitAfter {
		t.Errorf("declining must not set Updated/ExitAfter; got %+v", d)
	}
	gotOut := out.String()
	if !strings.Contains(gotOut, "Skipped") {
		t.Errorf("expected 'Skipped.' confirmation; got:\n%s", gotOut)
	}
	if strings.Contains(gotOut, "Running:") {
		t.Errorf("declined prompt must not run an upgrade command; got:\n%s", gotOut)
	}
	// Decline should persist a dismissal in the cache.
	cached := LoadCache(CachePath(p))
	if cached.DismissedVersion != "0.8.0" {
		t.Errorf("dismissal should be persisted for 0.8.0; got %+v", cached)
	}
}

func TestPromptEmptyInputDefaultsToYes(t *testing.T) {
	// Just Enter on the prompt should be treated as "Y" (the [Y/n] capital
	// indicates default). We can't actually run the command here, but we can
	// assert the prompt didn't go down the "n" branch.
	tmp := t.TempDir()
	p := paths.Paths{PluginsDir: tmp}

	in := bytes.NewBufferString("\n")
	var out bytes.Buffer
	_ = prompt(p, "0.7.0", Status{LatestVersion: "0.8.0"}, MethodBrew, []string{"true"}, in, &out)
	gotOut := out.String()
	if strings.Contains(gotOut, "Skipped") {
		t.Errorf("empty input should default to Y, not Skipped; got:\n%s", gotOut)
	}
}

func TestTruncateBodyAtLimit(t *testing.T) {
	body := "line1\nline2\nline3\nline4\nline5\nline6"
	got := truncateBody(body, 3)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 { // 3 content + " …" marker
		t.Errorf("truncate to 3 lines should yield 4 (incl. ellipsis); got %d:\n%s", len(lines), got)
	}
	if !strings.Contains(got, "…") {
		t.Error("truncation marker should be present when body exceeds limit")
	}
}

func TestTruncateBodySkipsComments(t *testing.T) {
	body := "<!-- internal -->\nreal content\n<!-- another -->\nmore content"
	got := truncateBody(body, 10)
	if strings.Contains(got, "internal") || strings.Contains(got, "another") {
		t.Errorf("HTML comments should be stripped from truncated body; got:\n%s", got)
	}
}
