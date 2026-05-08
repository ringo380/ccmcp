package discovery

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options configures a Discover() run.
type Options struct {
	// Sources is the merged source list to fetch from. Order doesn't affect
	// the result; merging dedupes by repo identity.
	Sources []Source
	// CachePath is where the merged result is persisted. Empty disables
	// caching entirely.
	CachePath string
	// Refresh forces a network fetch even when the on-disk cache is fresh.
	Refresh bool
	// HTTPClient overrides the default client. The default uses a 10s timeout.
	HTTPClient *http.Client
	// PerSourceTimeout caps a single source's fetch. Default 10s.
	PerSourceTimeout time.Duration
	// TotalTimeout caps the whole discovery call. Default 30s.
	TotalTimeout time.Duration
	// UserAgent overrides the User-Agent header. Default "ccmcp-discovery".
	UserAgent string
}

// DefaultSources returns the always-on source list: the embedded curated
// registry, the Anthropic-published registry probe, and a small set of
// well-known awesome-list scrapers.
//
// Set CCMCP_DISCOVERY_OFFLINE=1 to restrict the default to the embedded
// source only — useful for hermetic tests and air-gapped use.
func DefaultSources() []Source {
	if os.Getenv("CCMCP_DISCOVERY_OFFLINE") != "" {
		return []Source{EmbeddedSource()}
	}
	return []Source{
		EmbeddedSource(),
		AnthropicSource(AnthropicEndpoint),
		AwesomeListSource("hesreallyhim", "awesome-claude-code", "", ""),
	}
}

// Discover concurrently fetches every configured source, merges the results
// (deduplicating by source/repo), and writes the merged result to cache.
//
// Network failures are recorded per-source in the returned DiscoveryResult's
// Errors map and never fail the whole call when at least one source produced
// rows or a usable cache is available.
func Discover(ctx context.Context, opts Options) (*DiscoveryResult, error) {
	// Cache shortcut: a fresh cache + no Refresh skips the whole network path.
	if !opts.Refresh && opts.CachePath != "" {
		if cached, fresh, err := LoadCache(opts.CachePath); err == nil && cached != nil && fresh {
			cached.FromCache = true
			return cached, nil
		}
	}

	if opts.PerSourceTimeout == 0 {
		opts.PerSourceTimeout = 10 * time.Second
	}
	if opts.TotalTimeout == 0 {
		opts.TotalTimeout = 30 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "ccmcp-discovery"
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: opts.PerSourceTimeout}
	}
	if len(opts.Sources) == 0 {
		opts.Sources = DefaultSources()
	}

	// Wrap the http client transport with a UA-injecting RoundTripper.
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	clientWithUA := &http.Client{
		Transport: uaTransport{base: client.Transport, ua: opts.UserAgent},
		Timeout:   client.Timeout,
	}

	totalCtx, cancel := context.WithTimeout(ctx, opts.TotalTimeout)
	defer cancel()

	type srcOut struct {
		id   string
		rows []RemoteMarketplace
		err  error
	}
	results := make(chan srcOut, len(opts.Sources))
	var wg sync.WaitGroup
	for _, s := range opts.Sources {
		wg.Add(1)
		s := s
		go func() {
			defer wg.Done()
			subCtx, subCancel := context.WithTimeout(totalCtx, opts.PerSourceTimeout)
			defer subCancel()
			rows, err := s.Fetch(subCtx, clientWithUA)
			results <- srcOut{id: s.ID(), rows: rows, err: err}
		}()
	}
	wg.Wait()
	close(results)

	merged := map[string]RemoteMarketplace{}
	errs := map[string]string{}
	for r := range results {
		if r.err != nil {
			errs[r.id] = r.err.Error()
		}
		for _, row := range r.rows {
			key := mergeKey(row)
			if existing, ok := merged[key]; ok {
				// Prefer richer descriptions and explicit ManifestURL when
				// merging duplicate entries from multiple sources.
				if existing.Description == "" && row.Description != "" {
					existing.Description = row.Description
				}
				if existing.ManifestURL == "" && row.ManifestURL != "" {
					existing.ManifestURL = row.ManifestURL
				}
				if existing.Stars == 0 && row.Stars > 0 {
					existing.Stars = row.Stars
				}
				existing.Origin = mergeOrigins(existing.Origin, row.Origin)
				merged[key] = existing
				continue
			}
			merged[key] = row
		}
	}

	out := make([]RemoteMarketplace, 0, len(merged))
	for _, v := range merged {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	result := &DiscoveryResult{
		Marketplaces: out,
		Errors:       errs,
		FetchedAt:    time.Now(),
	}

	// Offline fallback: if everything failed AND we have a cache within
	// grace, return the cache flagged as such.
	if len(out) == 0 && opts.CachePath != "" {
		if cached, _, err := LoadCache(opts.CachePath); err == nil && cached != nil && WithinGrace(cached) {
			cached.FromCache = true
			cached.Errors = errs
			return cached, nil
		}
	}

	// Persist only when we got at least one row OR no source produced an
	// error. Saving an empty result over a previously good cache when every
	// source transiently failed (e.g. the user lost network mid-flight) would
	// poison the next 6h until the next forced refresh.
	if opts.CachePath != "" && (len(out) > 0 || len(errs) == 0) {
		_ = SaveCache(opts.CachePath, result)
	}
	return result, nil
}

// mergeKey produces a stable identity for deduping discovery results across
// sources. Github source-types collapse on lowercased "owner/repo".
func mergeKey(mp RemoteMarketplace) string {
	if mp.Source == "github" && mp.Repo != "" {
		return "github:" + strings.ToLower(mp.Repo)
	}
	if mp.Source != "" && mp.Repo != "" {
		return mp.Source + ":" + mp.Repo
	}
	return mp.Source + ":" + mp.Name
}

// mergeOrigins concatenates origin tags so the UI can show every source that
// surfaced a given marketplace.
func mergeOrigins(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if strings.Contains(a, b) {
		return a
	}
	return a + "," + b
}

// NewHTTPClient returns an http.Client wrapped with the discovery UA-injecting
// transport, suitable for one-off manifest fetches outside the Discover()
// orchestrator. timeout==0 leaves the underlying client unbounded; the caller
// is expected to supply a context.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: uaTransport{base: http.DefaultTransport, ua: "ccmcp-discovery"},
		Timeout:   timeout,
	}
}

// uaTransport injects a User-Agent header on every outbound request. Avoids
// surprising 403s from raw.githubusercontent.com mirrors that reject empty UAs.
type uaTransport struct {
	base http.RoundTripper
	ua   string
}

func (t uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.ua)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
