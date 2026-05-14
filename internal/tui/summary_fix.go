package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ringo380/ccmcp/internal/classify"
)

// summaryCat tags a summary row by issue category so fix proposals know what
// to do. Non-fixable rows (titles, plain stat lines) use catNone and are
// skipped during cursor navigation.
type summaryCat int

const (
	catNone summaryCat = iota
	catOrphanPlugin
	catOrphanStdio
	catStashGhost
	catStashRedundantWithUser
	catStashGhostedByPlugin
	catUserDupPlugin
	catStaleMcpjson
	catDuplicateLoad
	catPluginEnabledNotInstalled
	catPluginInstalledNotEnabled
	catSlashConflict
)

// summaryRow is the unit of cursor navigation in the Summary tab. Display rows
// (titles, blank separators, summary stats) carry cat=catNone; fixable issue
// rows carry the category + the key needed to build a fix proposal.
type summaryRow struct {
	text    string     // already-styled text rendered into the body
	cat     summaryCat // catNone for non-fixable display rows
	key     string     // override key, MCP name, or plugin id depending on cat
	project string     // per-project rows: the affected project path
}

func (r summaryRow) fixable() bool { return r.cat != catNone }

// buildSummaryFixProposal returns a fix proposal for the selected summary row,
// or (nil, false) if the row's category has no automated fix yet.
func buildSummaryFixProposal(r summaryRow, st *state) (*fixProposal, bool) {
	switch r.cat {
	case catOrphanPlugin, catOrphanStdio, catStashGhost:
		key := r.key
		project := r.project
		return &fixProposal{
			summary: fmt.Sprintf("Remove orphan override %q", key),
			kind:    fixInMemory,
			previewLines: []string{
				"Remove key from per-project disabledMcpServers:",
				"",
				"  project: " + project,
				"  key:     " + key,
				"",
				labelForCat(r.cat),
				"",
				"Press w to save, or Q to discard.",
			},
			applyFn: func(s *state) (string, error) {
				if !s.cj.RemoveProjectDisabledMcpServer(project, key) {
					return "", fmt.Errorf("key %q already gone — refresh with r", key)
				}
				s.dirtyClaude = true
				return "removed " + key + " from " + project + " (press w to save)", nil
			},
		}, true

	case catStashRedundantWithUser, catStashGhostedByPlugin:
		name := r.key
		return &fixProposal{
			summary: fmt.Sprintf("Drop stash entry %q", name),
			kind:    fixInMemory,
			previewLines: []string{
				"Drop entry from ~/.claude-mcp-stash.json:",
				"",
				"  name: " + name,
				"",
				labelForCat(r.cat),
				"",
				"Press w to save, or Q to discard.",
			},
			applyFn: func(s *state) (string, error) {
				if !s.stash.Delete(name) {
					return "", fmt.Errorf("stash entry %q already gone — refresh with r", name)
				}
				s.dirtyStash = true
				return "dropped stash entry " + name + " (press w to save)", nil
			},
		}, true

	case catPluginEnabledNotInstalled:
		id := r.key
		return &fixProposal{
			summary: fmt.Sprintf("Disable missing plugin %q", id),
			kind:    fixInMemory,
			previewLines: []string{
				"Set enabledPlugins[\"" + id + "\"] = false in ~/.claude/settings.json:",
				"",
				"The plugin is referenced but not installed on disk.",
				"Disabling stops Claude Code from warning on load.",
				"To install it instead, use the Plugins tab.",
				"",
				"Press w to save, or Q to discard.",
			},
			applyFn: func(s *state) (string, error) {
				s.settings.SetPluginEnabled(id, false)
				s.dirtySettings = true
				return "disabled missing plugin " + id + " (press w to save)", nil
			},
		}, true

	case catUserDupPlugin:
		name := r.key
		prompt := fmt.Sprintf(
			"In ~/.claude.json, the user-scope MCP server %q is also registered by an installed plugin "+
				"(plugin-shipped MCPs auto-load when the plugin is enabled). Two copies will try to load and "+
				"may collide. Move the user-scope entry to ~/.claude-mcp-stash.json by stashing it — keep the "+
				"plugin source as the active definition. If you have user-scope configuration overrides "+
				"(args, env vars) that the plugin doesn't ship, preserve them in the stashed copy so they're "+
				"available if the user un-stashes later. Do not edit any other MCP server entries.",
			name,
		)
		return &fixProposal{
			summary:   fmt.Sprintf("Stash user-scope %q (plugin provides it)", name),
			kind:      fixClaudeCLI,
			target:    st.paths.ClaudeJSON,
			cliPrompt: prompt,
			cliArgs:   claudeFixArgs(prompt),
		}, true

	case catStaleMcpjson:
		name := r.key
		project := r.project
		settingsPath := filepath.Join(project, ".claude", "settings.json")
		prompt := fmt.Sprintf(
			"In %s, the enabledMcpjsonServers or disabledMcpjsonServers list references %q but %s/.mcp.json no "+
				"longer defines that server. Remove the stale name from whichever list it appears in. Preserve "+
				"all other entries verbatim. Do not edit %s/.mcp.json.",
			settingsPath, name, project, project,
		)
		return &fixProposal{
			summary:   fmt.Sprintf("Clean stale .mcp.json ref %q", name),
			kind:      fixClaudeCLI,
			target:    settingsPath,
			cliPrompt: prompt,
			cliArgs:   claudeFixArgs(prompt),
		}, true

	case catDuplicateLoad:
		name := r.key
		prompt := fmt.Sprintf(
			"In ~/.claude.json, the MCP server %q is defined at BOTH user scope (top-level mcpServers) and "+
				"project scope (projects[<this project>].mcpServers). The same MCP will load twice for this "+
				"project, which can cause duplicate tool registration or env conflicts. Decide which scope it "+
				"should live in (project scope wins for per-project workflows; user scope for cross-project "+
				"defaults), and remove the entry from the other scope. Preserve the entry's full configuration "+
				"on the winning scope. Do not touch any other MCP servers.",
			name,
		)
		return &fixProposal{
			summary:   fmt.Sprintf("Resolve duplicate-load %q", name),
			kind:      fixClaudeCLI,
			target:    st.paths.ClaudeJSON,
			cliPrompt: prompt,
			cliArgs:   claudeFixArgs(prompt),
		}, true

	case catPluginInstalledNotEnabled:
		id := r.key
		prompt := fmt.Sprintf(
			"In ~/.claude/settings.json, register the plugin %q that is already installed under "+
				"~/.claude/plugins/ but missing from the enabledPlugins map. Inspect "+
				"~/.claude/plugins/installed_plugins.json to confirm the correct marketplace suffix (the entry "+
				"keys take the form \"<id>@<marketplace>\"), then add it to enabledPlugins set to true. "+
				"Preserve every other plugin entry verbatim.",
			id,
		)
		return &fixProposal{
			summary:   fmt.Sprintf("Register installed plugin %q", id),
			kind:      fixClaudeCLI,
			target:    st.paths.SettingsJSON,
			cliPrompt: prompt,
			cliArgs:   claudeFixArgs(prompt),
		}, true

	case catSlashConflict:
		name := r.key
		prompt := fmt.Sprintf(
			"The slash command /%s is provided by more than one source (plugin skill + plugin command, or "+
				"two different plugins). Inspect ~/.claude/plugins/installed_plugins.json and the plugins' "+
				"directories under ~/.claude/plugins/ to identify which plugins ship a command or skill named "+
				"%q. Decide which definition the user most likely wants to keep, then add the others to a "+
				"skillOverrides or commandOverrides map in ~/.claude/settings.json with the value \"off\" to "+
				"disable them. Do not uninstall any plugins; only override the conflicting command/skill.",
			name, name,
		)
		return &fixProposal{
			summary:   fmt.Sprintf("Resolve slash conflict /%s", name),
			kind:      fixClaudeCLI,
			target:    st.paths.SettingsJSON,
			cliPrompt: prompt,
			cliArgs:   claudeFixArgs(prompt),
		}, true
	}
	return nil, false
}

func labelForCat(c summaryCat) string {
	switch c {
	case catOrphanPlugin:
		return "Reason: plugin not installed on disk (no source provides this MCP)."
	case catOrphanStdio:
		return "Reason: no MCP definition found in any scope or stash."
	case catStashGhost:
		return "Reason: name appears only in stash; override is redundant."
	case catStashRedundantWithUser:
		return "Reason: same name is active in user scope; stash copy is redundant."
	case catStashGhostedByPlugin:
		return "Reason: an enabled plugin provides this MCP; stash copy is redundant."
	}
	return ""
}

// buildSummaryReviewPrompt assembles the LLM prompt used when the user
// triggers `l` on a Summary row. Asks claude --print to read the relevant
// config files and propose a fix narrative without applying it.
func buildSummaryReviewPrompt(r summaryRow, st *state) (string, bool) {
	if r.cat == catNone {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b,
		"You are reviewing a ccmcp Summary-tab finding. Read ~/.claude.json, ~/.claude/settings.json, "+
			"~/.claude-mcp-stash.json (if present), and ~/.claude/plugins/installed_plugins.json so you "+
			"have full context. Do NOT edit any files in this response.\n\n",
	)
	fmt.Fprintf(&b, "Finding: %s\n", summarizeRow(r))
	if r.key != "" {
		fmt.Fprintf(&b, "Affected name/key: %s\n", r.key)
	}
	if r.project != "" {
		fmt.Fprintf(&b, "Project: %s\n", r.project)
	}
	fmt.Fprintf(&b, "\nIn under 200 words, answer:\n")
	fmt.Fprintf(&b, "  1. Is this actually a problem the user should fix? (yes/no + one-sentence why)\n")
	fmt.Fprintf(&b, "  2. What is the recommended fix? (concrete steps)\n")
	fmt.Fprintf(&b, "  3. What could go wrong if applied blindly?\n")
	return b.String(), true
}

func summarizeRow(r summaryRow) string {
	switch r.cat {
	case catOrphanPlugin:
		return "Orphan plugin override (plugin not installed)"
	case catOrphanStdio:
		return "Orphan stdio override (no source provides this MCP)"
	case catStashGhost:
		return "Stash-ghost override (name appears only in stash)"
	case catStashRedundantWithUser:
		return "Stash entry redundant with user scope"
	case catStashGhostedByPlugin:
		return "Stash entry ghosted by enabled plugin"
	case catUserDupPlugin:
		return "User-scope MCP duplicates plugin-provided MCP"
	case catStaleMcpjson:
		return "Stale .mcp.json allow/deny reference"
	case catDuplicateLoad:
		return "MCP defined in both user AND project scope (loads twice)"
	case catPluginEnabledNotInstalled:
		return "Plugin enabled in settings but not installed on disk"
	case catPluginInstalledNotEnabled:
		return "Plugin installed on disk but not registered in settings"
	case catSlashConflict:
		return "Slash-command name collision across plugins/skills"
	}
	return "(unknown)"
}

// pruneOrphanCount returns the count of recoverable orphans (plugin + stdio).
// Used by the Summary cleanup-hint section that still surfaces `p` to prune.
func pruneOrphanCount(c *classify.Overrides) int {
	return len(c.OrphanPlugin) + len(c.OrphanStdio)
}
