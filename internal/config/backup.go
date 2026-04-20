package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Backup copies the given file into backupsDir/<basename>-<ts>.json.
// Silently no-ops if src doesn't exist (nothing to back up). Collisions within the same
// second get a numeric suffix so rapid-fire mutations still succeed.
func Backup(src, backupsDir string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(backupsDir, 0o755); err != nil {
		return err
	}
	ts := time.Now().Format("20060102-150405")
	base := filepath.Base(src)
	// Strip the extension, then strip leading dots so ~/.claude.json -> "claude" not ".claude".
	name := strings.TrimLeft(strings.TrimSuffix(base, filepath.Ext(base)), ".")
	if name == "" {
		name = base
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	var out *os.File
	for attempt := 0; attempt < 100; attempt++ {
		var dst string
		if attempt == 0 {
			dst = filepath.Join(backupsDir, fmt.Sprintf("%s-%s.json", name, ts))
		} else {
			dst = filepath.Join(backupsDir, fmt.Sprintf("%s-%s-%d.json", name, ts, attempt))
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			out = f
			break
		}
		if !os.IsExist(err) {
			return err
		}
	}
	if out == nil {
		return fmt.Errorf("backup: exhausted same-second collision attempts for %s", src)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
