package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ringo380/ccmcp/internal/paths"
)

// PreviewResult points at a freshly cloned plugin directory ready for scanning.
// The directory is reused across calls when the resolved sha hasn't changed.
type PreviewResult struct {
	// Dir is the local path containing the plugin's contents (the root the
	// scanners walk).
	Dir string
	// Repo is the cloned upstream URL — useful for messages.
	Repo string
	// Sha is the resolved git commit sha at HEAD of the clone, or empty when
	// the source isn't git-backed.
	Sha string
}

// PreviewClone resolves a plugin's source field to an upstream repo, shallow
// clones it (if not already cached), and returns the directory ready for
// inventory scanning.
//
// Caches under <PluginsDir>/cache/_discovery/<owner>/<repo>/<sha-or-HEAD>/.
// Reuses the cache when sha matches an existing dir.
func PreviewClone(ctx context.Context, p paths.Paths, mp RemoteMarketplace, plugin RemotePlugin) (*PreviewResult, error) {
	repoURL, subdir, ref, err := resolvePluginUpstream(mp, plugin)
	if err != nil {
		return nil, err
	}

	owner, repo := splitOwnerRepo(repoURL)
	root := filepath.Join(p.PluginsDir, "cache", "_discovery", owner, repo)

	// If we have a cached HEAD already, reuse it; otherwise clone.
	headDir := filepath.Join(root, "HEAD")
	if _, err := os.Stat(filepath.Join(headDir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(headDir), 0o755); err != nil {
			return nil, err
		}
		if err := shallowClone(ctx, repoURL, headDir, ref); err != nil {
			return nil, err
		}
	}

	sha := readHeadSha(headDir)
	dir := headDir
	if subdir != "" {
		dir = filepath.Join(headDir, subdir)
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

	// Object source: {source: "github"|"url"|"git-subdir", repo|url, ref?, path?}.
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
		// unfamiliar — the inventory scan will simply find nothing useful.
		if cloneURL == "" {
			return "", "", "", fmt.Errorf("plugin %q has unsupported source kind %q", plugin.Name, kind)
		}
	}

	if r, ok := obj["ref"].(string); ok {
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

// shallowClone runs `git clone --depth 1` (with optional --branch ref) into
// dst, with the supplied context governing the subprocess lifetime.
func shallowClone(ctx context.Context, url, dst, ref string) error {
	args := []string{"clone", "--depth", "1", "--quiet"}
	if ref != "" {
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
	return nil
}

// readHeadSha returns the resolved HEAD sha of a clone, or "" on any error
// (the caller doesn't need surgical precision — a missing sha just means we
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
