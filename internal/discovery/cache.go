package discovery

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// CacheTTL is the freshness window for the discovery cache. Reads inside this
// window short-circuit network calls and return the cached result.
const CacheTTL = 6 * time.Hour

// CacheGrace extends the cache window when all sources fail (e.g. offline). A
// stale-but-recent cache is preferable to an empty list.
const CacheGrace = 72 * time.Hour

// CachePath returns the on-disk location ccmcp uses for the discovery cache.
// Lives under the Claude config dir so it follows $CLAUDE_CONFIG_DIR test
// sandboxes without ceremony.
func CachePath(claudeConfigDir string) string {
	return filepath.Join(claudeConfigDir, "ccmcp", "discovery-cache.json")
}

// LoadCache reads the cached discovery result, if present and parseable. The
// returned bool reports whether the cache is fresh (FetchedAt < TTL ago); the
// caller decides whether to use a stale cache as offline fallback.
func LoadCache(path string) (*DiscoveryResult, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var r DiscoveryResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, false, err
	}
	fresh := time.Since(r.FetchedAt) < CacheTTL
	return &r, fresh, nil
}

// SaveCache writes result atomically to path. Errors during cache persistence
// are non-fatal for callers - a working session is more valuable than a
// pristine cache.
func SaveCache(path string, r *DiscoveryResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WithinGrace reports whether the cache is recent enough to serve as offline
// fallback even when fresh-TTL has elapsed.
func WithinGrace(r *DiscoveryResult) bool {
	if r == nil {
		return false
	}
	return time.Since(r.FetchedAt) < CacheGrace
}
