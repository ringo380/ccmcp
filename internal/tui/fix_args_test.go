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
	assertMCPIsolated(t, claudeFixModelArgs(1), "claudeFixModelArgs")
	assertMCPIsolated(t, claudeFixArgs("do the thing", 1), "claudeFixArgs")
	assertMCPIsolated(t, claudeAssetFixArgs("do the thing", permDescription, 1), "claudeAssetFixArgs(description)")
	assertMCPIsolated(t, claudeAssetFixArgs("do the thing", permRename, 1), "claudeAssetFixArgs(rename)")
}

// fixTurnsForItems scales the headless --max-turns cap with the number of files
// a run will touch: a single fix gets the baseline headroom, bulk fixes get
// more so a large bundle isn't starved mid-batch, and an upper bound prevents a
// runaway loop. The single-item value must match the documented baseline so a
// one-row fix behaves identically to before the bulk-scaling change.
func TestFixTurnsForItems(t *testing.T) {
	cases := []struct {
		items int
		want  int
	}{
		{items: 0, want: 12},  // clamped to 1 → baseline
		{items: 1, want: 12},  // base 8 + 4*1
		{items: 3, want: 20},  // base 8 + 4*3
		{items: 10, want: 48}, // base 8 + 4*10
		{items: 13, want: 60}, // base 8 + 4*13 = 60, exactly the cap
		{items: 50, want: 60}, // far past the cap → clamped
	}
	for _, c := range cases {
		if got := fixTurnsForItems(c.items); got != c.want {
			t.Errorf("fixTurnsForItems(%d) = %d, want %d", c.items, got, c.want)
		}
	}
}

// A bulk run's CLI args must carry a higher --max-turns than a single fix, or
// the bundle exhausts its budget partway through and exits "Reached max turns".
func TestBulkFixArgsScaleTurns(t *testing.T) {
	turnsOf := func(args []string) string {
		i := slices.Index(args, "--max-turns")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("--max-turns missing from args: %v", args)
		}
		return args[i+1]
	}
	single := turnsOf(claudeFixArgs("x", 1))
	bulk := turnsOf(claudeFixArgs("x", 8))
	if single != "12" {
		t.Errorf("single-fix max-turns = %s, want 12", single)
	}
	if bulk != "40" {
		t.Errorf("8-item bulk max-turns = %s, want 40", bulk)
	}
}

// claudeFixModelArgs appends --fallback-model only when the detected Claude Code
// version supports it (>= 2.1.152), so older versions never receive an unknown
// flag. Caps is a package var; restore it after mutating.
func TestClaudeFixModelArgsFallbackGate(t *testing.T) {
	orig := Caps
	t.Cleanup(func() { Caps = orig })

	Caps.SupportsFallbackModel = false
	if slices.Contains(claudeFixModelArgs(1), "--fallback-model") {
		t.Error("baseline (no fallback support) must not pass --fallback-model")
	}

	Caps.SupportsFallbackModel = true
	Caps.FallbackModel = "claude-sonnet-4-6"
	args := claudeFixModelArgs(1)
	i := slices.Index(args, "--fallback-model")
	if i < 0 || i+1 >= len(args) || args[i+1] != "claude-sonnet-4-6" {
		t.Errorf("expected --fallback-model claude-sonnet-4-6; got %v", args)
	}
}
