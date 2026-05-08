package discovery_test

import (
	"testing"

	"github.com/ringo380/ccmcp/internal/discovery"
)

func TestExtractGitHubReposFromBullets(t *testing.T) {
	md := "" +
		"# Awesome Claude Code\n\n" +
		"badge: [![Build](https://github.com/anthropics/claude-code)](...)\n\n" +
		"## Marketplaces\n\n" +
		"- [davila7](https://github.com/davila7/claude-code-templates) — templates galore\n" +
		"- wshobson/agents at https://github.com/wshobson/agents.git\n" +
		"- duplicate: https://github.com/davila7/claude-code-templates/tree/main\n" +
		"- 1. [stoplisted](https://github.com/anthropics/claude-code) — should be ignored\n" +
		"text outside a list with https://github.com/should/skip — ignored.\n"
	got := discovery.ExtractGitHubRepos(md)
	wantRepos := map[string]bool{
		"davila7/claude-code-templates": true,
		"wshobson/agents":               true,
	}
	if len(got) != len(wantRepos) {
		t.Fatalf("expected %d repos, got %d: %+v", len(wantRepos), len(got), got)
	}
	for _, r := range got {
		key := r.Owner + "/" + r.Repo
		if !wantRepos[key] {
			t.Errorf("unexpected repo: %s", key)
		}
	}
}

func TestExtractGitHubReposEmpty(t *testing.T) {
	if got := discovery.ExtractGitHubRepos(""); len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
	if got := discovery.ExtractGitHubRepos("just a paragraph"); len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestExtractGitHubReposStripsTrailingPath(t *testing.T) {
	md := "- https://github.com/foo/bar/tree/main/sub — note\n"
	got := discovery.ExtractGitHubRepos(md)
	if len(got) != 1 || got[0].Repo != "bar" || got[0].Owner != "foo" {
		t.Fatalf("expected foo/bar, got %+v", got)
	}
}
