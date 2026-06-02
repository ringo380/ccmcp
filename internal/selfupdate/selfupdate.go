// Package selfupdate runs an oh-my-zsh-style background check against the
// GitHub releases API for ringo380/ccmcp, prompts the user when a newer version
// is available, and dispatches to the appropriate upgrade command for the
// detected install method (brew tap vs go install vs raw binary).
//
// The check is gated by:
//   - Build version != "dev" (skips local development builds)
//   - $CCMCP_NO_UPDATE_CHECK is unset or empty
//   - --no-update-check flag not passed (wired in cmd/root.go)
//   - stdin AND stdout are TTYs (no point prompting in a pipe)
//   - Cached result freshness: trusted as-is under SoftTTL (15m); past that a
//     synchronous refresh (≤FetchTimeout) runs before deciding, so a release
//     published since the last check is surfaced on THIS launch, not the next
//   - Result has not been dismissed by the user in the dismiss-window
//
// All failures (network errors, malformed JSON, missing TTY) are silent —
// the check is best-effort and must never block ccmcp launch.
package selfupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ringo380/ccmcp/internal/paths"
)

const (
	// SoftTTL is the cache trust window. Under SoftTTL a cached result is used
	// as-is with no network call; at or past it, CheckOnLaunch refreshes
	// synchronously (capped at FetchTimeout) before deciding, so a release
	// published since the last check is surfaced on THIS launch. Kept short so
	// the post-release blind spot is small while routine back-to-back launches
	// within the window still avoid hitting the GitHub API.
	SoftTTL = 15 * time.Minute
	// DismissTTL is how long after the user answers "n" before we prompt again
	// for the same latest-version. Older than DismissTTL or a NEWER latest-version
	// re-arms the prompt.
	DismissTTL = 24 * time.Hour
	// FetchTimeout is the hard cap on the GitHub API request. Kept low so a slow
	// network never visibly stalls ccmcp launch.
	FetchTimeout = 2 * time.Second

	releasesAPI = "https://api.github.com/repos/ringo380/ccmcp/releases/latest"
)

// Status is the cached state of the update check. Persisted under
// ~/.claude/plugins/cache/ccmcp-update-check.json.
type Status struct {
	CheckedAt        time.Time `json:"checkedAt"`
	CurrentVersion   string    `json:"currentVersion"`
	LatestVersion    string    `json:"latestVersion"`
	LatestURL        string    `json:"latestUrl"`
	Body             string    `json:"body,omitempty"`
	DismissedAt      time.Time `json:"dismissedAt,omitempty"`
	DismissedVersion string    `json:"dismissedVersion,omitempty"`
}

// CachePath returns the absolute path of the cache file.
func CachePath(p paths.Paths) string {
	return filepath.Join(p.PluginsDir, "cache", "ccmcp-update-check.json")
}

// LoadCache reads the status cache; missing/malformed file returns zero-value.
func LoadCache(path string) Status {
	var s Status
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s) // best-effort
	return s
}

// SaveCache writes the status atomically (tmp + rename). Creates parent dirs.
func SaveCache(path string, s Status) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// IsFresh reports whether the cached check is still within TTL.
func (s Status) IsFresh(ttl time.Duration) bool {
	return !s.CheckedAt.IsZero() && time.Since(s.CheckedAt) < ttl
}

// HasUpdate reports whether s.LatestVersion is semver-greater than the supplied
// current version (which lets the caller compare against the live binary's
// version, not the one cached at last check).
func (s Status) HasUpdate(currentVersion string) bool {
	return CompareSemver(s.LatestVersion, currentVersion) > 0
}

// IsDismissed reports whether the user recently declined an update for this
// exact LatestVersion. A newer LatestVersion re-arms the prompt automatically.
func (s Status) IsDismissed() bool {
	return s.DismissedVersion == s.LatestVersion &&
		!s.DismissedAt.IsZero() &&
		time.Since(s.DismissedAt) < DismissTTL
}

// FetchLatest hits the GitHub releases API and returns (version, body, htmlURL).
// version is normalized without the leading "v" (e.g. "0.7.0").
func FetchLatest(ctx context.Context) (version, body, htmlURL string, err error) {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", "ccmcp-selfupdate")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("github releases api returned %s", resp.Status)
	}
	var payload struct {
		TagName    string `json:"tag_name"`
		Body       string `json:"body"`
		HTMLURL    string `json:"html_url"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", "", err
	}
	if payload.Prerelease || payload.Draft {
		return "", "", "", errors.New("latest release is a prerelease/draft")
	}
	return strings.TrimPrefix(payload.TagName, "v"), payload.Body, payload.HTMLURL, nil
}

// CompareSemver returns -1, 0, +1 for (a < b, a == b, a > b). Accepts versions
// with or without a "v" prefix, and tolerates pre-release suffixes (treated as
// equivalent to the base for comparison purposes; full semver pre-release
// ordering would be overkill here).
func CompareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

// ParseSemver splits a version string into its (major, minor, patch) integer
// components, tolerating a leading "v" and a pre-release/build suffix. Missing
// or non-numeric components are 0. Exported so other packages (e.g.
// internal/claudecode) reuse the single canonical parser instead of
// re-implementing semver parsing.
func ParseSemver(v string) (major, minor, patch int) {
	p := parseSemver(v)
	return p[0], p[1], p[2]
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop pre-release/build suffix.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, s := range parts {
		if i >= 3 {
			break
		}
		n, _ := strconv.Atoi(s)
		out[i] = n
	}
	return out
}

// Method identifies how ccmcp was installed.
type Method string

const (
	MethodBrew   Method = "brew"
	MethodGo     Method = "go install"
	MethodBinary Method = "binary"
)

// DetectMethod looks at the running executable's path to decide which upgrade
// command to suggest. Falls back to MethodBinary when nothing matches.
func DetectMethod() Method {
	exe, err := os.Executable()
	if err != nil {
		return MethodBinary
	}
	exe, _ = filepath.EvalSymlinks(exe)
	switch {
	case strings.Contains(exe, "/Cellar/"),
		strings.Contains(exe, "/opt/homebrew/"),
		strings.HasPrefix(exe, "/usr/local/Cellar/"),
		strings.HasPrefix(exe, "/home/linuxbrew/"):
		return MethodBrew
	case strings.Contains(exe, "/go/bin/"),
		strings.HasSuffix(filepath.Dir(exe), filepath.Join("go", "bin")):
		return MethodGo
	}
	return MethodBinary
}

// UpgradeCommand returns the argv that performs the upgrade for the given method.
// Empty slice means "no automatic command — user must upgrade manually".
func UpgradeCommand(m Method) []string {
	switch m {
	case MethodBrew:
		return []string{"brew", "upgrade", "ccmcp"}
	case MethodGo:
		return []string{"go", "install", "github.com/ringo380/ccmcp@latest"}
	default:
		return nil
	}
}

// MethodHint returns a one-line human-readable description of how to update
// when no automatic command is available (or as informational context).
func MethodHint(m Method, htmlURL string) string {
	switch m {
	case MethodBrew:
		return "Detected Homebrew install — will run: brew upgrade ccmcp"
	case MethodGo:
		return "Detected go install — will run: go install github.com/ringo380/ccmcp@latest"
	default:
		if htmlURL != "" {
			return "Manual install detected — download the latest binary from " + htmlURL
		}
		return "Manual install detected — visit https://github.com/ringo380/ccmcp/releases"
	}
}

// CheckOnLaunch is the entry point called once before the TUI launches. It:
//   - Returns immediately if the build is "dev" or $CCMCP_NO_UPDATE_CHECK is set
//   - Returns immediately if stdin/stdout aren't both TTYs
//   - Refreshes the cache synchronously (blocking, 2s cap) when it's at/past
//     SoftTTL or absent, so a release shipped since the last check is surfaced
//     on this launch; under SoftTTL it trusts the cache with no network call
//   - Prompts the user if an update is available and hasn't been recently dismissed
//   - When the user answers "Y" and an automatic command exists, runs it (stdout/
//     stderr inherited) and returns Decision{ExitAfter: true} so the caller can
//     bail out of TUI launch and let the user re-run the upgraded binary
//
// Errors are returned only when an upgrade command actually fails after the user
// confirmed — every other failure is swallowed so the TUI launch is unblocked.
type Decision struct {
	// Updated is true when an upgrade command ran successfully.
	Updated bool
	// ExitAfter is true when the caller should stop before launching the TUI.
	ExitAfter bool
}

// IsTTY checks whether the given file is a terminal. Reads the underlying fd
// and calls IsTerminal under the hood; falls back to false on any error.
func IsTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// SkipCheck returns true when the configured environment indicates we must not
// run the update check at all (dev build, opt-out env var, non-TTY).
func SkipCheck(currentVersion string) bool {
	if currentVersion == "" || currentVersion == "dev" || strings.HasPrefix(currentVersion, "dev ") {
		return true
	}
	if v := os.Getenv("CCMCP_NO_UPDATE_CHECK"); v != "" && v != "0" && v != "false" {
		return true
	}
	if !IsTTY(os.Stdin) || !IsTTY(os.Stdout) {
		return true
	}
	return false
}

// RefreshCache runs FetchLatest and writes the new Status to disk if the fetch
// succeeds. Preserves DismissedAt/DismissedVersion across refreshes so a "n"
// answer survives a cache refresh as long as LatestVersion hasn't moved.
func RefreshCache(ctx context.Context, cachePath, currentVersion string, prev Status) Status {
	latestVer, body, htmlURL, err := FetchLatest(ctx)
	if err != nil {
		// Don't overwrite a previous successful result with a failed one. Bump
		// CheckedAt to throttle retries when the network is down.
		prev.CheckedAt = time.Now().UTC()
		_ = SaveCache(cachePath, prev)
		return prev
	}
	next := Status{
		CheckedAt:      time.Now().UTC(),
		CurrentVersion: currentVersion,
		LatestVersion:  latestVer,
		LatestURL:      htmlURL,
		Body:           body,
	}
	// Carry the dismissal forward iff it's still for the same LatestVersion.
	if prev.DismissedVersion == latestVer {
		next.DismissedAt = prev.DismissedAt
		next.DismissedVersion = prev.DismissedVersion
	}
	_ = SaveCache(cachePath, next)
	return next
}

// refreshMode is how CheckOnLaunch should refresh the cache given its age.
type refreshMode int

const (
	refreshNone refreshMode = iota // cache is within SoftTTL; trust it, no network
	refreshSync                    // at/past SoftTTL (or no cache); block and refresh now
)

// chooseRefresh decides the refresh strategy from the cache's age. Pure so the
// SoftTTL windowing is unit-testable without touching the network.
func chooseRefresh(s Status, now time.Time) refreshMode {
	if s.CheckedAt.IsZero() {
		return refreshSync
	}
	if now.Sub(s.CheckedAt) >= SoftTTL {
		return refreshSync
	}
	return refreshNone
}

// CheckOnLaunch wires together the pieces above. Pure plumbing — no business
// logic lives here that isn't tested through the exported helpers.
func CheckOnLaunch(ctx context.Context, p paths.Paths, currentVersion string) Decision {
	if SkipCheck(currentVersion) {
		return Decision{}
	}
	cachePath := CachePath(p)
	status := LoadCache(cachePath)
	if chooseRefresh(status, time.Now()) == refreshSync {
		// At/past SoftTTL (or first run): refresh synchronously (FetchTimeout cap)
		// so a release shipped since the last check is surfaced on THIS launch.
		status = RefreshCache(ctx, cachePath, currentVersion, status)
	}
	if !status.HasUpdate(currentVersion) || status.IsDismissed() {
		return Decision{}
	}
	method := DetectMethod()
	return prompt(p, currentVersion, status, method, UpgradeCommand(method), os.Stdin, os.Stdout)
}

// prompt is split out so tests can drive it with synthetic readers/writers and
// inject an arbitrary upgrade command — DetectMethod's result depends on the
// running binary's path and isn't useful to test against.
func prompt(p paths.Paths, currentVersion string, status Status, method Method, cmd []string, in io.Reader, out io.Writer) Decision {
	fmt.Fprintf(out, "\nccmcp v%s is available (you have v%s).\n", status.LatestVersion, currentVersion)
	if status.Body != "" {
		fmt.Fprintf(out, "\n%s\n", truncateBody(status.Body, 12))
	}
	fmt.Fprintf(out, "\n%s\n", MethodHint(method, status.LatestURL))

	if len(cmd) == 0 {
		fmt.Fprintln(out, "Press Enter to continue.")
		_ = readLine(in)
		dismiss(p, status)
		return Decision{}
	}

	fmt.Fprint(out, "\nUpdate now? [Y/n] ")
	line := strings.TrimSpace(strings.ToLower(readLine(in)))
	if line == "n" || line == "no" {
		dismiss(p, status)
		fmt.Fprintf(out, "Skipped. Will check again in %s.\n", DismissTTL)
		return Decision{}
	}

	fmt.Fprintf(out, "Running: %s\n\n", strings.Join(cmd, " "))
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdout = out
	c.Stderr = out
	if err := c.Run(); err != nil {
		fmt.Fprintf(out, "\nUpgrade command failed: %v\nLaunching the existing version.\n", err)
		return Decision{}
	}
	fmt.Fprintln(out, "\nUpgrade complete. Re-run `ccmcp` to launch the new version.")
	// Persist the dismissal so subsequent re-runs (before brew has stamped the
	// new binary path) don't prompt again immediately.
	dismiss(p, status)
	return Decision{Updated: true, ExitAfter: true}
}

func dismiss(p paths.Paths, status Status) {
	status.DismissedAt = time.Now().UTC()
	status.DismissedVersion = status.LatestVersion
	_ = SaveCache(CachePath(p), status)
}

func readLine(r io.Reader) string {
	br := bufio.NewReader(r)
	line, _ := br.ReadString('\n')
	return line
}

// truncateBody trims a release-notes Markdown body to at most n lines of useful
// content. Strips Markdown comments, empty separators, and trailing whitespace.
func truncateBody(body string, n int) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(strings.TrimSpace(s), "<!--") {
			continue
		}
		out = append(out, s)
		if len(out) >= n {
			out = append(out, "  …")
			break
		}
	}
	return strings.Join(out, "\n")
}

// runtimeGOOS is exposed for tests that want to assert OS-specific behavior.
// Currently unused but kept here so future method-detection per-OS branches
// have a seam.
var runtimeGOOS = runtime.GOOS
