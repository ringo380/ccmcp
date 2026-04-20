package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadWriteJSONRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "file.json")
	want := map[string]any{
		"string": "hello",
		"int":    float64(42), // json.Unmarshal decodes numbers as float64
		"bool":   true,
		"nested": map[string]any{"arr": []any{"a", "b"}},
	}
	if err := WriteJSON(path, want); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got := map[string]any{}
	if err := ReadJSON(path, &got); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestReadJSONMissingIsNil(t *testing.T) {
	got := map[string]any{}
	if err := ReadJSON(filepath.Join(t.TempDir(), "nope.json"), &got); err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
}

func TestWriteJSONIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"keep":"me"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Overwrite; ensure no .tmp file remains in dir.
	if err := WriteJSON(path, map[string]any{"overwritten": true}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > 0 && e.Name()[0] == '.' && e.Name() != "." && e.Name() != ".." {
			// allow only dotfiles that existed before; .ccmcp-* tmp files should be gone
			if len(e.Name()) >= 7 && e.Name()[:7] == ".ccmcp-" {
				t.Fatalf("leftover tmp file: %s", e.Name())
			}
		}
	}
}

func TestWriteJSONPreservesPerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("want 0644, got %o", info.Mode().Perm())
	}
}

func TestWriteJSONDefaultsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.json")
	if err := WriteJSON(path, map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600 for new file under home-ish paths, got %o", info.Mode().Perm())
	}
}
