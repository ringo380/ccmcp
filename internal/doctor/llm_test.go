package doctor_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/doctor"
)

// installFakeClaude writes a shell script named "claude" into a fresh dir and
// prepends it to PATH for the duration of the test. The script body is the
// caller's responsibility (must be POSIX-shell parseable).
func installFakeClaude(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude shim relies on POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return script
}

func writeMD(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReviewAutoFallbackToClaudeCLI(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	installFakeClaude(t, `cat > /dev/null; printf "FAKE-OK"`)
	tmp := t.TempDir()
	path := writeMD(t, tmp, "CLAUDE.md", "# hello\n")

	out, err := doctor.Review(path, doctor.ReviewOptions{})
	if err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if !strings.Contains(out, "FAKE-OK") {
		t.Fatalf("expected FAKE-OK in output, got %q", out)
	}
}

// TestReviewPrefersClaudeCLIOverEnvKey locks in the precedence rule: when the
// claude CLI is on PATH AND ANTHROPIC_API_KEY is set, auto-resolution must pick
// the CLI (headless `claude --print`), not the HTTP API. The fake claude shim
// returns a sentinel; a real Anthropic HTTP call could not produce it.
func TestReviewPrefersClaudeCLIOverEnvKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-be-ignored")
	t.Setenv("OPENAI_API_KEY", "")
	installFakeClaude(t, `cat > /dev/null; printf "CLI-WINS"`)
	tmp := t.TempDir()
	path := writeMD(t, tmp, "CLAUDE.md", "# hello\n")

	out, err := doctor.Review(path, doctor.ReviewOptions{})
	if err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if !strings.Contains(out, "CLI-WINS") {
		t.Fatalf("expected the claude CLI to be used over the env key, got %q", out)
	}
}

// TestReviewClaudeCLIIsolatesMCP guards against the MCP-overflow trap: every
// headless `claude --print` the review path spawns must disable the user's
// configured MCP servers, or an MCP-heavy machine overflows the model window.
// The fake claude echoes its args so we can assert the flags are present.
func TestReviewClaudeCLIIsolatesMCP(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	installFakeClaude(t, `cat > /dev/null; printf "%s" "$*"`)
	tmp := t.TempDir()
	path := writeMD(t, tmp, "CLAUDE.md", "# hi\n")

	out, err := doctor.Review(path, doctor.ReviewOptions{})
	if err != nil {
		t.Fatalf("Review error: %v", err)
	}
	if !strings.Contains(out, "--strict-mcp-config") {
		t.Fatalf("expected --strict-mcp-config in claude args, got %q", out)
	}
	if !strings.Contains(out, "mcpServers") {
		t.Fatalf("expected an empty --mcp-config server set in claude args, got %q", out)
	}
}

func TestReviewClaudeCLINotFoundExplicit(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("PATH", t.TempDir()) // empty dir
	tmp := t.TempDir()
	path := writeMD(t, tmp, "CLAUDE.md", "# hello\n")

	_, err := doctor.Review(path, doctor.ReviewOptions{Provider: doctor.ProviderClaudeCLI})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, doctor.ErrClaudeCLINotFound) {
		t.Fatalf("expected ErrClaudeCLINotFound, got %v", err)
	}
}

func TestReviewClaudeCLINonZeroExit(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	installFakeClaude(t, `printf "boom\n" >&2; exit 1`)
	tmp := t.TempDir()
	path := writeMD(t, tmp, "CLAUDE.md", "# hi\n")

	_, err := doctor.Review(path, doctor.ReviewOptions{Provider: doctor.ProviderClaudeCLI})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("expected exit 1 in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr 'boom' captured, got %q", err.Error())
	}
}

func TestReviewNoKeysAndNoCLIErrors(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("PATH", t.TempDir())
	tmp := t.TempDir()
	path := writeMD(t, tmp, "CLAUDE.md", "# hi\n")

	_, err := doctor.Review(path, doctor.ReviewOptions{})
	if err == nil {
		t.Fatal("expected error when no keys and no CLI")
	}
	// Auto-fallback degenerates to anthropic, which raises a friendly key-missing error.
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("expected ANTHROPIC_API_KEY hint, got %q", err.Error())
	}
}

func TestAPIErrorFormatting(t *testing.T) {
	tt := []struct {
		name     string
		err      *doctor.APIError
		contains []string
	}{
		{
			name:     "with parsed message",
			err:      &doctor.APIError{Provider: "anthropic", Status: 400, Message: "bad input"},
			contains: []string{"anthropic", "400", "bad input"},
		},
		{
			name:     "raw fallback",
			err:      &doctor.APIError{Provider: "openai", Status: 500, Raw: `{"x":1}`},
			contains: []string{"openai", "500"},
		},
		{
			name:     "bare status",
			err:      &doctor.APIError{Provider: "anthropic", Status: 503},
			contains: []string{"anthropic", "503"},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.err.Error()
			for _, want := range tc.contains {
				if !strings.Contains(s, want) {
					t.Fatalf("error %q missing %q", s, want)
				}
			}
		})
	}
}

func TestAPIErrorIsAssertable(t *testing.T) {
	var apiErr *doctor.APIError
	src := &doctor.APIError{Provider: "anthropic", Status: 401, Message: "key rejected"}
	if !errors.As(src, &apiErr) {
		t.Fatal("errors.As should match *APIError")
	}
	if apiErr.Status != 401 {
		t.Fatalf("status=%d", apiErr.Status)
	}
}
