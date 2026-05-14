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
	// Asset-lint categories (CC 2.1.141 compliance — see internal/doctor/asset_lint.go).
	// All use fixClaudeCLI to let Claude rewrite content while preserving meaning.
	catSkillNameInvalid    // ^[a-z0-9-]+$ violation: rename file AND directory
	catSkillNameTooLong    // >64 chars: rename file AND directory
	catSkillDescTooLong    // combined description+when_to_use too long: rewrite frontmatter
	catAgentDescTooLong    // agent description too long
	catCommandDescTooLong  // command description too long
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
// or (nil, false) if the row's category has no automated fix yet. Stamps the
// row's category onto the proposal so the post-fix asset-cache invalidation
// can decide whether the change could have affected skill/agent/command state.
func buildSummaryFixProposal(r summaryRow, st *state) (*fixProposal, bool) {
	p, ok := buildSummaryFixProposalImpl(r, st)
	if ok && p != nil {
		p.cat = r.cat
	}
	return p, ok
}

func buildSummaryFixProposalImpl(r summaryRow, st *state) (*fixProposal, bool) {
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

	case catSkillNameInvalid, catSkillNameTooLong:
		// r.key carries the SKILL.md path so revert/snapshot can find the file.
		// The actual rename is destructive (mv + frontmatter edit) so the prompt
		// instructs Claude to also rename the directory and pick a unique slug.
		file := r.key
		dir := filepath.Dir(file)
		reason := "use only lowercase letters, digits, and hyphens"
		if r.cat == catSkillNameTooLong {
			reason = "shrink it to 64 characters or fewer while keeping it descriptive"
		}
		prompt := fmt.Sprintf(
			"The skill at %s has an invalid `name:` value per Claude Code 2.1.141 (name must match "+
				"^[a-z0-9-]+$ with a 64-character hard cap). Read %s and:\n"+
				"  1. Choose a new slug for `name:` that follows the rule (%s). Ensure no other "+
				"skill in ~/.claude/skills/ or under installed plugins already uses it.\n"+
				"  2. Update the frontmatter `name:` field in %s.\n"+
				"  3. Rename the containing directory from its current basename to the new slug. "+
				"Use Bash `mv %s <parent>/<new-slug>` so Claude Code's loader picks the skill up "+
				"under its new directory name. Do not touch any other skills.",
			file, file, reason, file, dir,
		)
		return &fixProposal{
			summary:   "Rename skill " + filepath.Base(dir),
			kind:      fixClaudeCLI,
			target:    file,
			cliPrompt: prompt,
			cliArgs:   claudeAssetFixArgs(prompt, permRename),
		}, true

	case catSkillDescTooLong:
		file := r.key
		prompt := fmt.Sprintf(
			"The skill at %s has a frontmatter `description:` (combined with `when_to_use:` when "+
				"present) that exceeds the 1,536-character display limit documented for Claude "+
				"Code 2.1.141. Content past the cap is silently dropped from skill listings, so "+
				"the model never sees the trailing portion. Read %s and rewrite the description "+
				"+ when_to_use so:\n"+
				"  - The combined length is at most 1,200 characters (a comfortable margin).\n"+
				"  - The key use case appears first.\n"+
				"  - The original meaning is preserved; trim filler, not signal.\n"+
				"Edit only the frontmatter block. Do not change the skill body, the `name:` "+
				"field, or any other skill.",
			file, file,
		)
		return &fixProposal{
			summary:   "Shorten skill description " + filepath.Base(filepath.Dir(file)),
			kind:      fixClaudeCLI,
			target:    file,
			cliPrompt: prompt,
			cliArgs:   claudeAssetFixArgs(prompt, permDescription),
		}, true

	case catAgentDescTooLong:
		file := r.key
		prompt := fmt.Sprintf(
			"The agent at %s has a frontmatter `description:` exceeding the 1,536-character "+
				"display limit. Read %s and rewrite the description so it is at most 1,200 "+
				"characters, leading with the agent's primary purpose. Preserve original "+
				"meaning. Edit only the frontmatter description field; do not change name, "+
				"model, or the agent body.",
			file, file,
		)
		return &fixProposal{
			summary:   "Shorten agent description " + filepath.Base(file),
			kind:      fixClaudeCLI,
			target:    file,
			cliPrompt: prompt,
			cliArgs:   claudeAssetFixArgs(prompt, permDescription),
		}, true

	case catCommandDescTooLong:
		file := r.key
		prompt := fmt.Sprintf(
			"The slash command at %s has a frontmatter `description:` over 500 characters, "+
				"which degrades the command palette UX. Read %s and shorten the description to "+
				"under 400 characters, keeping the action verb-led summary. Edit only the "+
				"frontmatter description; leave the command body intact.",
			file, file,
		)
		return &fixProposal{
			summary:   "Shorten command description " + filepath.Base(file),
			kind:      fixClaudeCLI,
			target:    file,
			cliPrompt: prompt,
			cliArgs:   claudeAssetFixArgs(prompt, permDescription),
		}, true
	}
	return nil, false
}

// permKind selects which --allowedTools set Claude is granted for an asset fix.
// Description rewrites only need Edit/Write/Read; slug renames additionally need
// Glob+Grep (to verify the new slug isn't already taken across the skills
// directory) and Bash (to `mv` the containing directory).
type permKind int

const (
	permDescription permKind = iota
	permRename
)

// claudeAssetFixArgs returns the Claude CLI args for an asset-fix prompt. The
// permission profile widens only when the task genuinely needs more — bulk-fix
// reuses these via buildBulkFixPrompt so the same scoping rules apply.
func claudeAssetFixArgs(prompt string, perm permKind) []string {
	tools := "Edit,Write,Read"
	if perm == permRename {
		tools = "Edit,Write,Read,Glob,Grep,Bash"
	}
	return []string{"--allowedTools", tools, "--permission-mode", "acceptEdits", "--print", prompt}
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
	case catSkillNameInvalid:
		return "Skill name violates ^[a-z0-9-]+$ (CC 2.1.141 hard requirement)"
	case catSkillNameTooLong:
		return "Skill name exceeds 64-character hard cap"
	case catSkillDescTooLong:
		return "Skill description+when_to_use exceeds 1,536-char display limit"
	case catAgentDescTooLong:
		return "Agent description exceeds 1,536-char display limit"
	case catCommandDescTooLong:
		return "Command description exceeds 500-char soft limit"
	}
	return "(unknown)"
}

// bulkFixCategory reports whether a category supports the F bulk-fix flow. In-memory
// categories are excluded — `p` (prune) already handles them in one shot, and bulk
// LLM rewrites only make sense for fixClaudeCLI categories.
func bulkFixCategory(c summaryCat) bool {
	switch c {
	case catUserDupPlugin, catStaleMcpjson, catDuplicateLoad, catPluginInstalledNotEnabled,
		catSlashConflict, catSkillNameInvalid, catSkillNameTooLong, catSkillDescTooLong,
		catAgentDescTooLong, catCommandDescTooLong:
		return true
	}
	return false
}

// permForCategory returns the permission profile to grant Claude when bulk-fixing
// the given category. Matches the per-row claudeAssetFixArgs choice.
func permForCategory(c summaryCat) permKind {
	switch c {
	case catSkillNameInvalid, catSkillNameTooLong:
		return permRename
	}
	return permDescription
}

// pruneOrphanCount returns the count of recoverable orphans (plugin + stdio).
// Used by the Summary cleanup-hint section that still surfaces `p` to prune.
func pruneOrphanCount(c *classify.Overrides) int {
	return len(c.OrphanPlugin) + len(c.OrphanStdio)
}

// buildBulkFixProposal collects every row sharing the cursor's category and
// produces a single fixClaudeCLI proposal that hands all targets to Claude in
// one prompt. Returns (nil, false) when the cursor's category is not bulk-fixable
// (in-memory or unknown). The proposal's `target` is set to the first file so the
// existing snapshot/revert pipeline still has a primary handle; additional files
// are passed via the prompt body and listed in `previewLines` for the user.
func buildBulkFixProposal(cursor summaryRow, all []summaryRow, st *state) (*fixProposal, []string, bool) {
	if !bulkFixCategory(cursor.cat) {
		return nil, nil, false
	}
	var targets []summaryRow
	for _, r := range all {
		if r.cat == cursor.cat {
			targets = append(targets, r)
		}
	}
	if len(targets) == 0 {
		return nil, nil, false
	}
	// File path(s) Claude will touch — used for snapshot/revert and the
	// confirmation modal preview. For categories whose `key` IS the file path
	// (asset-lint findings) we use the keys directly; for config-edit categories
	// we point at the appropriate config file.
	var files []string
	var primary string
	switch cursor.cat {
	case catSkillNameInvalid, catSkillNameTooLong, catSkillDescTooLong,
		catAgentDescTooLong, catCommandDescTooLong:
		for _, t := range targets {
			files = append(files, t.key)
		}
		primary = files[0]
	case catUserDupPlugin, catDuplicateLoad:
		primary = st.paths.ClaudeJSON
		files = []string{primary}
	case catStaleMcpjson:
		primary = filepath.Join(cursor.project, ".claude", "settings.json")
		files = []string{primary}
	case catPluginInstalledNotEnabled, catSlashConflict:
		primary = st.paths.SettingsJSON
		files = []string{primary}
	}
	prompt := buildBulkPrompt(cursor.cat, targets, st)
	preview := []string{
		fmt.Sprintf("Bulk fix: %s", summarizeRow(cursor)),
		fmt.Sprintf("  %d item(s) in this category will be addressed in one Claude run", len(targets)),
		"",
		"Files Claude may edit:",
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f] {
			continue
		}
		seen[f] = true
		preview = append(preview, "  "+f)
	}
	preview = append(preview,
		"",
		fmt.Sprintf("Permissions: %s", permLabel(permForCategory(cursor.cat))),
		"",
		"y to apply, n/esc to cancel.",
	)
	return &fixProposal{
		summary:      fmt.Sprintf("Bulk-fix %d %s", len(targets), summarizeRow(cursor)),
		kind:         fixClaudeCLI,
		target:       primary,
		cliPrompt:    prompt,
		cliArgs:      claudeAssetFixArgs(prompt, permForCategory(cursor.cat)),
		previewLines: preview,
		cat:          cursor.cat,
	}, files, true
}

// categoryAffectsAssets reports whether a fix in this category could change
// the output of skills/agents/commands Discover or the asset-lint passes —
// i.e. whether the cached asset state on summaryView must be dropped after the
// fix lands. False for fixes that only touch ~/.claude.json mcpServers, the
// stash, or per-project disabledMcpServers — those have no asset-side effect.
func categoryAffectsAssets(c summaryCat) bool {
	switch c {
	case catPluginEnabledNotInstalled,    // flips enabledPlugins → Discover plugin scope changes
		catPluginInstalledNotEnabled,    // same
		catSlashConflict,                // writes skillOverrides → skill enablement view changes
		catSkillNameInvalid,             // renames skill dir → Discover output changes
		catSkillNameTooLong,             // same
		catSkillDescTooLong,             // rewrites SKILL.md frontmatter → lint result changes
		catAgentDescTooLong,             // same for agents
		catCommandDescTooLong:           // same for commands
		return true
	}
	return false
}

func permLabel(p permKind) string {
	if p == permRename {
		return "Edit,Write,Read,Glob,Grep,Bash (slug renames need uniqueness check + `mv`)"
	}
	return "Edit,Write,Read"
}

// buildBulkPrompt assembles the Claude prompt for a bulk-fix of `targets` (all in
// the same category). Each prompt is tailored to the category's constraints; the
// shape is "intro + list of items + constraints + scope guardrails".
func buildBulkPrompt(cat summaryCat, targets []summaryRow, st *state) string {
	var b strings.Builder
	switch cat {
	case catSkillNameInvalid, catSkillNameTooLong:
		fmt.Fprintf(&b,
			"Multiple skills violate the Claude Code 2.1.141 name rule (^[a-z0-9-]+$, max 64 chars). "+
				"For EACH skill below: read the file, pick a new slug that follows the rule and isn't already "+
				"used by another skill, update the frontmatter `name:`, and rename the containing directory via "+
				"`mv <old-dir> <parent>/<new-slug>` so Claude Code's loader picks the skill up under its new "+
				"directory name. Use Glob/Grep against ~/.claude/skills/ and the installed-plugins directories "+
				"to verify uniqueness before each rename. Do not edit unrelated skills.\n\n",
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catSkillDescTooLong:
		fmt.Fprintf(&b,
			"Multiple skill SKILL.md files have a frontmatter `description:` (combined with `when_to_use:` "+
				"when present) over the 1,536-character display limit documented for Claude Code 2.1.141. "+
				"Content past the cap is silently dropped from skill listings. For EACH file below: read the "+
				"current description, then rewrite it (and when_to_use if present) so the combined length is "+
				"at most 1,200 characters, leading with the primary use case. Preserve meaning; trim filler. "+
				"Edit only the frontmatter block; do not touch the body, the `name:` field, or any unrelated "+
				"skills.\n\n",
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catAgentDescTooLong:
		fmt.Fprintf(&b,
			"Multiple agent files have frontmatter `description:` exceeding the 1,536-character display "+
				"limit. For EACH file below: rewrite the description to ≤1,200 characters leading with the "+
				"agent's primary purpose. Preserve meaning. Edit only frontmatter description; leave name, "+
				"model, body, and other agents untouched.\n\n",
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catCommandDescTooLong:
		fmt.Fprintf(&b,
			"Multiple slash command files have a frontmatter `description:` over 500 characters, degrading "+
				"the command palette UX. For EACH file below: shorten the description to under 400 characters "+
				"with a verb-led action summary. Edit only frontmatter description; leave bodies intact and "+
				"do not touch unrelated commands.\n\n",
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catUserDupPlugin:
		fmt.Fprintf(&b,
			"In ~/.claude.json, several user-scope MCP servers are ALSO provided by installed plugins. "+
				"Plugin-shipped MCPs auto-load when the plugin is enabled, so two copies collide. For EACH name "+
				"below, move the user-scope entry into ~/.claude-mcp-stash.json (preserving any user-only "+
				"config overrides) and remove it from the user-scope mcpServers map. Keep the plugin source as "+
				"the active definition. Do not touch any other MCP servers.\n\n",
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catDuplicateLoad:
		fmt.Fprintf(&b,
			"In ~/.claude.json, several MCP servers are defined at BOTH user scope and project scope for "+
				"the current project (%s), which causes them to load twice. For EACH name below, decide which "+
				"scope wins (project scope for per-project workflows; user scope for cross-project defaults), "+
				"and remove the entry from the other scope while preserving the entry's full configuration on "+
				"the winning scope. Do not touch unrelated MCP servers.\n\n",
			st.project,
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catStaleMcpjson:
		settingsPath := filepath.Join(st.project, ".claude", "settings.json")
		fmt.Fprintf(&b,
			"In %s, the enabledMcpjsonServers/disabledMcpjsonServers lists reference MCP names that no "+
				"longer exist in %s/.mcp.json. For EACH stale name below, remove it from whichever list it "+
				"appears in. Preserve all other entries verbatim. Do not edit %s/.mcp.json.\n\n",
			settingsPath, st.project, st.project,
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catPluginInstalledNotEnabled:
		fmt.Fprintf(&b,
			"In ~/.claude/settings.json, register the following plugins that are already installed under "+
				"~/.claude/plugins/ but missing from the enabledPlugins map. Inspect "+
				"~/.claude/plugins/installed_plugins.json to confirm each entry's correct marketplace suffix "+
				"(form: \"<id>@<marketplace>\"), then add them to enabledPlugins set to true. Preserve every "+
				"other plugin entry verbatim.\n\n",
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - %s\n", t.key)
		}
	case catSlashConflict:
		fmt.Fprintf(&b,
			"Multiple slash commands are claimed by more than one source (plugin skill vs plugin command, "+
				"or two different plugins). For EACH conflicting name below: inspect "+
				"~/.claude/plugins/installed_plugins.json and the relevant plugin directories under "+
				"~/.claude/plugins/ to identify the contributors, pick the definition the user most likely "+
				"wants, and add the others to the appropriate skillOverrides/commandOverrides map in "+
				"~/.claude/settings.json with value \"off\". Do NOT uninstall any plugins.\n\n",
		)
		for _, t := range targets {
			fmt.Fprintf(&b, "  - /%s\n", t.key)
		}
	}
	return b.String()
}
