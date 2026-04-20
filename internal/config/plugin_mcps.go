package config

import (
	"os"
	"path/filepath"
	"sort"
)

// PluginMCPSource describes an MCP server registered by a plugin's bundled .mcp.json.
// These load automatically whenever the plugin is enabled — they are NOT managed via
// ~/.claude.json or the top-level .mcp.json, so ccmcp must scan plugin cache dirs to
// find them.
type PluginMCPSource struct {
	MCPName    string // e.g. "context7"
	PluginID   string // e.g. "context7@claude-plugins-official"
	PluginPath string // e.g. /Users/x/.claude/plugins/cache/claude-plugins-official/context7/unknown
	Config     any    // the raw MCP entry from the plugin's .mcp.json
}

// ScanEnabledPluginMCPs walks every installed plugin whose enabledPlugins value is true
// and reads <installPath>/.mcp.json (if present), collecting each registered MCP server.
// The result maps MCP name -> list of sources (typically 1, but same name could come from
// multiple enabled plugins — that's a name collision worth surfacing).
//
// Sources of plugin metadata:
//   - ~/.claude/settings.json#/enabledPlugins — which plugins are currently on (bool map)
//   - ~/.claude/plugins/installed_plugins.json — resolves qualified id -> installPath
//
// The installed_plugins.json entries tell us where each plugin's cache lives on disk.
// If the installPath is missing/empty, we fall back to the conventional
// cache/<marketplace>/<name>/unknown/ layout.
func ScanEnabledPluginMCPs(settings *Settings, installed *InstalledPlugins, pluginsDir string) map[string][]PluginMCPSource {
	out := map[string][]PluginMCPSource{}
	if settings == nil || installed == nil {
		return out
	}
	// Build an id -> installPath index.
	paths := map[string]string{}
	for _, p := range installed.List() {
		paths[p.ID] = p.InstallPath
	}
	for _, e := range settings.PluginEntries() {
		if !e.Enabled {
			continue
		}
		path := paths[e.ID]
		if path == "" {
			// Fall back to convention
			name, mkt := ParsePluginID(e.ID)
			if mkt == "" {
				continue
			}
			candidate := filepath.Join(pluginsDir, "cache", mkt, name, "unknown")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			} else {
				continue
			}
		}
		mjson := filepath.Join(path, ".mcp.json")
		mj, err := LoadMCPJson(mjson)
		if err != nil {
			continue
		}
		servers := mj.Servers()
		// A plugin can also ship `.mcp.json` with entries at the top level (no "mcpServers" wrapper).
		// We already handle the {mcpServers:{...}} form via LoadMCPJson. Handle the bare-object form too.
		if len(servers) == 0 {
			// Re-read raw; if top level looks like {name: {command:...}} we use that.
			raw, _ := RawJSON(mjson)
			if looksLikeBareMCPMap(raw) {
				servers = raw
			}
		}
		for name, cfg := range servers {
			out[name] = append(out[name], PluginMCPSource{
				MCPName:    name,
				PluginID:   e.ID,
				PluginPath: path,
				Config:     cfg,
			})
		}
	}
	// Sort each slice for deterministic output.
	for k := range out {
		srcs := out[k]
		sort.Slice(srcs, func(i, j int) bool { return srcs[i].PluginID < srcs[j].PluginID })
		out[k] = srcs
	}
	return out
}

// looksLikeBareMCPMap recognizes { "name": { command: ..., args: ...} } at the top level.
func looksLikeBareMCPMap(raw map[string]any) bool {
	if len(raw) == 0 {
		return false
	}
	// Heuristic: every top-level value is an object, and at least one has command/type/url.
	for _, v := range raw {
		inner, ok := v.(map[string]any)
		if !ok {
			return false
		}
		_, hasCmd := inner["command"]
		_, hasType := inner["type"]
		_, hasURL := inner["url"]
		if !hasCmd && !hasType && !hasURL {
			return false
		}
	}
	return true
}

// SortedPluginSources returns MCP names that have plugin sources, sorted.
func SortedPluginSources(m map[string][]PluginMCPSource) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
