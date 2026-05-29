package install

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/paths"
)

func TestLoadMarketplaceAndFindPlugin(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{PluginsDir: filepath.Join(dir, "plugins")}
	manifestDir := filepath.Join(p.PluginsDir, "marketplaces", "mktA", ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"name": "mktA",
		"plugins": []any{
			map[string]any{"name": "foo", "source": "./plugins/foo"},
			map[string]any{"name": "bar", "source": map[string]any{"source": "url", "url": "https://example.com/x.git"}},
		},
	}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(manifestDir, "marketplace.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	m, mdir, err := LoadMarketplace(p, "mktA")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(mdir, "mktA") {
		t.Errorf("marketplace dir: %s", mdir)
	}
	if len(m.Plugins) != 2 {
		t.Fatalf("want 2 plugins, got %d", len(m.Plugins))
	}
	got, err := m.FindPlugin("bar")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "bar" {
		t.Errorf("name: %s", got.Name)
	}
	// source should parse as object
	var src map[string]any
	if err := json.Unmarshal(got.Source, &src); err != nil {
		t.Fatal(err)
	}
	if src["source"] != "url" {
		t.Errorf("source kind: %v", src["source"])
	}
}

func TestFindPluginNearMatch(t *testing.T) {
	m := &MarketplaceManifest{
		Plugins: []MarketplacePlugin{
			{Name: "foo-bar"},
			{Name: "foo-baz"},
			{Name: "qux"},
		},
	}
	_, err := m.FindPlugin("foo")
	if err == nil {
		t.Fatal("should error on exact miss")
	}
	msg := err.Error()
	if !strings.Contains(msg, "foo-bar") || !strings.Contains(msg, "foo-baz") {
		t.Errorf("expected close-match suggestions, got: %s", msg)
	}
}

func TestCopyTreeSkipsDotGit(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	// README + sub/file.txt should exist
	if _, err := os.Stat(filepath.Join(dst, "README.md")); err != nil {
		t.Errorf("README missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "file.txt")); err != nil {
		t.Errorf("sub/file.txt missing: %v", err)
	}
	// .git should be absent
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		t.Error(".git should be skipped")
	}
}

func TestWithinDirRejectsSiblingPrefix(t *testing.T) {
	cases := []struct {
		candidate, root string
		want            bool
	}{
		// Legitimate paths inside the root
		{"/a/b/foo", "/a/b/foo", true},
		{"/a/b/foo/sub", "/a/b/foo", true},
		{"/a/b/foo/x/y/z", "/a/b/foo", true},
		// Sibling directory with common prefix — must be rejected
		{"/a/b/foo-evil", "/a/b/foo", false},
		{"/a/b/foo-evil/x", "/a/b/foo", false},
		// Traversal
		{"/a/b/foo/../bar", "/a/b/foo", false},
		{"/a", "/a/b/foo", false},
	}
	for _, c := range cases {
		if got := withinDir(c.candidate, c.root); got != c.want {
			t.Errorf("withinDir(%q, %q) = %v, want %v", c.candidate, c.root, got, c.want)
		}
	}
}

func TestListLocalMarketplaces(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{PluginsDir: filepath.Join(dir, "plugins")}
	mktsDir := filepath.Join(p.PluginsDir, "marketplaces")

	// Create one cloned marketplace (has .git) and one non-git dir.
	if err := os.MkdirAll(filepath.Join(mktsDir, "official", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mktsDir, "local-copy"), 0o755); err != nil {
		t.Fatal(err)
	}

	names, err := ListLocalMarketplaces(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "official" {
		t.Errorf("want [official], got %v", names)
	}
}

func TestListLocalMarketplacesEmpty(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{PluginsDir: filepath.Join(dir, "plugins")}
	// marketplaces dir doesn't exist yet — should return nil, not error.
	names, err := ListLocalMarketplaces(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("want empty, got %v", names)
	}
}

func TestUpdateInstallPreservesInstalledAt(t *testing.T) {
	dir := t.TempDir()
	installedPath := filepath.Join(dir, "installed.json")
	writeInstalledJSON(t, installedPath, "myplugin@mkt", map[string]any{
		"scope":        "user",
		"installPath":  "/cache/myplugin/oldsha",
		"version":      "oldsha",
		"gitCommitSha": "oldshaFull",
		"installedAt":  "2025-01-01T00:00:00Z",
		"lastUpdated":  "2025-01-01T00:00:00Z",
	})

	installed, err := config.LoadInstalledPlugins(installedPath)
	if err != nil {
		t.Fatal(err)
	}

	newInstPath := filepath.Join(dir, "cache", "newsha")
	if err := os.MkdirAll(newInstPath, 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Result{
		QualifiedID:  "myplugin@mkt",
		InstallPath:  newInstPath,
		Version:      "newsha",
		GitCommitSha: "newshaFull",
	}
	UpdateInstall(installed, r, "/cache/myplugin/oldsha")

	list := installed.List()
	if len(list) != 1 {
		t.Fatalf("want 1, got %d", len(list))
	}
	got := list[0]
	if got.InstalledAt != "2025-01-01T00:00:00Z" {
		t.Errorf("InstalledAt changed: got %q", got.InstalledAt)
	}
	if got.InstallPath != newInstPath {
		t.Errorf("InstallPath: got %q", got.InstallPath)
	}
	if got.GitCommitSha != "newshaFull" {
		t.Errorf("GitCommitSha: got %q", got.GitCommitSha)
	}
}

func TestUpdateInstallDoesNotTouchEnabledPlugins(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{"myplugin@mkt":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	installedPath := filepath.Join(dir, "installed.json")
	writeInstalledJSON(t, installedPath, "myplugin@mkt", map[string]any{
		"scope": "user", "installPath": "/old", "version": "old",
	})
	installed, err := config.LoadInstalledPlugins(installedPath)
	if err != nil {
		t.Fatal(err)
	}

	r := &Result{QualifiedID: "myplugin@mkt", InstallPath: "/new", Version: "new", GitCommitSha: "new"}
	UpdateInstall(installed, r, "/old")

	// enabledPlugins should still be false — UpdateInstall must not flip it.
	if enabled, _ := settings.PluginEnabled("myplugin@mkt"); enabled {
		t.Error("UpdateInstall must not enable a deliberately-disabled plugin")
	}
}

func TestUpdateInstallGCsOldPath(t *testing.T) {
	dir := t.TempDir()
	oldDir := filepath.Join(dir, "old-version")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "file.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	installedPath := filepath.Join(dir, "installed.json")
	writeInstalledJSON(t, installedPath, "p@m", map[string]any{"scope": "user", "installPath": oldDir})
	installed, err := config.LoadInstalledPlugins(installedPath)
	if err != nil {
		t.Fatal(err)
	}

	newPath := filepath.Join(dir, "new-version")
	r := &Result{QualifiedID: "p@m", InstallPath: newPath, Version: "new"}
	UpdateInstall(installed, r, oldDir)

	if _, err := os.Stat(oldDir); err == nil {
		t.Error("old version directory should have been removed by GC")
	}
}

func writeInstalledJSON(t *testing.T, path, id string, entry map[string]any) {
	t.Helper()
	raw := map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			id: []any{entry},
		},
	}
	b, _ := json.Marshal(raw)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCopyTreeOverwrites(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "old.txt"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Re-install: copyTree wipes dst first.
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); err == nil {
		t.Error("old.txt should be removed by wipe-then-copy")
	}
	b, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil || string(b) != "new" {
		t.Errorf("a.txt: %q %v", b, err)
	}
}

func writeMarketplaceManifest(t *testing.T, p paths.Paths, mkt string, pluginNames ...string) {
	t.Helper()
	dir := filepath.Join(p.PluginsDir, "marketplaces", mkt, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := make([]any, 0, len(pluginNames))
	for _, n := range pluginNames {
		plugins = append(plugins, map[string]any{"name": n, "source": "./plugins/" + n})
	}
	b, _ := json.Marshal(map[string]any{"name": mkt, "plugins": plugins})
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemovedFromMarketplace(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{PluginsDir: filepath.Join(dir, "plugins")}

	// mktA still lists "stays" but no longer lists "gone".
	writeMarketplaceManifest(t, p, "mktA", "stays")
	// mktB has no local manifest at all — its plugins must NOT be flagged.

	ids := []string{
		"stays@mktA",  // present → not removed
		"gone@mktA",   // absent from synced manifest → removed
		"x@mktB",      // marketplace not cached → skip (no false positive)
		"unqualified", // no @marketplace → skip
	}
	got := RemovedFromMarketplace(p, ids)
	if len(got) != 1 || !got["gone@mktA"] {
		t.Errorf("RemovedFromMarketplace = %v, want {gone@mktA:true}", got)
	}
}

// gitRun runs a git command in dir with a deterministic author/committer identity
// so commits succeed regardless of the host's global git config (and CI).
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitCommit(t *testing.T, repo, file, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "c")
	return gitRun(t, repo, "rev-parse", "HEAD")
}

// TestPullMarketplacesForPlugins reproduces the stale-marketplace update loop:
// a bare-string plugin's marketplace clone is never refreshed during plugin update,
// so the recorded sha never advances to match the upstream HEAD the staleness probe
// compares against. The helper must fast-forward the local clone so a subsequent
// Install records the new sha.
func TestPullMarketplacesForPlugins(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{PluginsDir: filepath.Join(dir, "plugins")}
	mktsDir := filepath.Join(p.PluginsDir, "marketplaces")

	// Upstream working repo with commit A.
	up := filepath.Join(dir, "upstream")
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, up, "init", "-q", "-b", "main")
	shaA := gitCommit(t, up, "f.txt", "A")

	// Clone upstream into the marketplace dir (local clone now at A).
	mktDir := filepath.Join(mktsDir, "mktA")
	gitRun(t, dir, "clone", "-q", up, mktDir)

	// A non-git "local source" marketplace — must be skipped, not error.
	if err := os.MkdirAll(filepath.Join(mktsDir, "local-copy"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Upstream advances to commit B.
	shaB := gitCommit(t, up, "f.txt", "B")
	if shaA == shaB {
		t.Fatal("setup: upstream sha did not advance")
	}

	errs := PullMarketplacesForPlugins(p, []string{
		"foo@mktA", "bar@mktA", // same marketplace twice -> pulled once, no dup error
		"baz@local-copy", // non-git -> skipped silently
		"unqualified",    // no @marketplace -> skipped
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if head := LocalMarketplaceHead(p, "mktA"); head != shaB {
		t.Errorf("marketplace not fast-forwarded: local HEAD = %s, want upstream %s", head, shaB)
	}
}
