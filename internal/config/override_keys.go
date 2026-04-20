package config

import "strings"

// MCPSource identifies where an MCP server entry originates. The same display name
// can appear under multiple sources (e.g. "context7" the stdio MCP vs the one
// registered by the context7 plugin) — they are distinct entries with distinct
// override keys in ~/.claude.json#/projects[<path>].disabledMcpServers.
type MCPSource string

const (
	SourceUser    MCPSource = "user"     // ~/.claude.json#/mcpServers
	SourceLocal   MCPSource = "local"    // ~/.claude.json#/projects[<cwd>]/mcpServers
	SourceProject MCPSource = "project"  // ./.mcp.json
	SourcePlugin  MCPSource = "plugin"   // bundled in an enabled plugin
	SourceClaude  MCPSource = "claudeai" // claude.ai remote integration (OAuth)
	SourceStash   MCPSource = "stash"    // ccmcp-only parked
	SourceUnknown MCPSource = "unknown"  // leftover in disabledMcpServers with no matching source
)

// OverrideKey returns the string key Claude Code uses in disabledMcpServers to refer
// to an MCP of the given source.
//
//	stdio-ish (user/local/project):  "<name>"
//	claude.ai:                        "claude.ai <name>"
//	plugin:                           "plugin:<pluginName>:<name>"
//	stash / unknown:                  "" (stash isn't reflected in disabledMcpServers; unknown uses the raw key)
//
// For stash we return "" to signal that space-toggling-as-override doesn't apply.
func OverrideKey(source MCPSource, displayName, pluginName string) string {
	switch source {
	case SourceUser, SourceLocal, SourceProject:
		return displayName
	case SourceClaude:
		return "claude.ai " + displayName
	case SourcePlugin:
		if pluginName == "" {
			return ""
		}
		return "plugin:" + pluginName + ":" + displayName
	case SourceUnknown:
		return displayName // the raw key
	default:
		return ""
	}
}

// ParseOverrideKey is the inverse: given an entry from disabledMcpServers, identify
// its source and canonical display name. For plugin entries it also returns the
// plugin name.
//
// Heuristics:
//   - leading "plugin:" with at least one more ":" ⇒ plugin-sourced
//   - leading "claude.ai " ⇒ claude.ai integration
//   - anything else ⇒ we can't tell whether it's user/local/project just from the key,
//     so caller must cross-reference other sources. We report SourceUnknown here; the
//     rebuild code then resolves by looking it up in user/local/project maps.
func ParseOverrideKey(key string) (source MCPSource, displayName, pluginName string) {
	if strings.HasPrefix(key, "plugin:") {
		rest := strings.TrimPrefix(key, "plugin:")
		i := strings.Index(rest, ":")
		if i > 0 {
			return SourcePlugin, rest[i+1:], rest[:i]
		}
		// malformed "plugin:x" with no second colon — treat as unknown
		return SourceUnknown, key, ""
	}
	if strings.HasPrefix(key, "claude.ai ") {
		return SourceClaude, strings.TrimPrefix(key, "claude.ai "), ""
	}
	return SourceUnknown, key, ""
}
