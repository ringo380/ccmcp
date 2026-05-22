package discovery

import (
	"regexp"
	"sort"
	"strings"
)

// GitHubRepoRef is a parsed github.com/<owner>/<repo> link with the markdown
// bullet text it appeared in (used as a one-line description hint).
type GitHubRepoRef struct {
	Owner string
	Repo  string
	Hint  string // surrounding bullet text, lightly cleaned
}

// repoLinkAnyRe matches github.com/<owner>/<repo>, allowing trailing punctuation
// or path components which we strip. Tolerates raw.githubusercontent.com paths
// (the trailing `/` boundary stops the match before the path component).
var repoLinkAnyRe = regexp.MustCompile(`(?i)github\.com/([\w.-]+)/([\w.-]+?)(?:\.git)?(?:[)\s/#?]|$)`)

// stoplist drops common false-positives — these are repos that turn up in
// almost every awesome-list as references but aren't ccmcp marketplaces.
var stoplist = map[string]bool{
	"github/awesome-claude":     true,
	"sindresorhus/awesome":      true,
	"matiassingers/awesome-rea": true,
	// The awesome-list index repo itself — it's a curated list, not a marketplace.
	"hesreallyhim/awesome-claude-code": true,
	// Hooks examples repo; has no .claude-plugin/marketplace.json.
	"disler/claude-code-hooks-mastery": true,
}

// ExtractGitHubRepos scans markdown for github.com/<owner>/<repo> links inside
// bullet list items. The result is deduplicated and ordered alphabetically.
// Links not inside a bullet are ignored (e.g. badges in the header).
//
// The hint string is the bullet text with markdown link syntax stripped — it
// is best-effort and may be empty.
func ExtractGitHubRepos(markdown string) []GitHubRepoRef {
	seen := map[string]GitHubRepoRef{}
	for _, raw := range strings.Split(markdown, "\n") {
		line := strings.TrimSpace(raw)
		if !isBullet(line) {
			continue
		}
		text := strings.TrimLeft(line, "-*+0123456789. ")
		matches := repoLinkAnyRe.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			owner := m[1]
			repo := strings.TrimSuffix(m[2], ".git")
			key := strings.ToLower(owner + "/" + repo)
			if stoplist[key] {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = GitHubRepoRef{
				Owner: owner,
				Repo:  repo,
				Hint:  cleanBulletText(text),
			}
		}
	}
	out := make([]GitHubRepoRef, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// isBullet returns true if the line opens with a markdown list marker.
func isBullet(line string) bool {
	if len(line) == 0 {
		return false
	}
	if line[0] == '-' || line[0] == '*' || line[0] == '+' {
		return len(line) > 1 && line[1] == ' '
	}
	// Numbered list "1. ".
	if line[0] >= '0' && line[0] <= '9' {
		for i := 1; i < len(line); i++ {
			if line[i] == '.' {
				return i+1 < len(line) && line[i+1] == ' '
			}
			if line[i] < '0' || line[i] > '9' {
				return false
			}
		}
	}
	return false
}

// cleanBulletText drops markdown link syntax `[label](url)` → `label`, image
// embeds `![alt](url)` → "", and trims surrounding whitespace.
func cleanBulletText(s string) string {
	imgRe := regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	s = imgRe.ReplaceAllString(s, "")
	linkRe := regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	s = linkRe.ReplaceAllString(s, "$1")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
