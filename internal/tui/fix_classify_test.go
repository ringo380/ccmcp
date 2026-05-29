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
		// Anchoring regressions: incidental numbers must NOT trip the
		// status-code arms. "401"/"429" embedded in a larger number have no
		// word boundary, so they should not classify as auth / rate-limit.
		{
			name:   "embedded 401 in token count -> empty",
			output: "claude run produced 14012 tokens of output, then the edit was not applied",
			want:   "",
		},
		{
			name:   "embedded 429 in byte count -> empty",
			output: "wrote 4290 bytes before the process exited non-zero",
			want:   "",
		},
		// Quoted file content mentioning auth concepts must not be read as an
		// auth failure — only structured API auth errors should.
		{
			name:   "file content mentions authentication -> empty",
			output: "edited the section describing the authentication flow and oauth scopes; exited without applying",
			want:   "",
		},
		// "model" and "not found" far apart (different concerns) must not
		// classify as a model error.
		{
			name:   "model word far from not found -> empty",
			output: "the model produced a long response but the target file path was not found by the read step earlier in the unrelated narration",
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

// TestClassifyClaudeFailureFromError covers the doctor-review path, where the
// failure reason is folded into the returned error string (callClaudeCLI) and
// there is no separate output buffer. classifyClaudeFailure must scan err too.
func TestClassifyClaudeFailureFromError(t *testing.T) {
	err := errors.New("claude CLI exit 1: API Error: 400 You have reached your specified API usage limits. You will regain access on 2026-06-01 at 00:00 UTC.")
	got := classifyClaudeFailure(nil, err)
	if !strings.Contains(got, "regains access 2026-06-01") {
		t.Fatalf("expected usage-limit classification from error string, got %q", got)
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
