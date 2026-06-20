package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

func TestDoctorBannerWhenClaudeMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir → no claude on PATH

	st, _ := buildState(t)
	m := newModel(st)
	out := drive(m, "0") // jump to Doctor tab (key 0 since the Discover tab was inserted)
	if !strings.Contains(out, "claude CLI not found in PATH") {
		t.Fatalf("expected missing-claude banner, got:\n%s", out)
	}
}

func TestDoctorLLMErrorWraps(t *testing.T) {
	st, _ := buildState(t)
	v := newDoctorView(st)
	v.w = 60
	v.h = 24
	long := strings.Repeat("very-long-error-token ", 12) // forces wrap
	v.llmResults = []llmReviewResult{{path: "/tmp/CLAUDE.md", err: errors.New(long)}}
	v.showLLM = true

	out := v.renderLLM()
	for _, line := range strings.Split(out, "\n") {
		// Strip ANSI escapes by counting only printable runes; lipgloss wraps
		// styled output in escape sequences, so a naive len() will overstate.
		// Use a loose ceiling well above v.w to catch egregious wrap failures.
		if visibleWidth(line) > v.w*2 {
			t.Fatalf("line longer than 2*v.w (%d): %q", visibleWidth(line), line)
		}
	}
	if !strings.Contains(stripANSI(out), "very-long-error-token") {
		t.Fatalf("error content missing from render:\n%s", out)
	}
}

func TestDoctorLLMErrorClaudeNotFoundHint(t *testing.T) {
	st, _ := buildState(t)
	v := newDoctorView(st)
	v.w = 80
	v.h = 24
	v.llmResults = []llmReviewResult{{path: "/tmp/CLAUDE.md", err: doctor.ErrClaudeCLINotFound}}
	v.showLLM = true

	out := stripANSI(v.renderLLM())
	if !strings.Contains(out, "install the claude CLI") {
		t.Fatalf("expected install hint, got:\n%s", out)
	}
}

func TestDoctorLLMErrorAPIError401Hint(t *testing.T) {
	st, _ := buildState(t)
	v := newDoctorView(st)
	v.w = 80
	v.h = 24
	apiErr := &doctor.APIError{Provider: "anthropic", Status: 401, Message: "key rejected"}
	v.llmResults = []llmReviewResult{{path: "/tmp/CLAUDE.md", err: apiErr}}
	v.showLLM = true

	out := stripANSI(v.renderLLM())
	if !strings.Contains(out, "claude /login") && !strings.Contains(out, "claude-cli") {
		t.Fatalf("expected 401 hint, got:\n%s", out)
	}
}

func TestDoctorFixDoneEnrichesExitStatus(t *testing.T) {
	st, _ := buildState(t)
	v := newDoctorView(st)
	v.w = 80
	v.h = 24
	v.update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Simulate the exit-status error from a failed claude CLI run. The handler
	// should rewrite the bare "exit status N" text into "claude CLI exit N".
	// Captured stderr (now surfaced inline below the flash since the goroutine
	// switch in execFixCmd) is not exercised here - see
	// TestDoctorFixErrorSurfacesOutputInline for that path.
	v.update(fixDoneMsg{err: errors.New("exit status 1"), origin: tabDoctor})
	if !strings.Contains(stripANSI(v.flash), "claude CLI exit 1") {
		t.Fatalf("expected enriched message, got %q", v.flash)
	}
}

func TestEnrichExitStatus(t *testing.T) {
	if got := enrichExitStatus("exit status 2"); !strings.Contains(got, "claude CLI exit 2") {
		t.Fatalf("rewrite failed: %q", got)
	}
	if got := enrichExitStatus("some other error"); got != "some other error" {
		t.Fatalf("non-matching message should pass through, got %q", got)
	}
}

// stripANSI removes ANSI CSI sequences so tests can assert on plain text.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func visibleWidth(s string) int {
	return len(stripANSI(s))
}
