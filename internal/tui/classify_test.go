package tui

import (
	"reflect"
	"testing"

	"github.com/ringo380/ccmcp/internal/config"
)

// TestClassifyOverridesBuckets exercises every classifier branch against a single
// realistic override list. The fixture mirrors the four buckets surfaced in the
// 2026-04-20 investigation: stash ghost, disabled plugin, orphan plugin, orphan stdio,
// plus the two "known-good" buckets (plugin active, claude.ai, stdio live) as controls.
func TestClassifyOverridesBuckets(t *testing.T) {
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
		"plugin:context7:context7",    // pluginActive
		"plugin:postman:postman",      // pluginDisabled  (plugin installed, globally off)
		"plugin:Notion:notion",        // orphanPlugin    (plugin not installed at all)
		"claude.ai Notion",            // claudeai
		"claude.ai Revoked",           // orphanStdio (not in ever-connected list)
		"alive-stdio",                 // stdioLive (user scope has it)
		"dropbox",                     // stashGhost (only in stash)
		"appstore-connect",            // orphanStdio (no source anywhere)
	}

	got := classifyOverrides(overrides, userMCPs, localMCPs, claudeAi, stashed, pluginMCPs, installed)

	want := classifiedOverrides{
		pluginActive:   []string{"plugin:context7:context7"},
		pluginDisabled: []string{"plugin:postman:postman"},
		claudeai:       []string{"claude.ai Notion"},
		stdioLive:      []string{"alive-stdio"},
		stashGhost:     []string{"dropbox"},
		orphanPlugin:   []string{"plugin:Notion:notion"},
		orphanStdio:    []string{"claude.ai Revoked", "appstore-connect"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("classifyOverrides mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}
