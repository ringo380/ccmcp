package paths

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestProjectKeyMatchesWhatClaudeCodeWrites pins the key form observed in a
// live ~/.claude.json on Windows (fancy-pc, 2026-09-27): forward slashes,
// drive letter kept, no trailing slash - "C:/Users/ringo/git/ccmcp". On Unix
// the path is returned cleaned and otherwise untouched.
func TestProjectKeyMatchesWhatClaudeCodeWrites(t *testing.T) {
	var cases []struct{ in, want string }
	if runtime.GOOS == "windows" {
		cases = []struct{ in, want string }{
			{`C:\Users\ringo\git\ccmcp`, "C:/Users/ringo/git/ccmcp"},
			{`C:\Users\ringo\git\ccmcp\`, "C:/Users/ringo/git/ccmcp"},  // trailing separator
			{`C:\Users\ringo\git\.\ccmcp`, "C:/Users/ringo/git/ccmcp"}, // dot segment
			{`C:/Users/ringo/git/ccmcp`, "C:/Users/ringo/git/ccmcp"},   // already canonical
			{`C:\`, "C:/"}, // drive root, seen live
			// os.Getwd hands back an uppercase drive letter on this box even
			// after `cd c:\...`, and Claude Code keys on its cwd, so a typed
			// lowercase drive must not yield a key Claude Code never reads.
			{`c:\Users\ringo\git\ccmcp`, "C:/Users/ringo/git/ccmcp"},
			{`c:/users/ringo/git/ccmcp`, "C:/users/ringo/git/ccmcp"}, // only the drive is case-normalized
			// UNC: cleaned and slashed like any other path. No UNC key has
			// been observed in a live file; this pins the current behavior,
			// not a Claude Code contract.
			{`\\server\share\proj`, "//server/share/proj"},
		}
	} else {
		cases = []struct{ in, want string }{
			{"/Users/x/git/ccmcp", "/Users/x/git/ccmcp"},
			{"/Users/x/git/ccmcp/", "/Users/x/git/ccmcp"},
			{"/Users/x/git/./ccmcp", "/Users/x/git/ccmcp"},
		}
	}
	for _, c := range cases {
		got, err := ProjectKey(c.in)
		if err != nil {
			t.Fatalf("ProjectKey(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ProjectKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestProjectKeyMakesRelativePathsAbsolute: a relative --path must key on the
// absolute directory, or every project would collide on ".".
func TestProjectKeyMakesRelativePathsAbsolute(t *testing.T) {
	got, err := ProjectKey(".")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(filepath.FromSlash(got)) {
		t.Fatalf("ProjectKey(\".\") = %q, want an absolute path", got)
	}
}

// TestSameProjectToleratesLegacySpellings: the live file on fancy-pc holds
// both "C:/..." and "C:\" keys, and Windows paths are case-insensitive.
func TestSameProjectToleratesLegacySpellings(t *testing.T) {
	if runtime.GOOS == "windows" {
		if !SameProject(`C:\Users\x\proj`, "C:/Users/x/proj") {
			t.Fatal("backslash and forward-slash spellings must match")
		}
		if !SameProject(`c:/users/x/proj`, "C:/Users/x/proj") {
			t.Fatal("case must not matter on Windows")
		}
		if SameProject("C:/Users/x/proj", "C:/Users/x/proj2") {
			t.Fatal("different directories must not match")
		}
		return
	}
	if !SameProject("/Users/x/proj/", "/Users/x/proj") {
		t.Fatal("a trailing slash must not break equality")
	}
	if SameProject("/Users/x/Proj", "/Users/x/proj") {
		t.Fatal("Unix paths are case-sensitive")
	}
}
