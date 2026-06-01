package tui

import (
	"slices"
	"strings"
	"testing"
)

// Every headless `claude --print` invocation MUST disable the user's configured
// MCP servers. On a machine with many MCP servers (ccmcp's exact audience), the
// loaded tool definitions overflow the model context → "Prompt is too long" →
// exit 1 with tokens charged and no edit made. --strict-mcp-config + an empty
// --mcp-config excludes them; the fixes only ever need Edit/Write/Read.
func assertMCPIsolated(t *testing.T, args []string, label string) {
	t.Helper()
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Errorf("%s: missing --strict-mcp-config; got %v", label, args)
	}
	i := slices.Index(args, "--mcp-config")
	if i < 0 || i+1 >= len(args) {
		t.Errorf("%s: missing --mcp-config <value>; got %v", label, args)
		return
	}
	if !strings.Contains(args[i+1], "mcpServers") {
		t.Errorf("%s: --mcp-config value should be an empty server set, got %q", label, args[i+1])
	}
}

func TestClaudeFixArgsIsolateMCP(t *testing.T) {
	assertMCPIsolated(t, claudeFixModelArgs(), "claudeFixModelArgs")
	assertMCPIsolated(t, claudeFixArgs("do the thing"), "claudeFixArgs")
	assertMCPIsolated(t, claudeAssetFixArgs("do the thing", permDescription), "claudeAssetFixArgs(description)")
	assertMCPIsolated(t, claudeAssetFixArgs("do the thing", permRename), "claudeAssetFixArgs(rename)")
}

// claudeFixModelArgs appends --fallback-model only when the detected Claude Code
// version supports it (>= 2.1.152), so older versions never receive an unknown
// flag. Caps is a package var; restore it after mutating.
func TestClaudeFixModelArgsFallbackGate(t *testing.T) {
	orig := Caps
	t.Cleanup(func() { Caps = orig })

	Caps.SupportsFallbackModel = false
	if slices.Contains(claudeFixModelArgs(), "--fallback-model") {
		t.Error("baseline (no fallback support) must not pass --fallback-model")
	}

	Caps.SupportsFallbackModel = true
	Caps.FallbackModel = "claude-sonnet-4-6"
	args := claudeFixModelArgs()
	i := slices.Index(args, "--fallback-model")
	if i < 0 || i+1 >= len(args) || args[i+1] != "claude-sonnet-4-6" {
		t.Errorf("expected --fallback-model claude-sonnet-4-6; got %v", args)
	}
}
