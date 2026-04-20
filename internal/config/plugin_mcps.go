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
	Enabled    bool   // true when the owning plugin is set to true in enabledPlugins
}

// ScanAllInstalledPluginMCPs walks every plugin that's REGISTERED in enabledPlugins (regardless
// of whether the value is true or false) and reads <installPath>/.mcp.json to collect every
// MCP server the plugin ships. Each emitted source carries an Enabled bool.
//
// This is strictly a superset of ScanEnabledPluginMCPs — callers that only care about what
// will actually load in the current project should filter by .Enabled (or use the helper).
//
// Why all installed plugins, not just enabled? Per-project disable entries in
// ~/.claude.json#/projects[<p>].disabledMcpServers use the key `plugin:<plugin>:<server>`,
// which persists even after the user globally disables the plugin. If ccmcp only knew about
// enabled plugins, it'd mis-classify those overrides as "unknown source" when in reality
// we know exactly which installed-but-disabled plugin they came from.
func ScanAllInstalledPluginMCPs(settings *Settings, installed *InstalledPlugins, pluginsDir string) map[string][]PluginMCPSource {
	out := map[string][]PluginMCPSource{}
	if settings == nil || installed == nil {
		return out
	}
	// Build an id -> installPath index from installed_plugins.json.
	paths := map[string]string{}
	for _, p := range installed.List() {
		paths[p.ID] = p.InstallPath
	}
	for _, e := range settings.PluginEntries() {
		path := paths[e.ID]
		if path == "" {
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
		if len(servers) == 0 {
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
				Enabled:    e.Enabled,
			})
		}
	}
	for k := range out {
		srcs := out[k]
		sort.Slice(srcs, func(i, j int) bool { return srcs[i].PluginID < srcs[j].PluginID })
		out[k] = srcs
	}
	return out
}

// ScanEnabledPluginMCPs is the "only what will actually load" view. Thin filter over
// ScanAllInstalledPluginMCPs that drops entries whose Enabled is false. Kept for back
// compat with callers (summary tab, CLI list) that don't care about disabled plugins.
func ScanEnabledPluginMCPs(settings *Settings, installed *InstalledPlugins, pluginsDir string) map[string][]PluginMCPSource {
	all := ScanAllInstalledPluginMCPs(settings, installed, pluginsDir)
	out := map[string][]PluginMCPSource{}
	for name, srcs := range all {
		var kept []PluginMCPSource
		for _, s := range srcs {
			if s.Enabled {
				kept = append(kept, s)
			}
		}
		if len(kept) > 0 {
			out[name] = kept
		}
	}
	return out
}

// looksLikeBareMCPMap recognizes { "name": { command: ..., args: ...} } at the top level.
func looksLikeBareMCPMap(raw map[string]any) bool {
	if len(raw) == 0 {
		return false
	}
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
