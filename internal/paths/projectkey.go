package paths

import (
	"path/filepath"
	"runtime"
	"strings"
)

// ProjectKey returns the string Claude Code uses as the key under
// ~/.claude.json#/projects for dir: absolute and cleaned, and on Windows with
// forward slashes. Observed live on Windows (fancy-pc, 2026-09-27): projects
// are keyed "C:/Users/ringo/git/ccmcp" while os.Getwd returns
// `C:\Users\ringo\git\ccmcp`, so indexing by the raw cwd misses every project
// and a write creates a duplicate. On Unix this is Abs+Clean and nothing else.
func ProjectKey(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return filepath.ToSlash(abs), nil
	}
	return abs, nil
}

// SameProject reports whether two project keys name the same directory: equal
// after normalization to ProjectKey form, and case-insensitively on Windows.
// Used to find a project under a legacy spelling (a `C:\` key exists in the
// live file) instead of creating a second key beside it.
func SameProject(a, b string) bool {
	na := normalizeKey(a)
	nb := normalizeKey(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(na, nb)
	}
	return na == nb
}

func normalizeKey(k string) string {
	k = filepath.Clean(k)
	if runtime.GOOS == "windows" {
		k = filepath.ToSlash(k)
	}
	return k
}
