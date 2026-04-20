package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
