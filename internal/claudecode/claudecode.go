// Package claudecode detects the actively-installed Claude Code CLI version and
// derives a Capabilities bundle that calibrates ccmcp's version-sensitive
// behavior — asset-lint limits, the headless fix/review model, and
// fallback-model support — to that exact version.
//
// To support a NEW Claude Code version, edit ONLY CapabilitiesFor (in
// capabilities.go). Every consumer reads a resolved Capabilities (or a
// LintConfig derived from it) rather than re-deriving from the version, so all
// version logic lives in one place.
//
// Detection is best-effort: when `claude` is absent from $PATH or its
// --version output is unparseable, Detect returns an unknown Version and
// CapabilitiesFor falls back to the conservative Baseline. Detection never
// errors out a caller.
package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ringo380/ccmcp/internal/paths"
	"github.com/ringo380/ccmcp/internal/selfupdate"
)

// SoftTTL is the fallback freshness window for a cached probe when the binary's
// identity (path + mtime) can't be confirmed unchanged. The mtime check is the
// primary, precise invalidation signal — a Claude Code upgrade swaps the
// versioned install and changes the resolved binary's mtime — so this TTL only
// matters on filesystems that don't report mtime.
const SoftTTL = 15 * time.Minute

// Version is a parsed Claude Code semver plus the raw (suffix-stripped) string.
// The zero value is "unknown" (Known() == false).
type Version struct {
	Raw   string
	Major int
	Minor int
	Patch int
}

// Known reports whether a version was successfully detected and parsed.
func (v Version) Known() bool { return v.Raw != "" }

// String returns the semver, or "unknown" when undetected.
func (v Version) String() string {
	if v.Raw == "" {
		return "unknown"
	}
	return v.Raw
}

// AtLeast reports whether v >= the supplied semver. An unknown version is never
// AtLeast anything, so version-gated capabilities default off when undetected.
func (v Version) AtLeast(s string) bool {
	if !v.Known() {
		return false
	}
	return selfupdate.CompareSemver(v.Raw, s) >= 0
}

// Info is the persisted probe result, cached under
// ~/.claude/plugins/cache/ccmcp-claude-version.json.
type Info struct {
	CheckedAt  time.Time `json:"checkedAt"`
	BinPath    string    `json:"binPath"`
	BinModTime time.Time `json:"binModTime"`
	Version    string    `json:"version"` // "" => probed but missing/unparseable
}

// lookupClaude resolves the `claude` binary on $PATH, follows symlinks to the
// real (versioned) binary, and returns its path + mtime. Split from the spawn so
// Detect can validate the cache without running a subprocess. Swappable in tests.
var lookupClaude = func() (binPath string, modTime time.Time, err error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", time.Time{}, err
	}
	real := bin
	if resolved, rerr := filepath.EvalSymlinks(bin); rerr == nil {
		real = resolved
	}
	if fi, serr := os.Stat(real); serr == nil {
		return real, fi.ModTime(), nil
	}
	return real, time.Time{}, nil
}

// runClaudeVersion spawns `claude --version` and returns its stdout. Swappable
// in tests to fake the version string without a real subprocess.
var runClaudeVersion = func(binPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binPath, "--version").Output()
	return string(out), err
}

// ParseVersion strips the " (Claude Code)" suffix (everything from the first
// space) and parses the leading semver. Returns an unknown Version when the
// leading token isn't a dotted numeric semver (e.g. "dev", "", garbage).
func ParseVersion(raw string) Version {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Version{}
	}
	tok := raw
	if i := strings.IndexByte(tok, ' '); i > 0 {
		tok = tok[:i]
	}
	maj, min, patch := selfupdate.ParseSemver(tok)
	if maj == 0 && min == 0 && patch == 0 {
		return Version{} // unparseable, or a 0.0.0 that isn't a real CC version
	}
	return Version{Raw: tok, Major: maj, Minor: min, Patch: patch}
}

// CachePath returns the absolute path of the version cache file.
func CachePath(p paths.Paths) string {
	return filepath.Join(p.PluginsDir, "cache", "ccmcp-claude-version.json")
}

func loadInfo(path string) (Info, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Info{}, false
	}
	var i Info
	if json.Unmarshal(b, &i) != nil {
		return Info{}, false
	}
	return i, true
}

func saveInfo(path string, i Info) {
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return
	}
	// Per-process tmp suffix so concurrent ccmcp processes can't clobber each
	// other's write or rename a torn file into place.
	tmp := path + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}

// Detect returns the installed Claude Code version. It reuses a cached probe
// when the resolved binary's path and mtime are unchanged (a CC upgrade changes
// one or both), otherwise spawns `claude --version` and rewrites the cache. All
// failures degrade to an unknown Version — Detect never errors.
func Detect(p paths.Paths) Version {
	cachePath := CachePath(p)
	cached, haveCache := loadInfo(cachePath)

	binPath, modTime, err := lookupClaude()
	if err != nil {
		// `claude` not resolvable. Persist an unknown probe so we don't rewrite
		// the cache on every call — but only when the existing marker is stale,
		// otherwise this path would re-write on every Detect (no actual throttle).
		if !(haveCache && cached.BinPath == "" && !cached.CheckedAt.IsZero() && time.Since(cached.CheckedAt) < SoftTTL) {
			saveInfo(cachePath, Info{CheckedAt: time.Now().UTC()})
		}
		return Version{}
	}

	if haveCache && cached.BinPath == binPath {
		switch {
		case !modTime.IsZero() && cached.BinModTime.Equal(modTime):
			// Binary unchanged since last probe. A KNOWN version is trusted until
			// the mtime changes (a CC upgrade swaps the versioned install). An
			// unknown cached.Version means the last probe FAILED transiently
			// (spawn error / unparseable output) — never pin that until mtime
			// changes; re-probe once the TTL backstop lapses so a recovered
			// `claude --version` is picked up.
			if cached.Version != "" || (!cached.CheckedAt.IsZero() && time.Since(cached.CheckedAt) < SoftTTL) {
				return versionFromInfo(cached)
			}
		case modTime.IsZero() && !cached.CheckedAt.IsZero() && time.Since(cached.CheckedAt) < SoftTTL:
			// mtime unavailable; fall back to the TTL backstop to avoid re-spawning.
			return versionFromInfo(cached)
		}
	}

	out, rerr := runClaudeVersion(binPath)
	info := Info{CheckedAt: time.Now().UTC(), BinPath: binPath, BinModTime: modTime}
	if rerr == nil {
		if v := ParseVersion(out); v.Known() {
			info.Version = v.Raw
		}
	}
	saveInfo(cachePath, info)
	return versionFromInfo(info)
}

func versionFromInfo(i Info) Version {
	if i.Version == "" {
		return Version{}
	}
	return ParseVersion(i.Version)
}
