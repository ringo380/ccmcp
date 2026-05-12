package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Source is one origin from which RemoteMarketplaces can be enumerated. Each
// source's Fetch is run concurrently by Discover() under a per-source timeout.
// Implementations MUST be cheap to construct and safe to call from multiple
// goroutines (no shared mutable state).
type Source interface {
	// ID is a stable, human-readable identifier used in DiscoveryResult.Errors
	// and as the Origin tag on the surfaced entries.
	ID() string
	// Fetch returns the marketplaces this source surfaces. A non-nil error is
	// recorded in DiscoveryResult.Errors but never aborts the merge.
	Fetch(ctx context.Context, c *http.Client) ([]RemoteMarketplace, error)
}

// registryFile is the on-disk shape shared by the embedded registry and any
// user-supplied registry URL. Keeping it permissive (extra fields ignored)
// means we can add fields without breaking older clients.
type registryFile struct {
	Version      int                 `json:"version"`
	Marketplaces []RemoteMarketplace `json:"marketplaces"`
}

// ---- embedded registry source ----------------------------------------------

type embeddedSource struct{}

// EmbeddedSource returns the curated, ccmcp-bundled registry source. It is
// always-on and never fails (the bytes are baked into the binary).
func EmbeddedSource() Source { return embeddedSource{} }

func (embeddedSource) ID() string { return "embedded" }

func (embeddedSource) Fetch(_ context.Context, _ *http.Client) ([]RemoteMarketplace, error) {
	var rf registryFile
	if err := json.Unmarshal(registryBytes, &rf); err != nil {
		return nil, fmt.Errorf("parse embedded registry: %w", err)
	}
	out := make([]RemoteMarketplace, 0, len(rf.Marketplaces))
	for _, mp := range rf.Marketplaces {
		mp.Origin = "embedded"
		out = append(out, mp)
	}
	return out, nil
}

// ---- user URL source -------------------------------------------------------

type userURLSource struct{ url string }

// UserURLSource fetches a registry JSON file from a user-provided URL. The
// expected schema matches the embedded registry shape.
func UserURLSource(url string) Source { return userURLSource{url: url} }

func (s userURLSource) ID() string { return "user:" + s.url }

func (s userURLSource) Fetch(ctx context.Context, c *http.Client) ([]RemoteMarketplace, error) {
	body, err := getJSON(ctx, c, s.url)
	if err != nil {
		return nil, err
	}
	var rf registryFile
	if err := json.Unmarshal(body, &rf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.url, err)
	}
	out := make([]RemoteMarketplace, 0, len(rf.Marketplaces))
	for _, mp := range rf.Marketplaces {
		mp.Origin = s.ID()
		out = append(out, mp)
	}
	return out, nil
}

// ---- Anthropic source ------------------------------------------------------

// anthropicSource fetches an Anthropic-published registry, if one exists at
// the configured endpoint. The endpoint is intentionally a constant the build
// can update without changing user-visible behavior; a 404 is treated as "no
// registry yet" and silently returns an empty slice (no error recorded).
type anthropicSource struct {
	endpoint string
}

// AnthropicSource returns a source that probes the Anthropic-published
// registry. When endpoint is empty the source becomes a no-op — useful for
// disabling at build time without surgery elsewhere.
func AnthropicSource(endpoint string) Source { return anthropicSource{endpoint: endpoint} }

// AnthropicEndpoint is the canonical URL the build expects an Anthropic
// registry to live at. It may not exist yet — the source treats 404 as
// "no registry" and surfaces no rows. Override at runtime via the user
// settings discovery list when Anthropic publishes a final URL.
const AnthropicEndpoint = "https://docs.claude.com/.well-known/claude-code-marketplaces.json"

func (s anthropicSource) ID() string { return "anthropic" }

func (s anthropicSource) Fetch(ctx context.Context, c *http.Client) ([]RemoteMarketplace, error) {
	if s.endpoint == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic registry: HTTP %d", resp.StatusCode)
	}
	// docs.claude.com serves an HTML SPA at every path (including unknown
	// .well-known URLs that have no JSON behind them) — treat non-JSON 200
	// responses as "no registry yet" rather than surfacing a parse error.
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(strings.ToLower(ct), "json") {
		return nil, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var rf registryFile
	if err := json.Unmarshal(body, &rf); err != nil {
		return nil, nil
	}
	out := make([]RemoteMarketplace, 0, len(rf.Marketplaces))
	for _, mp := range rf.Marketplaces {
		mp.Origin = "anthropic"
		out = append(out, mp)
	}
	return out, nil
}

// ---- awesome-list source ---------------------------------------------------

// awesomeListSource scrapes a curated GitHub README for github.com/<owner>/<repo>
// links and surfaces each unique target as a candidate marketplace. The README
// is fetched from raw.githubusercontent.com (no rate limit) — never from the
// API.
type awesomeListSource struct {
	owner, repo, branch, path string
}

// AwesomeListSource returns a source that scrapes a curated GitHub README. If
// branch is empty, "HEAD" is used; if path is empty, "README.md" is used.
func AwesomeListSource(owner, repo, branch, path string) Source {
	if branch == "" {
		branch = "HEAD"
	}
	if path == "" {
		path = "README.md"
	}
	return awesomeListSource{owner: owner, repo: repo, branch: branch, path: path}
}

func (s awesomeListSource) ID() string {
	return fmt.Sprintf("awesome-list:%s/%s", s.owner, s.repo)
}

func (s awesomeListSource) Fetch(ctx context.Context, c *http.Client) ([]RemoteMarketplace, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", s.owner, s.repo, s.branch, s.path)
	body, err := getJSON(ctx, c, url) // any non-2xx surfaces here
	if err != nil {
		return nil, err
	}
	repos := ExtractGitHubRepos(string(body))
	out := make([]RemoteMarketplace, 0, len(repos))
	for _, r := range repos {
		// Skip the source repo itself.
		if strings.EqualFold(r.Owner, s.owner) && strings.EqualFold(r.Repo, s.repo) {
			continue
		}
		out = append(out, RemoteMarketplace{
			Name:        r.Owner + "-" + r.Repo,
			Description: r.Hint,
			Source:      "github",
			Repo:        r.Owner + "/" + r.Repo,
			Origin:      s.ID(),
		})
	}
	return out, nil
}

// ---- shared HTTP helper ----------------------------------------------------

// getJSON performs a GET and returns the body bytes for any 2xx; non-2xx
// surfaces a typed error. Used by both registry-style and README-style fetches
// (the latter is plain text but the helper makes no assumptions about the
// content type).
func getJSON(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
