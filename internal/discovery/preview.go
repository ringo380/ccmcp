package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ringo380/ccmcp/internal/paths"
)

// PreviewResult points at a freshly cloned plugin directory ready for scanning.
// The directory is reused across calls when the resolved sha hasn't changed.
type PreviewResult struct {
	// Dir is the local path containing the plugin's contents (the root the
	// scanners walk).
	Dir string
	// Repo is the cloned upstream URL - useful for messages.
	Repo string
	// Sha is the resolved git commit sha of the clone, or empty when the
	// source isn't git-backed.
	Sha string
}

// shaRe matches a 40-char lowercase hex commit SHA, which `git clone --branch`
// rejects but `git fetch <sha>` accepts. Used to dispatch the clone strategy.
var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// pathSegRe restricts cache directory segments to a safe character set so
// untrusted registry input can't escape the preview cache via "..", absolute
// paths, or shell-special chars. Anything outside the set is replaced with "_"
// per-rune.
var pathSegRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// PreviewClone resolves a plugin's source field to an upstream repo, fetches
// it (if not already cached), and returns the directory ready for inventory
// scanning.
//
// Caching layout: <PluginsDir>/cache/_discovery/<owner>/<repo>/<sha>/. The
// resolved sha is determined upstream-first via `git ls-remote`, so different
// HEADs produce different cache directories and an existing dir is only
// reused when its sha matches what the upstream advertises today. When
// ls-remote fails (offline / private repo), the cache key falls back to a
// safe `unknown-<refSlug>` directory and the clone is still attempted.
func PreviewClone(ctx context.Context, p paths.Paths, mp RemoteMarketplace, plugin RemotePlugin) (*PreviewResult, error) {
	repoURL, subdir, ref, err := resolvePluginUpstream(mp, plugin)
	if err != nil {
		return nil, err
	}

	owner, repo := splitOwnerRepo(repoURL)
	owner = sanitizeSegment(owner)
	repo = sanitizeSegment(repo)
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("invalid repo URL %q (cannot derive cache path)", repoURL)
	}

	root := filepath.Join(p.PluginsDir, "cache", "_discovery", owner, repo)

	// Resolve the upstream sha for the requested ref so the cache dir is sha-
	// keyed. ls-remote is cheap (one HTTPS round-trip) and is the only way to
	// avoid reusing a stale clone when the branch tip moved.
	resolvedSha := remoteSha(ctx, repoURL, ref)
	cacheKey := resolvedSha
	if cacheKey == "" {
		cacheKey = "unknown-" + sanitizeSegment(ref)
		if cacheKey == "unknown-" {
			cacheKey = "unknown-HEAD"
		}
	}
	cloneDir := filepath.Join(root, cacheKey)

	// Reuse only when the dir already contains a working clone; otherwise
	// (re)clone. We don't trust a stale .git from a prior run with no sha
	// agreement.
	if _, statErr := os.Stat(filepath.Join(cloneDir, ".git")); statErr != nil {
		if err := os.MkdirAll(filepath.Dir(cloneDir), 0o755); err != nil {
			return nil, err
		}
		if err := shallowClone(ctx, repoURL, cloneDir, ref, resolvedSha); err != nil {
			return nil, err
		}
	}

	sha := readHeadSha(cloneDir)
	dir := cloneDir
	if subdir != "" {
		dir = filepath.Join(cloneDir, subdir)
		if _, err := os.Stat(dir); err != nil {
			return nil, fmt.Errorf("subdir %s not found in clone of %s", subdir, repoURL)
		}
	}

	return &PreviewResult{Dir: dir, Repo: repoURL, Sha: sha}, nil
}

// resolvePluginUpstream walks the polymorphic plugin.source field and returns
// (cloneURL, subdir, ref). For the bare-string case the marketplace's repo is
// the clone URL and the bare-string is the subdir.
func resolvePluginUpstream(mp RemoteMarketplace, plugin RemotePlugin) (cloneURL, subdir, ref string, err error) {
	// Default: clone the marketplace itself; let the bare-string carry subdir.
	if mp.Source == "github" && mp.Repo != "" {
		cloneURL = "https://github.com/" + mp.Repo
	} else if mp.Source == "git" && mp.Repo != "" {
		cloneURL = mp.Repo
	}

	if len(plugin.Source) == 0 {
		if cloneURL == "" {
			return "", "", "", fmt.Errorf("no upstream resolvable for plugin %q in marketplace %q", plugin.Name, mp.Name)
		}
		return cloneURL, "", "", nil
	}

	// Bare string source: relative path inside the marketplace repo.
	var s string
	if err := json.Unmarshal(plugin.Source, &s); err == nil && s != "" {
		if cloneURL == "" {
			return "", "", "", fmt.Errorf("bare-string source for plugin %q but marketplace %q has no clonable repo", plugin.Name, mp.Name)
		}
		return cloneURL, strings.TrimPrefix(s, "./"), "", nil
	}

	// Object source: {source: "github"|"url"|"git-subdir", repo|url, ref?, sha?, path?}.
	var obj map[string]any
	if err := json.Unmarshal(plugin.Source, &obj); err != nil {
		return "", "", "", fmt.Errorf("plugin %q has unrecognized source: %w", plugin.Name, err)
	}
	kind, _ := obj["source"].(string)
	switch kind {
	case "github":
		repo, _ := obj["repo"].(string)
		if repo == "" {
			return "", "", "", fmt.Errorf("plugin %q github source missing repo", plugin.Name)
		}
		cloneURL = "https://github.com/" + repo
	case "url":
		u, _ := obj["url"].(string)
		if u == "" {
			return "", "", "", fmt.Errorf("plugin %q url source missing url", plugin.Name)
		}
		cloneURL = u
	case "git-subdir":
		u, _ := obj["url"].(string)
		if u == "" {
			return "", "", "", fmt.Errorf("plugin %q git-subdir source missing url", plugin.Name)
		}
		cloneURL = u
		subdir, _ = obj["path"].(string)
	default:
		// Fall back to the marketplace's own repo when the source kind is
		// unfamiliar - the inventory scan will simply find nothing useful.
		if cloneURL == "" {
			return "", "", "", fmt.Errorf("plugin %q has unsupported source kind %q", plugin.Name, kind)
		}
	}

	// Sha pin (installer.go documents this for url sources) takes precedence
	// over a branch/tag ref because we can fetch a sha but `git clone --branch`
	// rejects it.
	if sha, ok := obj["sha"].(string); ok && sha != "" {
		ref = sha
	} else if r, ok := obj["ref"].(string); ok {
		ref = r
	}
	return cloneURL, subdir, ref, nil
}

// splitOwnerRepo extracts a github-style owner/repo pair from a clone URL.
// Falls back to "external"/"<host>" for non-github URLs so the cache layout
// stays predictable.
func splitOwnerRepo(repoURL string) (string, string) {
	const ghPrefix = "https://github.com/"
	if strings.HasPrefix(repoURL, ghPrefix) {
		rest := strings.TrimSuffix(strings.TrimPrefix(repoURL, ghPrefix), ".git")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) >= 2 {
			return parts[0], parts[1]
		}
	}
	host := repoURL
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "git@")
	host = strings.ReplaceAll(host, "/", "_")
	host = strings.ReplaceAll(host, ":", "_")
	return "external", host
}

// sanitizeSegment turns an arbitrary string into a path segment safe to use
// under PluginsDir/cache/_discovery. Disallowed chars are mapped to "_". Any
// run of two-or-more dots (the "..", "...", etc. traversal substrings) also
// collapses to "_" so a malicious owner like "../../etc" can't escape the
// cache. Empty / dots-only input returns "" (caller treats as a bad URL).
func sanitizeSegment(s string) string {
	clean := pathSegRe.ReplaceAllString(s, "_")
	clean = traversalRe.ReplaceAllString(clean, "_")
	clean = strings.Trim(clean, ".")
	if clean == "" || clean == "_" {
		return ""
	}
	return clean
}

// traversalRe matches any run of two-or-more consecutive dots so sanitizeSegment
// can neutralize "..", "...", and similar traversal substrings even when they
// appear mid-segment after slashes have been collapsed to underscores.
var traversalRe = regexp.MustCompile(`\.{2,}`)

// remoteSha resolves ref to a commit sha via `git ls-remote`. Returns "" on
// any error so the caller can fall back to a sha-less cache key.
//
// When ref is already a 40-char sha, ls-remote isn't useful (servers don't
// resolve sha → sha) - return it unchanged.
func remoteSha(ctx context.Context, repoURL, ref string) string {
	if shaRe.MatchString(strings.ToLower(ref)) {
		return strings.ToLower(ref)
	}
	target := ref
	if target == "" {
		target = "HEAD"
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--quiet", repoURL, target)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	// Output is "<sha>\t<refname>" lines; first line wins.
	scanner := bytes.IndexByte(out.Bytes(), '\n')
	first := out.Bytes()
	if scanner > 0 {
		first = first[:scanner]
	}
	tab := bytes.IndexByte(first, '\t')
	if tab <= 0 {
		return ""
	}
	candidate := string(bytes.TrimSpace(first[:tab]))
	if !shaRe.MatchString(candidate) {
		return ""
	}
	return candidate
}

// shallowClone clones url into dst at depth 1, dispatching on whether ref is a
// branch/tag (use `--branch`) or a commit sha (clone HEAD then `git fetch
// <sha>` and checkout). When ref is empty, clones HEAD.
//
// resolvedSha, when non-empty, is used as the post-clone checkout target so
// the working tree matches what we promised in the cache directory name.
func shallowClone(ctx context.Context, url, dst, ref, resolvedSha string) error {
	refIsSha := ref != "" && shaRe.MatchString(strings.ToLower(ref))
	checkoutSha := strings.ToLower(ref)
	if !refIsSha {
		checkoutSha = resolvedSha
	}

	args := []string{"clone", "--depth", "1", "--quiet"}
	if ref != "" && !refIsSha {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, dst)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("git clone %s: %s", url, msg)
		}
		return fmt.Errorf("git clone %s: %w", url, err)
	}

	if checkoutSha != "" && checkoutSha != readHeadSha(dst) {
		// Sha-pinned plugin or branch tip already moved between ls-remote and
		// clone - fetch and check out the exact commit. Fail loudly: a stale
		// working tree would silently produce wrong conflict reports.
		fetch := exec.CommandContext(ctx, "git", "-C", dst, "fetch", "--depth", "1", "origin", checkoutSha)
		var fetchErr bytes.Buffer
		fetch.Stderr = &fetchErr
		if err := fetch.Run(); err != nil {
			return fmt.Errorf("git fetch %s in %s: %s", checkoutSha, url, strings.TrimSpace(fetchErr.String()))
		}
		checkout := exec.CommandContext(ctx, "git", "-C", dst, "checkout", "--quiet", checkoutSha)
		var coErr bytes.Buffer
		checkout.Stderr = &coErr
		if err := checkout.Run(); err != nil {
			return fmt.Errorf("git checkout %s in %s: %s", checkoutSha, url, strings.TrimSpace(coErr.String()))
		}
	}
	return nil
}

// readHeadSha returns the resolved HEAD sha of a clone, or "" on any error
// (the caller doesn't need surgical precision - a missing sha just means we
// can't display a "pinned at <sha>" hint in the UI).
func readHeadSha(repo string) string {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// PreviewCacheRoot returns the root directory under which preview clones are
// stored. Useful for `ccmcp discover` housekeeping commands.
func PreviewCacheRoot(p paths.Paths) string {
	return filepath.Join(p.PluginsDir, "cache", "_discovery")
}
