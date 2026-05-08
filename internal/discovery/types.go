// Package discovery surfaces Claude Code marketplaces from authoritative
// online sources (Anthropic-curated lists, awesome-list-style README scraping,
// an embedded ccmcp registry, and user-supplied registry URLs), shows what
// each marketplace's plugins contribute, and flags conflicts against
// already-installed state before the user adopts one.
//
// The package is intentionally read-only: it never mutates ~/.claude.json,
// settings.json, or installed_plugins.json. Drilling into a remote plugin
// performs a shallow git clone into a preview cache (~/.claude/plugins/cache/
// _discovery/<owner>/<repo>/<sha>/) for inventory + conflict scanning. Calling
// `ccmcp marketplace add` or the existing TUI `add` flow is the only path
// that promotes a remote marketplace to an installed one.
package discovery

import (
	"encoding/json"
	"time"
)

// RemoteMarketplace describes a marketplace surfaced by a discovery source.
// It carries enough information to render a list row, fetch the manifest, and
// (later) add the marketplace via the existing install pipeline if the user
// chooses.
type RemoteMarketplace struct {
	// Name is the marketplace's preferred display name. Sources may set this
	// from explicit metadata or derive it from the repo slug.
	Name string `json:"name"`
	// Description is a one-line summary. May be empty for awesome-list scrapes.
	Description string `json:"description,omitempty"`
	// Source is one of "github" | "git" | "url" — matching the existing
	// Marketplace.SourceType vocabulary so the install path can adopt it
	// without translation.
	Source string `json:"source"`
	// Repo is the "owner/repo" form for github sources or the full clone URL
	// for git sources.
	Repo string `json:"repo,omitempty"`
	// Branch is an optional pinned ref. Empty means HEAD.
	Branch string `json:"branch,omitempty"`
	// Tags is a free-form classification; sources can populate from README
	// section names, registry tags, etc.
	Tags []string `json:"tags,omitempty"`
	// Stars is an optional GitHub star count when the source can cheaply
	// supply it. Zero means "unknown" — UIs should not render a "0 ★" pill.
	Stars int `json:"stars,omitempty"`
	// Origin records which discovery source surfaced this entry — useful for
	// debugging duplicates and for the TUI footer per-source error display.
	// Examples: "embedded", "user:https://example.com/registry.json",
	// "awesome-list:hesreallyhim/awesome-claude-code", "anthropic".
	Origin string `json:"origin,omitempty"`
	// ManifestURL is a hint at where to fetch the marketplace.json over HTTP
	// without cloning. When empty, callers fall back to constructed
	// raw.githubusercontent.com URLs.
	ManifestURL string `json:"manifestUrl,omitempty"`
}

// RemotePlugin is a plugin entry inside a fetched marketplace manifest. Its
// Source field is preserved as raw JSON because marketplace.json supports
// several polymorphic shapes (bare string, github, url, git-subdir) and the
// preview clone needs to dispatch on that shape directly.
type RemotePlugin struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Source      json.RawMessage `json:"source,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
}

// DiscoveryResult is the merged output of a Discover() call. Errors are
// retained per-source so partial failures don't lose successful results — the
// UI surfaces them in a footer.
type DiscoveryResult struct {
	Marketplaces []RemoteMarketplace `json:"marketplaces"`
	// Errors maps source ID → error.Error(). Keyed strings (not error values)
	// so the result round-trips through JSON cache without custom encoders.
	Errors    map[string]string `json:"errors,omitempty"`
	FetchedAt time.Time         `json:"fetchedAt"`
	// FromCache flags results returned from on-disk cache (TTL miss but inside
	// the offline-grace window). Useful for the TUI banner.
	FromCache bool `json:"-"`
}

// ConflictReport is the result of comparing a freshly cloned plugin's
// inventory against the user's currently-installed Claude Code state.
type ConflictReport struct {
	// MarketplaceNameClash is true when the remote marketplace name is
	// already known to settings.extraKnownMarketplaces.
	MarketplaceNameClash bool
	// PluginIDClash is true when "<plugin>@<marketplace>" is already in
	// installed_plugins.json (regardless of enabled state).
	PluginIDClash bool

	Skills     []Conflict
	Agents     []Conflict
	Commands   []Conflict
	MCPServers []Conflict
	Hooks      []Conflict
}

// Empty reports whether the report contains no conflicts of any kind.
func (r ConflictReport) Empty() bool {
	return !r.MarketplaceNameClash && !r.PluginIDClash &&
		len(r.Skills) == 0 && len(r.Agents) == 0 && len(r.Commands) == 0 &&
		len(r.MCPServers) == 0 && len(r.Hooks) == 0
}

// Total counts every conflict in the report (booleans count as 1 each).
func (r ConflictReport) Total() int {
	n := 0
	if r.MarketplaceNameClash {
		n++
	}
	if r.PluginIDClash {
		n++
	}
	n += len(r.Skills) + len(r.Agents) + len(r.Commands) + len(r.MCPServers) + len(r.Hooks)
	return n
}

// Conflict is a single name collision between a remote item and an installed one.
type Conflict struct {
	// Name is the colliding identifier (skill name, agent slug, command name,
	// MCP server key, or hook event name).
	Name string
	// ExistingSource describes where the existing item lives ("user", "project",
	// or "plugin:<plugin-id>"). Empty when the source can't be precisely
	// attributed.
	ExistingSource string
	// IncomingPath is the file inside the preview clone that introduces the
	// conflicting item — useful so the user can grep it.
	IncomingPath string
}
