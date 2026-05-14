package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// doctorSnapshotKeep is the per-source-file retention floor. Tests override.
var doctorSnapshotKeep = 20

// deleteSnapshot best-effort removes a single snapshot file. Empty paths and
// already-gone files are silently tolerated; any other error is also swallowed
// because failing to delete a snapshot must never block the user-visible flow
// (GC will sweep it within doctorSnapshotMaxAge).
func deleteSnapshot(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// doctorSnapshotMaxAge is the age cap; files older than this are deleted regardless of count.
var doctorSnapshotMaxAge = 30 * 24 * time.Hour

// snapshotForFix copies the file at src into snapshotDir as
//
//	<basename>-<unix-ts>-<N>.<ext>
//
// preserving the original extension. Returns the absolute path to the snapshot, or
// ("", nil) when src does not exist (nothing to snapshot). Errors are returned verbatim.
//
// Same-unix-second collisions get a 1..99 counter suffix.
func snapshotForFix(src, snapshotDir string) (string, error) {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return "", err
	}
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = base
		ext = ""
	}
	ts := time.Now().Unix()

	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	for attempt := 0; attempt < 100; attempt++ {
		dst := filepath.Join(snapshotDir, fmt.Sprintf("%s-%d-%d%s", stem, ts, attempt, ext))
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			defer f.Close()
			if _, err := io.Copy(f, in); err != nil {
				return "", err
			}
			if err := f.Sync(); err != nil {
				return "", err
			}
			return dst, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("snapshot: exhausted same-second collision attempts for %s", src)
}

// gcDoctorSnapshots prunes snapshots in dir under two rules, whichever deletes more:
//   - Per source file, keep at most `keep` snapshots (newest by mtime).
//   - Delete any snapshot older than `maxAge`.
//
// Source-file grouping strips the trailing "-<unix>-<N>" before the extension. dir is
// created if missing (no-op if no files yet). Errors stat-ing or removing individual files
// are not fatal; the function returns the last error seen.
func gcDoctorSnapshots(dir string, keep int, maxAge time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	type fileInfo struct {
		path  string
		mtime time.Time
	}
	groups := map[string][]fileInfo{}
	now := time.Now()
	var lastErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			lastErr = err
			continue
		}
		// Age cap: delete unconditionally if too old.
		if maxAge > 0 && now.Sub(info.ModTime()) > maxAge {
			if err := os.Remove(full); err != nil {
				lastErr = err
			}
			continue
		}
		stem := groupStem(e.Name())
		groups[stem] = append(groups[stem], fileInfo{path: full, mtime: info.ModTime()})
	}
	if keep <= 0 {
		return lastErr
	}
	for _, files := range groups {
		if len(files) <= keep {
			continue
		}
		sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
		for _, f := range files[keep:] {
			if err := os.Remove(f.path); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// groupStem strips the trailing "-<digits>-<digits>" before the extension to recover the
// original source basename. "MEMORY.md-1715500000-0.md" → "MEMORY.md".
func groupStem(name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	// Strip trailing "-<N>"
	if i := strings.LastIndex(stem, "-"); i > 0 {
		tail := stem[i+1:]
		if isDigits(tail) {
			stem = stem[:i]
		}
	}
	// Strip trailing "-<unix>"
	if i := strings.LastIndex(stem, "-"); i > 0 {
		tail := stem[i+1:]
		if isDigits(tail) {
			stem = stem[:i]
		}
	}
	return stem + ext
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// doctorSnapshotDir returns the snapshot directory beneath the configured backups dir.
func doctorSnapshotDir(backupsDir string) string {
	return filepath.Join(backupsDir, "doctor")
}
