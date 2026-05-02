// Package install fetches plugin source code into the Claude Code cache directory
// and keeps installed_plugins.json + enabledPlugins in sync.
//
// Four marketplace source types are supported:
//
//  1. bare string  — "./plugins/foo": path inside the marketplace repo itself.
//                    Plugin files are already on disk at marketplaces/<mkt>/plugins/foo.
//  2. "url"        — {source:"url", url, sha?}: full-repo clone; optional sha pin.
//  3. "git-subdir" — {source:"git-subdir", url, path, ref?, sha?}: clone then copy subdir.
//  4. "github"     — {source:"github", repo, ref?}: same as url but repo-shorthand.
//
// The installer writes to ~/.claude/plugins/cache/<mkt>/<plugin>/<version>/ and
// records gitCommitSha + installPath in installed_plugins.json so that Claude Code's
// loader treats the result as a first-class install.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/paths"
)

// Request is a single install intent resolved from a marketplace entry.
type Request struct {
	Marketplace string // e.g. "claude-plugins-official"
	Name        string // plugin name (without @marketplace)
	Source      any    // the raw "source" field from marketplace.json
}

// Result describes what was installed. The caller decides whether to flip enabledPlugins.
type Result struct {
	QualifiedID  string // name@marketplace
	InstallPath  string // cache/<mkt>/<name>/<version>
	Version      string // sha or "unknown"
	GitCommitSha string
}

// MarketplaceManifest is a trimmed view of .claude-plugin/marketplace.json.
type MarketplaceManifest struct {
	Name    string              `json:"name"`
	Plugins []MarketplacePlugin `json:"plugins"`
}

// MarketplacePlugin keeps "source" as json.RawMessage because it's polymorphic
// (can be a bare string OR an object).
type MarketplacePlugin struct {
	Name   string          `json:"name"`
	Source json.RawMessage `json:"source"`
}

// LoadMarketplace reads a marketplace manifest from <marketplaces-dir>/<name>/.claude-plugin/marketplace.json.
func LoadMarketplace(paths paths.Paths, marketplace string) (*MarketplaceManifest, string, error) {
	dir := filepath.Join(paths.PluginsDir, "marketplaces", marketplace)
	manifest := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	b, err := os.ReadFile(manifest)
	if err != nil {
		return nil, dir, fmt.Errorf("marketplace %q not fetched (no %s); clone it first or add it with `ccmcp marketplace add`: %w", marketplace, manifest, err)
	}
	var m MarketplaceManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, dir, fmt.Errorf("parse %s: %w", manifest, err)
	}
	return &m, dir, nil
}

// FindPlugin returns the plugin entry with the given name, or an error listing near-matches.
func (m *MarketplaceManifest) FindPlugin(name string) (MarketplacePlugin, error) {
	for _, p := range m.Plugins {
		if p.Name == name {
			return p, nil
		}
	}
	// Collect near-matches to help the user
	var hits []string
	lc := strings.ToLower(name)
	for _, p := range m.Plugins {
		if strings.Contains(strings.ToLower(p.Name), lc) {
			hits = append(hits, p.Name)
			if len(hits) >= 5 {
				break
			}
		}
	}
	if len(hits) > 0 {
		return MarketplacePlugin{}, fmt.Errorf("plugin %q not found in marketplace; close matches: %s", name, strings.Join(hits, ", "))
	}
	return MarketplacePlugin{}, fmt.Errorf("plugin %q not found in marketplace", name)
}

// Install fetches the plugin source according to its marketplace entry and returns
// enough metadata to update installed_plugins.json.
func Install(p paths.Paths, marketplace, pluginName string) (*Result, error) {
	m, marketplaceDir, err := LoadMarketplace(p, marketplace)
	if err != nil {
		return nil, err
	}
	entry, err := m.FindPlugin(pluginName)
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Join(p.PluginsDir, "cache", marketplace, pluginName)
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return nil, err
	}
	r := &Result{QualifiedID: pluginName + "@" + marketplace}

	// Try bare string source first
	var asString string
	if err := json.Unmarshal(entry.Source, &asString); err == nil && asString != "" {
		return installBareString(p, marketplaceDir, asString, cacheRoot, r)
	}
	// Otherwise it must be an object
	var obj map[string]any
	if err := json.Unmarshal(entry.Source, &obj); err != nil {
		return nil, fmt.Errorf("%s: source field is neither a string nor an object", pluginName)
	}
	sourceKind, _ := obj["source"].(string)
	switch sourceKind {
	case "url":
		return installURL(obj, cacheRoot, r)
	case "git-subdir":
		return installGitSubdir(obj, cacheRoot, r)
	case "github":
		return installGithub(obj, cacheRoot, r)
	default:
		return nil, fmt.Errorf("%s: unknown source kind %q (known: url, git-subdir, github, bare-string)", pluginName, sourceKind)
	}
}

// installBareString copies (or symlinks) the subdir of the already-cloned marketplace repo.
// Path containment is checked with filepath.Rel so sibling-directory prefix collisions
// (e.g. marketplaceDir="/a/b/foo" vs src="/a/b/foo-evil") can't slip past a naïve
// strings.HasPrefix — a malicious marketplace.json with a crafted "source" field would
// otherwise escape the intended root.
func installBareString(_ paths.Paths, marketplaceDir, relPath, cacheRoot string, r *Result) (*Result, error) {
	src := filepath.Join(marketplaceDir, relPath)
	if !withinDir(src, marketplaceDir) {
		return nil, fmt.Errorf("refusing to follow plugin path %q outside marketplace root", relPath)
	}
	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("plugin source dir missing: %s", src)
	}
	sha, _ := gitHeadSha(marketplaceDir)
	version := sha
	if version == "" {
		version = "unknown"
	}
	dst := filepath.Join(cacheRoot, version)
	if err := copyTree(src, dst); err != nil {
		return nil, err
	}
	r.InstallPath = dst
	r.Version = version
	r.GitCommitSha = sha
	return r, nil
}

// withinDir reports whether `candidate` resolves to the same path as, or a path
// nested inside, `root`. Uses filepath.Rel so sibling directories with a common
// prefix (foo/foo-evil) are correctly rejected.
func withinDir(candidate, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func installURL(obj map[string]any, cacheRoot string, r *Result) (*Result, error) {
	url, _ := obj["url"].(string)
	sha, _ := obj["sha"].(string)
	if url == "" {
		return nil, fmt.Errorf("url source: missing url")
	}
	version := sha
	if version == "" {
		version = "unknown"
	}
	dst := filepath.Join(cacheRoot, version)
	if err := gitClone(url, dst); err != nil {
		return nil, err
	}
	if sha != "" {
		if err := gitCheckout(dst, sha); err != nil {
			return nil, fmt.Errorf("checkout %s: %w", sha, err)
		}
	}
	head, _ := gitHeadSha(dst)
	r.InstallPath = dst
	r.Version = version
	r.GitCommitSha = head
	return r, nil
}

func installGitSubdir(obj map[string]any, cacheRoot string, r *Result) (*Result, error) {
	url, _ := obj["url"].(string)
	subpath, _ := obj["path"].(string)
	ref, _ := obj["ref"].(string)
	sha, _ := obj["sha"].(string)
	if url == "" || subpath == "" {
		return nil, fmt.Errorf("git-subdir source: missing url or path")
	}
	version := sha
	if version == "" {
		version = "unknown"
	}
	dst := filepath.Join(cacheRoot, version)
	// Clone into a temp directory, then copy the subdir into dst.
	tmp, err := os.MkdirTemp("", "ccmcp-clone-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := gitClone(url, tmp); err != nil {
		return nil, err
	}
	switch {
	case sha != "":
		if err := gitCheckout(tmp, sha); err != nil {
			return nil, err
		}
	case ref != "":
		if err := gitCheckout(tmp, ref); err != nil {
			return nil, err
		}
	}
	srcSub := filepath.Join(tmp, subpath)
	if _, err := os.Stat(srcSub); err != nil {
		return nil, fmt.Errorf("subpath %q not found in cloned repo", subpath)
	}
	if err := copyTree(srcSub, dst); err != nil {
		return nil, err
	}
	head, _ := gitHeadSha(tmp)
	r.InstallPath = dst
	r.Version = version
	r.GitCommitSha = head
	return r, nil
}

func installGithub(obj map[string]any, cacheRoot string, r *Result) (*Result, error) {
	repo, _ := obj["repo"].(string)
	ref, _ := obj["ref"].(string)
	if repo == "" {
		return nil, fmt.Errorf("github source: missing repo")
	}
	url := fmt.Sprintf("https://github.com/%s.git", repo)
	// Version directory: prefer the explicit ref; if absent, clone into a temp dir,
	// resolve HEAD, then move into a sha-named dir. Avoids the "unknown" dir collision
	// that previously caused repeated reinstalls of the same plugin to land on top of
	// each other with stale gitCommitSha metadata.
	if ref == "" {
		tmp, err := os.MkdirTemp("", "ccmcp-github-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		if err := gitClone(url, tmp); err != nil {
			return nil, err
		}
		head, _ := gitHeadSha(tmp)
		version := head
		if version == "" {
			version = "unknown"
		}
		dst := filepath.Join(cacheRoot, version)
		if err := copyTree(tmp, dst); err != nil {
			return nil, err
		}
		r.InstallPath = dst
		r.Version = version
		r.GitCommitSha = head
		return r, nil
	}
	version := ref
	dst := filepath.Join(cacheRoot, version)
	if err := gitClone(url, dst); err != nil {
		return nil, err
	}
	if ref != "" {
		if err := gitCheckout(dst, ref); err != nil {
			return nil, err
		}
	}
	head, _ := gitHeadSha(dst)
	r.InstallPath = dst
	r.Version = version
	r.GitCommitSha = head
	return r, nil
}

// --- git helpers -----------------------------------------------------------

func gitClone(url, dst string) error {
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		// already cloned — fetch to refresh
		cmd := exec.Command("git", "-C", dst, "fetch", "--tags", "--quiet")
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--quiet", url, dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", url, err)
	}
	return nil
}

func gitCheckout(repo, ref string) error {
	cmd := exec.Command("git", "-C", repo, "checkout", "--quiet", ref)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitHeadSha(repo string) (string, error) {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// copyTree copies src into dst, recursively. Skips .git directories so cache entries
// stay lean. dst is wiped if it already exists (reinstall path).
func copyTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(path, target, info.Mode())
		}
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// ListLocalMarketplaces returns the names of marketplace directories that have been
// cloned (i.e. contain a .git subdirectory) under pluginsDir/marketplaces/.
func ListLocalMarketplaces(p paths.Paths) ([]string, error) {
	dir := filepath.Join(p.PluginsDir, "marketplaces")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), ".git")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// MarketplaceDir returns the on-disk path where a marketplace clone lives (whether or
// not it currently exists).
func MarketplaceDir(p paths.Paths, name string) string {
	return filepath.Join(p.PluginsDir, "marketplaces", name)
}

// IsMarketplaceCloned reports whether <pluginsDir>/marketplaces/<name>/.git exists.
func IsMarketplaceCloned(p paths.Paths, name string) bool {
	_, err := os.Stat(filepath.Join(MarketplaceDir(p, name), ".git"))
	return err == nil
}

// CloneMarketplace clones a marketplace (github/git/local) into pluginsDir/marketplaces/<name>.
// For "local" sources, no clone happens — the directory is expected to already exist (or be
// symlinked) by the user. Returns nil when the clone already exists; callers needing a refresh
// should use UpdateMarketplace.
func CloneMarketplace(p paths.Paths, mp config.Marketplace) error {
	if mp.Name == "" {
		return fmt.Errorf("marketplace name required")
	}
	dst := MarketplaceDir(p, mp.Name)
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		return nil
	}
	switch mp.SourceType {
	case "github":
		if mp.Repo == "" {
			return fmt.Errorf("github source: missing repo")
		}
		url := fmt.Sprintf("https://github.com/%s.git", mp.Repo)
		return gitClone(url, dst)
	case "git":
		if mp.Repo == "" {
			return fmt.Errorf("git source: missing repo URL")
		}
		return gitClone(mp.Repo, dst)
	case "local":
		if mp.Path == "" {
			return fmt.Errorf("local source: missing path")
		}
		if _, err := os.Stat(mp.Path); err != nil {
			return fmt.Errorf("local marketplace path %s: %w", mp.Path, err)
		}
		// Ensure the parent dir exists; do nothing else — Claude Code expects local
		// marketplaces to be referenced in-place via settings, not copied.
		return os.MkdirAll(filepath.Dir(dst), 0o755)
	default:
		return fmt.Errorf("unknown source type %q (use github|git|local)", mp.SourceType)
	}
}

// AddMarketplace adds an entry to extraKnownMarketplaces and (for github/git source types)
// clones the marketplace into pluginsDir/marketplaces/<name>. Caller is responsible for
// Backup() + Save() afterwards.
func AddMarketplace(p paths.Paths, settings *config.Settings, mp config.Marketplace) error {
	if err := settings.AddMarketplace(mp); err != nil {
		return err
	}
	return CloneMarketplace(p, mp)
}

// RemoveMarketplace removes a marketplace entry from extraKnownMarketplaces and (when
// purgeClone is true) deletes pluginsDir/marketplaces/<name>. Returns an error if the
// marketplace is referenced by an installed plugin (caller should warn / require purge).
// Caller is responsible for Backup() + Save() afterwards.
func RemoveMarketplace(p paths.Paths, settings *config.Settings, installed *config.InstalledPlugins, name string, purgeClone bool) error {
	for _, ip := range installed.List() {
		_, mkt := config.ParsePluginID(ip.ID)
		if mkt == name {
			return fmt.Errorf("marketplace %q is still referenced by installed plugin %q; uninstall the plugin first", name, ip.ID)
		}
	}
	if !settings.RemoveMarketplace(name) {
		return fmt.Errorf("marketplace %q not found in extraKnownMarketplaces", name)
	}
	if purgeClone {
		_ = os.RemoveAll(MarketplaceDir(p, name))
	}
	return nil
}

// LocalMarketplaceHead returns the SHA of HEAD in the cloned marketplace directory,
// empty string if the marketplace is not cloned or git fails.
func LocalMarketplaceHead(p paths.Paths, name string) string {
	dir := MarketplaceDir(p, name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	sha, _ := gitHeadSha(dir)
	return sha
}

// RemoteMarketplaceHead runs `git ls-remote <origin> HEAD` for the marketplace's remote
// and returns the upstream HEAD sha. Returns ("", err) when git fails or no remote is
// configured (e.g. local-source marketplaces).
func RemoteMarketplaceHead(p paths.Paths, name string) (string, error) {
	dir := MarketplaceDir(p, name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("marketplace %q not cloned", name)
	}
	cmd := exec.Command("git", "-C", dir, "ls-remote", "--quiet", "origin", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git ls-remote: %w", err)
	}
	// Output: "<sha>\tHEAD"
	line := strings.TrimSpace(out.String())
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty ls-remote output")
	}
	return fields[0], nil
}

// RemoteSourceHead returns the upstream HEAD sha for a generic git URL (used to detect
// updates for plugins whose source is a separate git/url/github source rather than the
// marketplace itself). Empty string when the URL is unreachable.
func RemoteSourceHead(url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("empty url")
	}
	cmd := exec.Command("git", "ls-remote", "--quiet", url, "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	line := strings.TrimSpace(out.String())
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty ls-remote output")
	}
	return fields[0], nil
}

// PluginSourceURL inspects a marketplace plugin entry and returns the upstream git URL
// for its source (only meaningful for url/git-subdir/github sources). Empty string for
// bare-string sources — those track the marketplace itself.
func PluginSourceURL(entry MarketplacePlugin) string {
	var asString string
	if err := json.Unmarshal(entry.Source, &asString); err == nil && asString != "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(entry.Source, &obj); err != nil {
		return ""
	}
	switch obj["source"] {
	case "url":
		s, _ := obj["url"].(string)
		return s
	case "git-subdir":
		s, _ := obj["url"].(string)
		return s
	case "github":
		repo, _ := obj["repo"].(string)
		if repo == "" {
			return ""
		}
		return fmt.Sprintf("https://github.com/%s.git", repo)
	}
	return ""
}

// UpdateMarketplace runs `git pull --ff-only` in the cloned marketplace directory so
// that subsequent Install calls see the latest plugin sources. Returns an error if the
// marketplace directory has not been cloned (no .git).
func UpdateMarketplace(p paths.Paths, name string) error {
	dir := filepath.Join(p.PluginsDir, "marketplaces", name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return fmt.Errorf("marketplace %q is not a git clone at %s; clone it first", name, dir)
	}
	cmd := exec.Command("git", "-C", dir, "pull", "--quiet", "--ff-only")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git pull for marketplace %q: %w", name, err)
	}
	return nil
}

// UpdateInstall updates an existing installed_plugins.json entry after a re-fetch.
// Unlike RegisterInstall it:
//   - Preserves the original installedAt timestamp
//   - Does NOT touch enabledPlugins (the user's enable/disable choice is unchanged)
//   - Removes oldInstallPath from disk when it differs from r.InstallPath (GC)
//
// Caller is responsible for Save() + Backup() afterwards.
func UpdateInstall(installed *config.InstalledPlugins, r *Result, oldInstallPath string) {
	plugins, _ := installed.Raw["plugins"].(map[string]any)
	if plugins == nil {
		plugins = map[string]any{}
	}
	// Preserve installedAt from existing entry.
	var installedAt string
	if existing, ok := plugins[r.QualifiedID]; ok {
		if arr, ok := existing.([]any); ok && len(arr) > 0 {
			if entry, ok := arr[0].(map[string]any); ok {
				installedAt, _ = entry["installedAt"].(string)
			}
		}
	}
	if installedAt == "" {
		installedAt = time.Now().UTC().Format(time.RFC3339)
	}
	entry := map[string]any{
		"scope":        "user",
		"installPath":  r.InstallPath,
		"version":      r.Version,
		"installedAt":  installedAt,
		"lastUpdated":  time.Now().UTC().Format(time.RFC3339),
		"gitCommitSha": r.GitCommitSha,
	}
	plugins[r.QualifiedID] = []any{entry}
	installed.Raw["plugins"] = plugins

	// GC: remove the old versioned directory if it has changed.
	if oldInstallPath != "" && oldInstallPath != r.InstallPath {
		_ = os.RemoveAll(oldInstallPath)
	}
}

// RegisterInstall patches installed_plugins.json with the new entry and flips
// enabledPlugins[id] = true. Caller is responsible for Save() + Backup() afterwards.
func RegisterInstall(settings *config.Settings, installed *config.InstalledPlugins, r *Result) {
	plugins, _ := installed.Raw["plugins"].(map[string]any)
	if plugins == nil {
		plugins = map[string]any{}
	}
	entry := map[string]any{
		"scope":        "user",
		"installPath":  r.InstallPath,
		"version":      r.Version,
		"installedAt":  time.Now().UTC().Format(time.RFC3339),
		"lastUpdated":  time.Now().UTC().Format(time.RFC3339),
		"gitCommitSha": r.GitCommitSha,
	}
	plugins[r.QualifiedID] = []any{entry}
	installed.Raw["plugins"] = plugins

	settings.SetPluginEnabled(r.QualifiedID, true)
}
