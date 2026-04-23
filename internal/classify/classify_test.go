package classify

import (
	"reflect"
	"testing"

	"github.com/ringo380/ccmcp/internal/config"
)

// TestClassifyBuckets exercises every classifier branch against a single
// realistic override list. The fixture mirrors the four buckets surfaced in the
// 2026-04-20 investigation: stash ghost, disabled plugin, orphan plugin, orphan stdio,
// plus the three "known-good" buckets (plugin active, claude.ai, stdio live) as controls.
func TestClassifyBuckets(t *testing.T) {
	userMCPs := []string{"alive-stdio"}
	localMCPs := []string{}
	claudeAi := []string{"claude.ai Notion"}
	stashed := []string{"dropbox", "railway-mcp-server"}

	pluginMCPs := map[string][]config.PluginMCPSource{
		"context7": {{PluginID: "context7@mkt", Enabled: true}},
		"postman":  {{PluginID: "postman@mkt", Enabled: false}},
	}
	installed := &config.InstalledPlugins{Raw: map[string]any{
		"plugins": map[string]any{
			"context7@mkt": []any{map[string]any{"installPath": "/c"}},
			"postman@mkt":  []any{map[string]any{"installPath": "/p"}},
			// Note: "Notion" plugin deliberately absent → bucket 3 (orphan-plugin)
		},
	}}

	overrides := []string{
		"plugin:context7:context7", // PluginActive
		"plugin:postman:postman",   // PluginDisabled  (plugin installed, globally off)
		"plugin:Notion:notion",     // OrphanPlugin    (plugin not installed at all)
		"claude.ai Notion",         // ClaudeAi
		"claude.ai Revoked",        // OrphanStdio (not in ever-connected list)
		"alive-stdio",              // StdioLive (user scope has it)
		"dropbox",                  // StashGhost (only in stash)
		"appstore-connect",         // OrphanStdio (no source anywhere)
	}

	got := Classify(overrides, userMCPs, localMCPs, claudeAi, stashed, pluginMCPs, installed)

	want := Overrides{
		PluginActive:   []string{"plugin:context7:context7"},
		PluginDisabled: []string{"plugin:postman:postman"},
		ClaudeAi:       []string{"claude.ai Notion"},
		StdioLive:      []string{"alive-stdio"},
		StashGhost:     []string{"dropbox"},
		OrphanPlugin:   []string{"plugin:Notion:notion"},
		OrphanStdio:    []string{"claude.ai Revoked", "appstore-connect"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Classify mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestPluralY(t *testing.T) {
	if got := PluralY(1); got != "y" {
		t.Errorf("PluralY(1) = %q, want %q", got, "y")
	}
	for _, n := range []int{0, 2, 10} {
		if got := PluralY(n); got != "ies" {
			t.Errorf("PluralY(%d) = %q, want %q", n, got, "ies")
		}
	}
}
