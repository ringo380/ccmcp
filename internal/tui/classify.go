package tui

import (
	"github.com/ringo380/ccmcp/internal/config"
)

// classifiedOverrides groups every entry in a project's disabledMcpServers into the
// bucket that explains WHY it's there. The summary tab uses this to render counts;
// `ccmcp mcp prune` uses the same logic to decide what's safe to remove.
//
// Buckets (see plan for rationale):
//
//	pluginActive    — "plugin:X:Y" where X is enabled globally (legit per-project override)
//	pluginDisabled  — "plugin:X:Y" where X is installed but globally off (legit but informational)
//	claudeai        — "claude.ai Z" where Z is in claudeAiMcpEverConnected
//	stdioLive       — plain name matching a live user/local/project/stash source
//	stashGhost      — plain name matching ONLY a stash entry (override left over from pre-stash days)
//	orphanPlugin    — "plugin:X:Y" where X isn't installed anywhere (bucket 3)
//	orphanStdio     — plain name with no source found (bucket 4)
type classifiedOverrides struct {
	pluginActive   []string
	pluginDisabled []string
	claudeai       []string
	stdioLive      []string
	stashGhost     []string
	orphanPlugin   []string
	orphanStdio    []string
}

// classifyOverrides buckets a project's disabledMcpServers list. Pure function — no I/O —
// so it's trivial to unit-test. Callers pass in the pre-loaded config views they already
// have on hand; nothing here reopens any file.
func classifyOverrides(
	overrides []string,
	userMCPs []string,
	localMCPs []string,
	claudeAi []string, // full "claude.ai Name" strings
	stashedNames []string,
	pluginMCPs map[string][]config.PluginMCPSource,
	installed *config.InstalledPlugins,
) classifiedOverrides {
	userSet := map[string]bool{}
	for _, n := range userMCPs {
		userSet[n] = true
	}
	localSet := map[string]bool{}
	for _, n := range localMCPs {
		localSet[n] = true
	}
	stashSet := map[string]bool{}
	for _, n := range stashedNames {
		stashSet[n] = true
	}
	claudeSet := map[string]bool{}
	for _, n := range claudeAi {
		claudeSet[n] = true
	}

	// Plugin lookup by (pluginName, mcpName) → enabled?
	type pluginKey struct{ plugin, mcp string }
	pluginPairs := map[pluginKey]bool{} // value: enabled
	for mcp, srcs := range pluginMCPs {
		for _, s := range srcs {
			pn, _ := config.ParsePluginID(s.PluginID)
			pluginPairs[pluginKey{pn, mcp}] = s.Enabled
		}
	}

	var out classifiedOverrides
	for _, k := range overrides {
		src, name, pluginName := config.ParseOverrideKey(k)
		switch src {
		case config.SourcePlugin:
			if enabled, ok := pluginPairs[pluginKey{pluginName, name}]; ok {
				if enabled {
					out.pluginActive = append(out.pluginActive, k)
				} else {
					out.pluginDisabled = append(out.pluginDisabled, k)
				}
				continue
			}
			// Plugin not represented in pluginMCPs. Is it at least installed?
			if installed != nil && len(installed.ByName(pluginName)) > 0 {
				// Installed but doesn't register this name — still bucket 3 (orphan).
				out.orphanPlugin = append(out.orphanPlugin, k)
			} else {
				out.orphanPlugin = append(out.orphanPlugin, k)
			}
		case config.SourceClaude:
			if claudeSet[k] {
				out.claudeai = append(out.claudeai, k)
			} else {
				out.orphanStdio = append(out.orphanStdio, k)
			}
		default: // SourceUnknown i.e. plain name
			switch {
			case userSet[name] || localSet[name]:
				out.stdioLive = append(out.stdioLive, k)
			case stashSet[name]:
				out.stashGhost = append(out.stashGhost, k)
			default:
				out.orphanStdio = append(out.orphanStdio, k)
			}
		}
	}
	return out
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
