package discovery_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ringo380/ccmcp/internal/discovery"
)

func TestCacheRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cache.json")

	in := &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{{Name: "x", Source: "github", Repo: "a/b"}},
		FetchedAt:    time.Now(),
	}
	if err := discovery.SaveCache(path, in); err != nil {
		t.Fatal(err)
	}

	out, fresh, err := discovery.LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("just-written cache should be fresh")
	}
	if len(out.Marketplaces) != 1 || out.Marketplaces[0].Name != "x" {
		t.Fatalf("round-trip lost data: %+v", out)
	}
}

func TestCacheStaleAfterTTL(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cache.json")
	in := &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{{Name: "x"}},
		FetchedAt:    time.Now().Add(-discovery.CacheTTL - time.Hour),
	}
	if err := discovery.SaveCache(path, in); err != nil {
		t.Fatal(err)
	}
	out, fresh, err := discovery.LoadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("expected stale cache, got fresh")
	}
	if !discovery.WithinGrace(out) {
		t.Fatal("cache slightly past TTL should still be within grace window")
	}
}

func TestCacheMissingIsNotError(t *testing.T) {
	out, fresh, err := discovery.LoadCache(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if out != nil || fresh {
		t.Fatalf("missing file should yield nil/false, got %+v fresh=%v", out, fresh)
	}
}

func TestWithinGraceNil(t *testing.T) {
	if discovery.WithinGrace(nil) {
		t.Fatal("nil cache should not be within grace")
	}
}
