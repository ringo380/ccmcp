package mcpprobe

import "github.com/ringo380/ccmcp/internal/config"

// CacheVersion is bumped whenever the stored shape or the token accounting
// changes, invalidating every entry. Token counts are cached, so a tokenizer
// change must bump this.
const CacheVersion = 1

// Cache is the on-disk store of completed probe results, keyed by
// Target.Key(). It exists so probing happens once per server rather than on
// every visit - including a server that fails, since a hang is exactly the
// cost this cache is meant to spare the user on repeat visits.
type Cache struct {
	Path    string            `json:"-"`
	Version int               `json:"version"`
	Entries map[string]Result `json:"entries"`
}

// cacheFile is the on-disk shape. Kept separate from Cache so Path (which is
// where the file lives, not something to persist inside it) never round
// trips through JSON.
type cacheFile struct {
	Version int               `json:"version"`
	Entries map[string]Result `json:"entries"`
}

// LoadCache reads path into a Cache. A missing file is the first-run case and
// yields an empty cache, not an error. A cache written by an older
// CacheVersion is discarded wholesale rather than partially trusted - see the
// CacheVersion doc comment for why a stale entry is worse than no entry.
//
// A file that exists but cannot be decoded (truncated, corrupted, or holding
// something other than a cache) is treated exactly like a missing one: it is
// a cache, so a corrupt copy is a miss, not a fatal condition, and the next
// Save overwrites it. LoadCache also never returns a nil *Cache - even an
// error path (should one ever be added here) must hand back a usable empty
// cache, because the natural caller pattern on a TUI render path is to log an
// error and keep going, and a nil result there panics on the first Get.
func LoadCache(path string) (*Cache, error) {
	empty := func() *Cache {
		return &Cache{Path: path, Version: CacheVersion, Entries: map[string]Result{}}
	}

	var f cacheFile
	if err := config.ReadJSON(path, &f); err != nil {
		return empty(), nil
	}
	if f.Version != CacheVersion || f.Entries == nil {
		f.Entries = map[string]Result{}
	}
	return &Cache{Path: path, Version: CacheVersion, Entries: f.Entries}, nil
}

// Save writes the cache to disk atomically.
func (c *Cache) Save() error {
	return config.WriteJSON(c.Path, cacheFile{Version: CacheVersion, Entries: c.Entries})
}

// Get returns the cached result for key, if any.
func (c *Cache) Get(key string) (Result, bool) {
	r, ok := c.Entries[key]
	return r, ok
}

// Put stores r under r.Key, overwriting whatever was cached there before.
// Failures are stored exactly like successes - the cache does not filter on
// OK, because a cached failure is what keeps a hanging server from re-hanging
// the user on the next visit.
func (c *Cache) Put(r Result) {
	if c.Entries == nil {
		c.Entries = map[string]Result{}
	}
	c.Entries[r.Key] = r
}

// Delete removes key, reporting whether it was present.
func (c *Cache) Delete(key string) bool {
	if _, ok := c.Entries[key]; !ok {
		return false
	}
	delete(c.Entries, key)
	return true
}
