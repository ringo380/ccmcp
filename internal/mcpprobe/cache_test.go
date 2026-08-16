package mcpprobe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCacheMissingFileIsEmptyNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	c, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache on a missing file returned an error: %v", err)
	}
	if len(c.Entries) != 0 {
		t.Fatalf("expected an empty cache, got %d entries", len(c.Entries))
	}
	if c.Path != path {
		t.Fatalf("expected Path %q, got %q", path, c.Path)
	}
}

// TestLoadCacheCorruptFileSelfHeals proves that a cache file that exists but
// cannot be decoded is treated as a miss, not a fatal error, and that the
// corruption does not persist: the very next Save overwrites it with a valid
// file.
func TestLoadCacheCorruptFileSelfHeals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	c, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache on a corrupt file returned an error: %v", err)
	}
	if len(c.Entries) != 0 {
		t.Fatalf("expected a corrupt file to yield an empty cache, got %d entries", len(c.Entries))
	}

	// Self-heal: writing through the returned cache must replace the corrupt
	// file with something LoadCache can read back cleanly.
	c.Put(Result{Key: "k", Name: "n", OK: true})
	if err := c.Save(); err != nil {
		t.Fatalf("Save after loading a corrupt file: %v", err)
	}
	reloaded, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache after self-heal: %v", err)
	}
	if _, ok := reloaded.Get("k"); !ok {
		t.Fatalf("expected the self-healed cache to contain the entry written after load")
	}
}

// TestLoadCacheNeverReturnsNilCache proves the specific bug a caller that
// logs the error and continues (the natural pattern on a TUI render path)
// would hit: a nil *Cache from LoadCache panics on the first Get. Asserting
// err == nil alone would pass even if c were nil, so this calls Get on the
// returned cache directly - if LoadCache ever went back to returning
// (nil, err), this test crashes with a nil pointer dereference rather than
// failing cleanly.
func TestLoadCacheNeverReturnsNilCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	c, _ := LoadCache(path)
	if c == nil {
		t.Fatalf("LoadCache returned a nil *Cache")
	}
	// A log-and-continue caller does exactly this next - no nil check, no
	// panic recovery. If c were nil this line would crash the test.
	if _, ok := c.Get("anything"); ok {
		t.Fatalf("expected no entry in a freshly loaded cache")
	}
}

func TestCacheRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	c, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	want := Result{
		Key:               "stdio:npx:-y:foo",
		Name:              "foo",
		ProbedAt:          time.Now().Truncate(time.Second).UTC(),
		OK:                true,
		InstructionTokens: 42,
		Tools:             []Tool{{Name: "bar", SchemaTokens: 7}},
		NameTokens:        3,
	}
	c.Put(want)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache after Save: %v", err)
	}
	got, ok := reloaded.Get(want.Key)
	if !ok {
		t.Fatalf("expected key %q to be present after reload", want.Key)
	}
	if got.Name != want.Name || got.OK != want.OK || got.InstructionTokens != want.InstructionTokens ||
		got.NameTokens != want.NameTokens || len(got.Tools) != 1 || got.Tools[0].Name != "bar" ||
		got.Tools[0].SchemaTokens != 7 || !got.ProbedAt.Equal(want.ProbedAt) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}

	if deleted := reloaded.Delete(want.Key); !deleted {
		t.Fatalf("expected Delete to report the key was present")
	}
	if _, ok := reloaded.Get(want.Key); ok {
		t.Fatalf("expected key to be gone after Delete")
	}
	if deleted := reloaded.Delete(want.Key); deleted {
		t.Fatalf("expected a second Delete of the same key to report false")
	}
}

func TestCacheDiscardsEntriesFromAnOlderVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	stale := struct {
		Version int               `json:"version"`
		Entries map[string]Result `json:"entries"`
	}{
		Version: 0, // older than CacheVersion
		Entries: map[string]Result{
			"stdio:npx:-y:foo": {Key: "stdio:npx:-y:foo", Name: "foo", OK: true, InstructionTokens: 99},
		},
	}
	b, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	c, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(c.Entries) != 0 {
		t.Fatalf("expected a version mismatch to discard every entry, got %d", len(c.Entries))
	}
	if c.Version != CacheVersion {
		t.Fatalf("expected a freshly loaded cache to carry the current version %d, got %d", CacheVersion, c.Version)
	}
}

func TestCacheStoresFailuresToo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	c, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	failed := Result{
		Key:      "stdio:npx:-y:hangs",
		Name:     "hangs",
		ProbedAt: time.Now().Truncate(time.Second).UTC(),
		OK:       false,
		Err:      "timed out waiting for a response",
	}
	c.Put(failed)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache after Save: %v", err)
	}
	got, ok := reloaded.Get(failed.Key)
	if !ok {
		t.Fatalf("expected the failed result to have persisted")
	}
	if got.OK {
		t.Fatalf("expected OK=false to survive the round trip")
	}
	if got.Err != failed.Err {
		t.Fatalf("expected Err %q to survive the round trip, got %q", failed.Err, got.Err)
	}
	if !got.ProbedAt.Equal(failed.ProbedAt) {
		t.Fatalf("expected ProbedAt to survive the round trip, got %v want %v", got.ProbedAt, failed.ProbedAt)
	}
}
