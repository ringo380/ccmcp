package discovery_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ringo380/ccmcp/internal/discovery"
)

type fakeSource struct {
	id   string
	rows []discovery.RemoteMarketplace
	err  error
}

func (f fakeSource) ID() string { return f.id }
func (f fakeSource) Fetch(_ context.Context, _ *http.Client) ([]discovery.RemoteMarketplace, error) {
	return f.rows, f.err
}

func TestDiscoverMergesAndDedupes(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "cache.json")

	a := fakeSource{id: "a", rows: []discovery.RemoteMarketplace{{Name: "x", Source: "github", Repo: "owner/x"}}}
	b := fakeSource{id: "b", rows: []discovery.RemoteMarketplace{
		{Name: "x", Source: "github", Repo: "Owner/X", Description: "from b"},
		{Name: "y", Source: "github", Repo: "owner/y"},
	}}

	res, err := discovery.Discover(context.Background(), discovery.Options{
		Sources:   []discovery.Source{a, b},
		CachePath: cache,
		Refresh:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Marketplaces) != 2 {
		t.Fatalf("expected 2 deduped rows, got %d: %+v", len(res.Marketplaces), res.Marketplaces)
	}
	for _, mp := range res.Marketplaces {
		if mp.Name == "x" && mp.Description != "from b" {
			t.Errorf("merge should fill description from b, got %q", mp.Description)
		}
	}
}

func TestDiscoverPartialFailure(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "cache.json")

	good := fakeSource{id: "good", rows: []discovery.RemoteMarketplace{{Name: "x", Source: "github", Repo: "o/x"}}}
	bad := fakeSource{id: "bad", err: errors.New("server fire")}

	res, err := discovery.Discover(context.Background(), discovery.Options{
		Sources:   []discovery.Source{good, bad},
		CachePath: cache,
		Refresh:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Marketplaces) != 1 {
		t.Fatalf("expected 1 row from good source, got %d", len(res.Marketplaces))
	}
	if got := res.Errors["bad"]; got == "" {
		t.Fatalf("expected error from bad source recorded, got: %+v", res.Errors)
	}
}

func TestDiscoverOfflineFallback(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "cache.json")
	// Seed cache with rows from yesterday.
	seed := &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{{Name: "from-cache", Source: "github", Repo: "o/x"}},
		FetchedAt:    time.Now().Add(-24 * time.Hour),
	}
	if err := discovery.SaveCache(cache, seed); err != nil {
		t.Fatal(err)
	}

	bad := fakeSource{id: "bad", err: errors.New("offline")}
	res, err := discovery.Discover(context.Background(), discovery.Options{
		Sources:   []discovery.Source{bad},
		CachePath: cache,
		Refresh:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromCache {
		t.Fatal("expected FromCache=true when all sources fail")
	}
	if len(res.Marketplaces) != 1 || res.Marketplaces[0].Name != "from-cache" {
		t.Fatalf("expected cached row, got %+v", res.Marketplaces)
	}
}

func TestDiscoverFreshCacheShortCircuits(t *testing.T) {
	tmp := t.TempDir()
	cache := filepath.Join(tmp, "cache.json")
	seed := &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{{Name: "cached"}},
		FetchedAt:    time.Now(),
	}
	if err := discovery.SaveCache(cache, seed); err != nil {
		t.Fatal(err)
	}

	called := 0
	src := fakeSourceCounting{count: &called, rows: []discovery.RemoteMarketplace{{Name: "fresh"}}}
	res, err := discovery.Discover(context.Background(), discovery.Options{
		Sources:   []discovery.Source{src},
		CachePath: cache,
		Refresh:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Errorf("expected 0 source fetches with fresh cache, got %d", called)
	}
	if !res.FromCache || len(res.Marketplaces) != 1 || res.Marketplaces[0].Name != "cached" {
		t.Fatalf("expected cached result, got %+v", res)
	}
}

type fakeSourceCounting struct {
	count *int
	rows  []discovery.RemoteMarketplace
}

func (f fakeSourceCounting) ID() string { return "counting" }
func (f fakeSourceCounting) Fetch(_ context.Context, _ *http.Client) ([]discovery.RemoteMarketplace, error) {
	*f.count++
	return f.rows, nil
}
