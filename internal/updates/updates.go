// Package updates probes upstream sources to detect when a newer version of a
// marketplace, plugin, or MCP server is available. Results are cached in-process
// per session so the TUI doesn't re-probe on every render. All probes are best-effort:
// network/registry failures return empty results rather than errors, so the UI never
// blocks the main thread on a flaky network.
package updates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/paths"
)

// Status describes the result of an update probe for a single artifact.
type Status struct {
	// Local is the currently-installed version identifier (sha or semver).
	Local string
	// Remote is the latest available version identifier (sha or semver).
	Remote string
	// Outdated reports whether Remote != Local AND Remote is non-empty.
	Outdated bool
	// Err carries any error encountered. Probes set this for observability but
	// callers usually only render Outdated.
	Err error
	// CheckedAt is when this probe ran.
	CheckedAt time.Time
}

// Cache holds probe results keyed by ID. Safe for concurrent use.
type Cache struct {
	mu  sync.RWMutex
	mkt map[string]Status
	plg map[string]Status
	mcp map[string]Status
}

// NewCache returns an empty Cache.
func NewCache() *Cache {
	return &Cache{
		mkt: map[string]Status{},
		plg: map[string]Status{},
		mcp: map[string]Status{},
	}
}

// Marketplace returns the cached status for a marketplace name.
func (c *Cache) Marketplace(name string) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.mkt[name]
	return s, ok
}

// Plugin returns the cached status for a qualified plugin id.
func (c *Cache) Plugin(id string) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.plg[id]
	return s, ok
}

// MCP returns the cached status for an MCP server name.
func (c *Cache) MCP(name string) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.mcp[name]
	return s, ok
}

// PutMarketplace stores a marketplace status.
func (c *Cache) PutMarketplace(name string, s Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mkt[name] = s
}

// PutPlugin stores a plugin status.
func (c *Cache) PutPlugin(id string, s Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plg[id] = s
}

// PutMCP stores an MCP status.
func (c *Cache) PutMCP(name string, s Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mcp[name] = s
}

// InvalidatePlugin removes a plugin entry from the cache (used after a successful update).
func (c *Cache) InvalidatePlugin(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.plg, id)
}

// InvalidateMarketplace removes a marketplace entry from the cache.
func (c *Cache) InvalidateMarketplace(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.mkt, name)
}

// CountOutdated returns counts of outdated entries across all three categories.
func (c *Cache) CountOutdated() (mkt, plg, mcp int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, s := range c.mkt {
		if s.Outdated {
			mkt++
		}
	}
	for _, s := range c.plg {
		if s.Outdated {
			plg++
		}
	}
	for _, s := range c.mcp {
		if s.Outdated {
			mcp++
		}
	}
	return
}

// CheckMarketplace probes a single marketplace. Local-source marketplaces always return
// an empty Status (no upstream to check).
func CheckMarketplace(p paths.Paths, name string) Status {
	s := Status{CheckedAt: time.Now()}
	s.Local = install.LocalMarketplaceHead(p, name)
	remote, err := install.RemoteMarketplaceHead(p, name)
	if err != nil {
		s.Err = err
		return s
	}
	s.Remote = remote
	s.Outdated = remote != "" && s.Local != "" && remote != s.Local
	return s
}

// CheckPlugin probes a single plugin. The marketplace's RemoteHead may be passed in
// to avoid re-running git ls-remote when the plugin uses a bare-string source (i.e.
// it tracks the marketplace itself).
func CheckPlugin(p paths.Paths, ip config.InstalledPlugin, mktRemoteHead string) Status {
	s := Status{CheckedAt: time.Now(), Local: ip.GitCommitSha}
	name, mkt := config.ParsePluginID(ip.ID)
	if mkt == "" {
		return s
	}
	manifest, _, err := install.LoadMarketplace(p, mkt)
	if err != nil {
		s.Err = err
		return s
	}
	entry, err := manifest.FindPlugin(name)
	if err != nil {
		s.Err = err
		return s
	}
	srcURL := install.PluginSourceURL(entry)
	if srcURL == "" {
		// Bare-string source: tracks the marketplace itself.
		s.Remote = mktRemoteHead
	} else {
		remote, err := install.RemoteSourceHead(srcURL)
		if err != nil {
			s.Err = err
			return s
		}
		s.Remote = remote
	}
	s.Outdated = s.Remote != "" && s.Local != "" && s.Remote != s.Local
	return s
}

// MCPLauncher describes how to detect a versioned package launcher in an MCP command.
type MCPLauncher struct {
	Pkg     string // package name without version, e.g. "@modelcontextprotocol/server-foo"
	Version string // pinned version (empty = floats)
	Kind    string // "npm" | "pypi"
}

// DetectMCPLauncher inspects an MCP server command/args and returns the package and
// registry kind if it's a recognizable npx/uvx launcher. Returns zero MCPLauncher when
// no pattern matches (docker, custom binary, http, etc.).
func DetectMCPLauncher(command string, args []string) MCPLauncher {
	cmd := strings.TrimSpace(command)
	switch {
	case cmd == "npx", strings.HasSuffix(cmd, "/npx"):
		return scanNpxArgs(args)
	case cmd == "uvx", strings.HasSuffix(cmd, "/uvx"):
		return scanUvxArgs(args)
	case cmd == "uv", strings.HasSuffix(cmd, "/uv"):
		// Pattern: uv tool run <pkg>[==ver]  or  uv run <pkg>
		if len(args) >= 2 && (args[0] == "tool" && args[1] == "run") {
			return scanUvxArgs(args[2:])
		}
		if len(args) >= 1 && args[0] == "run" {
			return scanUvxArgs(args[1:])
		}
	}
	return MCPLauncher{}
}

func scanNpxArgs(args []string) MCPLauncher {
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		// First non-flag is the package spec.
		pkg, ver := splitPkgVersion(a)
		return MCPLauncher{Pkg: pkg, Version: ver, Kind: "npm"}
	}
	return MCPLauncher{}
}

func scanUvxArgs(args []string) MCPLauncher {
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		pkg, ver := splitPkgVersion(a)
		return MCPLauncher{Pkg: pkg, Version: ver, Kind: "pypi"}
	}
	return MCPLauncher{}
}

// splitPkgVersion splits "name@1.2.3" or "name==1.2.3" into name + version.
// For scoped npm packages ("@scope/name@1.2.3") only the *trailing* @ is treated as
// the version separator.
func splitPkgVersion(spec string) (pkg, ver string) {
	// Try "==" first (PyPI / pip syntax).
	if i := strings.Index(spec, "=="); i > 0 {
		return spec[:i], spec[i+2:]
	}
	// Last "@" not at index 0 is the version separator.
	at := strings.LastIndex(spec, "@")
	if at > 0 {
		return spec[:at], spec[at+1:]
	}
	return spec, ""
}

// Runner abstracts how external processes (git, npm) are invoked. The default uses
// os/exec; tests inject a stub.
type Runner interface {
	Run(cmd string, args ...string) (string, error)
	HTTPGet(url string) ([]byte, error)
}

type defaultRunner struct{}

func (defaultRunner) Run(cmd string, args ...string) (string, error) {
	c := exec.Command(cmd, args...)
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (defaultRunner) HTTPGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DefaultRunner is a process-and-network runner suitable for production use.
var DefaultRunner Runner = defaultRunner{}

// CheckMCPLauncher probes the registry for the latest version of an MCP launcher and
// compares it against the pinned version (if any). Floating versions ("@latest", no @)
// always return Outdated=false because there's nothing to compare against — the user
// already gets the latest on each launch.
func CheckMCPLauncher(r Runner, l MCPLauncher) Status {
	s := Status{CheckedAt: time.Now(), Local: l.Version}
	if r == nil {
		r = DefaultRunner
	}
	if l.Pkg == "" {
		return s
	}
	switch l.Kind {
	case "npm":
		out, err := r.Run("npm", "view", l.Pkg, "version")
		if err != nil {
			s.Err = err
			return s
		}
		s.Remote = strings.TrimSpace(out)
	case "pypi":
		body, err := r.HTTPGet(fmt.Sprintf("https://pypi.org/pypi/%s/json", l.Pkg))
		if err != nil {
			s.Err = err
			return s
		}
		var doc struct {
			Info struct {
				Version string `json:"version"`
			} `json:"info"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			s.Err = err
			return s
		}
		s.Remote = doc.Info.Version
	default:
		return s
	}
	if l.Version == "" || strings.EqualFold(l.Version, "latest") {
		// Nothing pinned; outdated is meaningless.
		return s
	}
	s.Outdated = s.Remote != "" && s.Remote != l.Version
	return s
}
