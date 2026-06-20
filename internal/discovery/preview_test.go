package discovery

import (
	"strings"
	"testing"
)

// Internal-package test so we can exercise sanitizeSegment + shaRe directly
// without exporting them.

func TestSanitizeSegmentRejectsTraversal(t *testing.T) {
	tt := []struct {
		in   string
		want string
	}{
		{"owner", "owner"},
		{"safe-repo_v2.0", "safe-repo_v2.0"},
		{"..", ""},
		{".", ""},
		{"", ""},
		{"../../etc", "____etc"},
		{"foo/bar", "foo_bar"},
		{"foo\\bar", "foo_bar"},
		{"foo:bar", "foo_bar"},
		{"foo bar", "foo_bar"},
	}
	for _, tc := range tt {
		got := sanitizeSegment(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeSegment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShaRegex(t *testing.T) {
	good := []string{
		"abcdef0123456789abcdef0123456789abcdef01",
		"0000000000000000000000000000000000000000",
	}
	bad := []string{
		"main",
		"v1.2.3",
		"abc",
		"ABCDEF0123456789ABCDEF0123456789ABCDEF01", // upper-case rejected (lowercase only)
		"abcdef0123456789abcdef0123456789abcdef0",  // 39 chars
		"abcdef0123456789abcdef0123456789abcdef012", // 41 chars
	}
	for _, s := range good {
		if !shaRe.MatchString(s) {
			t.Errorf("expected %q to match shaRe", s)
		}
	}
	for _, s := range bad {
		if shaRe.MatchString(s) {
			t.Errorf("expected %q NOT to match shaRe", s)
		}
	}
}

func TestSplitOwnerRepoCleanedByCaller(t *testing.T) {
	// splitOwnerRepo accepts arbitrary input - sanitizeSegment is the gate.
	// This test documents the integration: any "../"-style segment from
	// splitOwnerRepo is reduced to underscores before being used as a path.
	cases := []struct {
		url   string
		owner string
		repo  string
	}{
		{"https://github.com/owner/repo", "owner", "repo"},
		{"https://github.com/owner/repo.git", "owner", "repo"},
		{"https://github.com/../evil", "..", "evil"},
	}
	for _, c := range cases {
		o, r := splitOwnerRepo(c.url)
		if o != c.owner || r != c.repo {
			t.Errorf("splitOwnerRepo(%q) = (%q,%q), want (%q,%q)", c.url, o, r, c.owner, c.repo)
		}
	}
	// And confirm sanitizeSegment kills the dangerous one.
	if got := sanitizeSegment(".."); got != "" {
		t.Errorf("sanitizeSegment with '..' should be rejected, got %q", got)
	}
	if !strings.HasPrefix(sanitizeSegment("../foo"), "_") {
		t.Errorf("sanitizeSegment should rewrite leading slash chars, got %q", sanitizeSegment("../foo"))
	}
}
