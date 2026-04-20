package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadJSON loads `dst` from a JSON file. Missing file yields the zero value of dst (nil error).
func ReadJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// WriteJSON writes `src` to disk atomically (temp file + rename), indented for readability,
// preserving the destination's permissions if it already exists (else 0600 for files under $HOME).
func WriteJSON(path string, src any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	b, err := json.MarshalIndent(src, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	b = append(b, '\n')
	return atomicWrite(path, b, perm)
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ccmcp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// RawJSON loads the raw map so we can mutate only known keys without losing unknown fields.
func RawJSON(path string) (map[string]any, error) {
	out := map[string]any{}
	if err := ReadJSON(path, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
