package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubRepo builds a git repo whose symlinks are checked out as plain files
// holding the link target, which is what a clone produces on a default Git
// for Windows install (core.symlinks=false). files maps path to content,
// links maps path to link target.
func stubRepo(t *testing.T, files, links map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "config", "core.symlinks", "false")
	for p, body := range files {
		full := filepath.Join(repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, repo, "add", "--", p)
	}
	blobDir := t.TempDir()
	for p, target := range links {
		blob := filepath.Join(blobDir, "blob")
		if err := os.WriteFile(blob, []byte(target), 0o644); err != nil {
			t.Fatal(err)
		}
		sha := gitRun(t, repo, "hash-object", "-w", blob)
		gitRun(t, repo, "update-index", "--add", "--cacheinfo", "120000,"+sha+","+p)
		gitRun(t, repo, "checkout", "--", p)
	}
	return repo
}

// refuseSymlinks makes link creation fail, as it does on Windows without
// Developer Mode, for the duration of the test.
func refuseSymlinks(t *testing.T) {
	t.Helper()
	orig := symlink
	symlink = func(string, string) error { return errors.New("symlinks refused") }
	t.Cleanup(func() { symlink = orig })
}

// TestCopyTreeMaterializesGitSymlinkStubs: a clone with core.symlinks=false
// holds each link as a text file naming its target. The installed copy must
// carry the target's content (or a real link to it), not that text.
func TestCopyTreeMaterializesGitSymlinkStubs(t *testing.T) {
	repo := stubRepo(t,
		map[string]string{"real.md": "body", "shared/tool.md": "tool"},
		map[string]string{"link.md": "real.md", "plugin/shared": "../shared"},
	)
	for _, refuse := range []bool{false, true} {
		if refuse {
			refuseSymlinks(t)
		}
		dst := filepath.Join(t.TempDir(), "dst")
		if err := copyTree(repo, dst); err != nil {
			t.Fatalf("refuse=%v copyTree: %v", refuse, err)
		}
		if got, err := os.ReadFile(filepath.Join(dst, "link.md")); err != nil || string(got) != "body" {
			t.Fatalf("refuse=%v link.md = %q (%v), want the target's body", refuse, got, err)
		}
		if got, err := os.ReadFile(filepath.Join(dst, "plugin", "shared", "tool.md")); err != nil || string(got) != "tool" {
			t.Fatalf("refuse=%v plugin/shared/tool.md = %q (%v), want the linked directory's file", refuse, got, err)
		}
	}
}

// TestCopyTreeRefusesSymlinkLoops: when links must be copied, a link to an
// ancestor, to itself, or a pair of links pointing at each other would copy
// forever. Each must fail with an error that names the loop.
func TestCopyTreeRefusesSymlinkLoops(t *testing.T) {
	refuseSymlinks(t)
	cases := map[string]map[string]string{
		"ancestor": {"sub/up": ".."},
		"self":     {"sub/here": "."},
		"pair":     {"a/toB": "../b", "b/toA": "../a"},
	}
	for name, links := range cases {
		repo := stubRepo(t, map[string]string{"a/f.md": "f", "b/g.md": "g", "sub/h.md": "h"}, links)
		err := copyTree(repo, filepath.Join(t.TempDir(), "dst"))
		if err == nil || !strings.Contains(err.Error(), "loop") {
			t.Fatalf("%s: copyTree err = %v, want a symlink loop error", name, err)
		}
	}
}
