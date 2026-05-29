package tui

import (
	"errors"
	"strings"
	"testing"
)

// classifyClaudeFailure turns the captured stdout/stderr of a failed
// `claude --print` run into an actionable single-line message. The claude CLI
// writes API errors to stdout, so these fixtures mirror what execFixCmd
// captures for each distinct failure mode.
func TestClassifyClaudeFailure(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string // substring expected in the result; "" means expect ""
	}{
		{
			name:   "usage limit with reset date",
			output: "API Error: 400 You have reached your specified API usage limits. You will regain access on 2026-06-01 at 00:00 UTC.",
			want:   "regains access 2026-06-01",
		},
		{
			name:   "usage limit no date",
			output: "Error: you have reached your usage limit for this organization",
			want:   "usage limit reached",
		},
		{
			name:   "context overflow",
			output: "API Error: 400 Prompt is too long: 250000 tokens > 200000 maximum",
			want:   "context overflow",
		},
		{
			name:   "auth invalid token",
			output: "API Error: 401 Invalid bearer token",
			want:   "not authenticated",
		},
		{
			name:   "auth oauth expired",
			output: "OAuth token has expired, please re-authenticate",
			want:   "not authenticated",
		},
		{
			name:   "model not found",
			output: "API Error: model claude-bogus-9 not found",
			want:   "model unavailable",
		},
		{
			name:   "rate limited",
			output: "API Error: 429 rate limit exceeded",
			want:   "rate limited",
		},
		{
			name:   "unrecognised -> empty",
			output: "something went wrong in a way we don't classify",
			want:   "",
		},
		{
			name:   "empty output -> empty",
			output: "",
			want:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyClaudeFailure([]byte(tc.output), errors.New("exit status 1"))
			if tc.want == "" {
				if got != "" {
					t.Fatalf("expected empty classification, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("classification %q does not contain %q", got, tc.want)
			}
		})
	}
}

func TestExtractRegainDate(t *testing.T) {
	cases := map[string]string{
		"You will regain access on 2026-06-01 at 00:00 UTC.": "2026-06-01 at 00:00 UTC",
		"regain access on 2026-12-31":                        "2026-12-31",
		"no date here":                                       "",
	}
	for in, want := range cases {
		if got := extractRegainDate(in); got != want {
			t.Errorf("extractRegainDate(%q) = %q, want %q", in, got, want)
		}
	}
}
