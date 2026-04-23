// Package classify buckets a project's disabledMcpServers list by the reason each
// entry is there. Both the TUI summary tab and `ccmcp mcp prune` consume the same
// classification so the two surfaces can never drift.
//
// Buckets:
//
//	PluginActive    — "plugin:X:Y" where plugin X is enabled globally (legit per-project override)
//	PluginDisabled  — "plugin:X:Y" where plugin X is installed but globally off (legit, informational)
//	ClaudeAi        — "claude.ai Z" where Z is in claudeAiMcpEverConnected
//	StdioLive       — plain name matching a live user/local source
//	StashGhost      — plain name matching ONLY a stash entry (leftover from pre-stash life)
//	OrphanPlugin    — "plugin:X:Y" where plugin X is not installed anywhere
//	OrphanStdio     — plain name with no source on disk
package classify

import (
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/stringslice"
)

// Overrides groups every entry in a project's disabledMcpServers into its
// explaining bucket. Pure data — safe to compare with reflect.DeepEqual.
type Overrides struct {
	PluginActive   []string
	PluginDisabled []string
	ClaudeAi       []string
	StdioLive      []string
	StashGhost     []string
	OrphanPlugin   []string
	OrphanStdio    []string
}

// Classify buckets the given overrides list. Pure function — no I/O. Callers
// pass in the pre-loaded config views they already have on hand.
func Classify(
	overrides []string,
	userMCPs []string,
	localMCPs []string,
	claudeAi []string, // full "claude.ai Name" strings
	stashedNames []string,
	pluginMCPs map[string][]config.PluginMCPSource,
) Overrides {
	userSet := stringslice.Set(userMCPs)
	localSet := stringslice.Set(localMCPs)
	stashSet := stringslice.Set(stashedNames)
	claudeSet := stringslice.Set(claudeAi)

	type pluginKey struct{ plugin, mcp string }
	pluginPairs := map[pluginKey]bool{} // value: enabled
	for mcp, srcs := range pluginMCPs {
		for _, s := range srcs {
			pn, _ := config.ParsePluginID(s.PluginID)
			pluginPairs[pluginKey{pn, mcp}] = s.Enabled
		}
	}

	var out Overrides
	for _, k := range overrides {
		src, name, pluginName := config.ParseOverrideKey(k)
		switch src {
		case config.SourcePlugin:
			if enabled, ok := pluginPairs[pluginKey{pluginName, name}]; ok {
				if enabled {
					out.PluginActive = append(out.PluginActive, k)
				} else {
					out.PluginDisabled = append(out.PluginDisabled, k)
				}
				continue
			}
			out.OrphanPlugin = append(out.OrphanPlugin, k)
		case config.SourceClaude:
			if claudeSet[k] {
				out.ClaudeAi = append(out.ClaudeAi, k)
			} else {
				out.OrphanStdio = append(out.OrphanStdio, k)
			}
		default: // SourceUnknown i.e. plain name
			switch {
			case userSet[name] || localSet[name]:
				out.StdioLive = append(out.StdioLive, k)
			case stashSet[name]:
				out.StashGhost = append(out.StashGhost, k)
			default:
				out.OrphanStdio = append(out.OrphanStdio, k)
			}
		}
	}
	return out
}

// PluralY returns "y" for 1 and "ies" otherwise — for printing "entr{y,ies}".
func PluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
