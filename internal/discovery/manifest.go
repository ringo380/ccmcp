package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ringo380/ccmcp/internal/install"
)

// FetchManifest retrieves the marketplace.json for mp without cloning the
// repo. When mp.ManifestURL is set it's tried first; otherwise (or on 404)
// the function falls back to constructed raw.githubusercontent.com URLs at
// HEAD, main, and master, in that order.
//
// The returned manifest reuses install.MarketplaceManifest so downstream
// install/preview code paths are identical.
func FetchManifest(ctx context.Context, c *http.Client, mp RemoteMarketplace) (*install.MarketplaceManifest, error) {
	urls := manifestCandidates(mp)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no manifest URL available for %s", mp.Name)
	}

	var lastErr error
	for _, u := range urls {
		body, err := getJSON(ctx, c, u)
		if err != nil {
			lastErr = err
			continue
		}
		var m install.MarketplaceManifest
		if err := json.Unmarshal(body, &m); err != nil {
			lastErr = fmt.Errorf("parse %s: %w", u, err)
			continue
		}
		return &m, nil
	}
	return nil, lastErr
}

// manifestCandidates returns the ordered list of URLs to try when fetching a
// marketplace's manifest over HTTP.
func manifestCandidates(mp RemoteMarketplace) []string {
	var urls []string
	if mp.ManifestURL != "" {
		urls = append(urls, mp.ManifestURL)
	}
	if mp.Source == "github" && mp.Repo != "" {
		branches := []string{"HEAD", "main", "master"}
		if mp.Branch != "" {
			branches = append([]string{mp.Branch}, branches...)
		}
		for _, b := range branches {
			urls = append(urls, fmt.Sprintf(
				"https://raw.githubusercontent.com/%s/%s/.claude-plugin/marketplace.json",
				mp.Repo, b,
			))
		}
	}
	return urls
}

// FetchManifestPlugins is a convenience wrapper that returns the plugins from
// FetchManifest as RemotePlugin values, preserving the polymorphic source as
// raw JSON (matching install.MarketplacePlugin).
func FetchManifestPlugins(ctx context.Context, c *http.Client, mp RemoteMarketplace) ([]RemotePlugin, error) {
	m, err := FetchManifest(ctx, c, mp)
	if err != nil {
		return nil, err
	}
	out := make([]RemotePlugin, 0, len(m.Plugins))
	for _, p := range m.Plugins {
		out = append(out, RemotePlugin{
			Name:   p.Name,
			Source: p.Source,
		})
	}
	return out, nil
}
