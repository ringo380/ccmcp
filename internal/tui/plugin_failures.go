package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/install"
)

// lastFailuresFile is the on-disk persistence path for the most-recent bulk-update
// failures. Lives under BackupsDir so it shares the existing GC discipline.
const lastFailuresFile = "last-bulk-failures.json"

// lastFailuresMaxAge bounds how long a persisted record is considered fresh. After
// this window, loadLastFailures returns no result and the file is treated as stale.
const lastFailuresMaxAge = 7 * 24 * time.Hour

type lastFailuresEnvelope struct {
	Timestamp string              `json:"timestamp"`
	Failures  []bulkUpdateFailure `json:"failures"`
}

// classifyUpdateError returns a short actionable hint based on the error text from
// a failed `install.Install` call. Hints are best-effort: they're computed from
// substring matching against well-known git failure modes. When nothing matches we
// fall through to a generic suggestion.
func classifyUpdateError(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "permission denied"):
		return "permission denied — check write access to the plugin cache directory"
	case strings.Contains(l, "could not resolve host"),
		strings.Contains(l, "connection refused"),
		strings.Contains(l, "network is unreachable"),
		strings.Contains(l, "tls handshake timeout"),
		strings.Contains(l, "operation timed out"):
		return "network issue — retry when connectivity is back"
	case strings.Contains(l, "reference not found"),
		strings.Contains(l, "unknown revision"),
		strings.Contains(l, "did not match any file(s) known to git"),
		strings.Contains(l, "couldn't find remote ref"):
		return "marketplace SHA pin is stale — try refreshing the marketplace first (M tab → R)"
	case strings.Contains(l, "authentication failed"),
		strings.Contains(l, "could not read username"),
		strings.Contains(l, "403"):
		return "auth required — the source repo is private or needs credentials"
	case strings.Contains(l, "no space left"):
		return "disk full — free space under ~/.claude/plugins/cache"
	case strings.Contains(l, "already exists"):
		return "stale cache entry — press R in the panel to retry; ccmcp will overwrite"
	case strings.Contains(l, "not a git repository"):
		return "cache corrupted — remove the plugin's cache dir and reinstall"
	}
	return "see error text for details; press R to retry"
}

// saveLastFailures persists the current failure set to BackupsDir/last-bulk-failures.json.
// An empty `failures` removes the file (no stale data lingering after a clean run).
// All errors are swallowed by the caller — persistence is best-effort, the panel
// still works in-memory regardless.
func saveLastFailures(backupsDir string, failures []bulkUpdateFailure) error {
	path := filepath.Join(backupsDir, lastFailuresFile)
	if len(failures) == 0 {
		_ = os.Remove(path)
		return nil
	}
	if err := os.MkdirAll(backupsDir, 0o755); err != nil {
		return err
	}
	env := lastFailuresEnvelope{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Failures:  failures,
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// loadLastFailures returns the persisted failure set if it exists and is fresh
// (mtime within lastFailuresMaxAge). A stale or missing file returns ok=false; the
// stale file is left in place rather than deleted, since the user may want to see
// the timestamp via `ccmcp` later.
func loadLastFailures(backupsDir string) ([]bulkUpdateFailure, bool) {
	path := filepath.Join(backupsDir, lastFailuresFile)
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if time.Since(info.ModTime()) > lastFailuresMaxAge {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var env lastFailuresEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, false
	}
	if len(env.Failures) == 0 {
		return nil, false
	}
	return env.Failures, true
}

// updateFailures handles keystrokes while the failures panel is open.
func (v *pluginView) updateFailures(key tea.KeyMsg) tea.Cmd {
	n := len(v.lastFailures)
	switch key.String() {
	case "esc", "q":
		v.mode = ""
		v.failuresExpanded = false
		return nil
	case "up", "k":
		if v.failuresIndex > 0 {
			v.failuresIndex--
		}
		v.failuresExpanded = false
	case "down", "j":
		if v.failuresIndex < n-1 {
			v.failuresIndex++
		}
		v.failuresExpanded = false
	case "g":
		v.failuresIndex = 0
		v.failuresExpanded = false
	case "G":
		v.failuresIndex = n - 1
		v.failuresExpanded = false
	case "enter":
		v.failuresExpanded = !v.failuresExpanded
	case "R":
		// Retry the selected failure. Reuse the single-update path so the result
		// flows through pluginUpdateResultMsg (same handler as `U`). On success the
		// failure is removed from lastFailures.
		if v.updating || n == 0 {
			return nil
		}
		f := v.lastFailures[v.failuresIndex]
		name, mkt := config.ParsePluginID(f.ID)
		if mkt == "" {
			v.flash = styleErr.Render(f.ID + ": unqualified ID — cannot retry")
			return nil
		}
		var oldSha, oldInstPath string
		for _, ip := range v.st.installed.List() {
			if ip.ID == f.ID {
				oldSha = ip.GitCommitSha
				oldInstPath = ip.InstallPath
				break
			}
		}
		v.updating = true
		v.flash = styleProgress.Render("retrying " + f.ID + "…")
		id, p := f.ID, v.st.paths
		return func() tea.Msg {
			result, err := install.Install(p, mkt, name)
			return pluginUpdateResultMsg{id: id, oldSha: oldSha, oldInstPath: oldInstPath, result: result, err: err}
		}
	case "X":
		// Clear all failures (acknowledge + dismiss).
		v.lastFailures = nil
		_ = saveLastFailures(v.st.paths.BackupsDir, nil)
		v.mode = ""
		v.flash = styleDim.Render("cleared failure record")
		return nil
	}
	return nil
}

// renderFailures paints the failure panel sub-view.
func (v *pluginView) renderFailures() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf("Bulk-update failures (%d)", len(v.lastFailures))))
	b.WriteString("\n\n")
	if len(v.lastFailures) == 0 {
		b.WriteString(styleDim.Render("  (no failures)"))
		b.WriteString("\n")
	}
	for i, f := range v.lastFailures {
		marker := "  "
		if i == v.failuresIndex {
			marker = styleSelected.Render("▶ ")
		}
		oneLine := firstLine(f.Err)
		row := fmt.Sprintf("%s%s", marker, f.ID)
		b.WriteString(row)
		b.WriteString("\n    ")
		b.WriteString(styleErr.Render(truncate(oneLine, 100)))
		b.WriteString("\n    ")
		b.WriteString(styleDim.Render("hint: " + f.Hint))
		b.WriteString("\n")
		if i == v.failuresIndex && v.failuresExpanded {
			b.WriteString("\n")
			for _, line := range strings.Split(f.Err, "\n") {
				b.WriteString("      ")
				b.WriteString(styleDim.Render(line))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(styleDim.Render("[enter] expand/collapse · [R] retry · [X] clear all · [esc/q] back"))
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

