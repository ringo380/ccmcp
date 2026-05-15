package tui

import (
	"fmt"
	"strings"
)

// helpView renders a full-screen legend explaining every badge, mark, and key
// binding. It's purely read-only — toggled on/off by the `?` key handled in
// the parent model. Content is hand-rolled (rather than using bubbles/help)
// because the legend's main value is the badge-and-mark table, which doesn't
// fit the bubbles/help one-key-one-description model.
func renderHelp(width int) string {
	var b strings.Builder

	section := func(title string) {
		b.WriteString("\n")
		b.WriteString(styleTitle.Render(title))
		b.WriteString("\n")
	}
	row := func(left, right string) {
		b.WriteString("  ")
		b.WriteString(styleOK.Render(left))
		b.WriteString("  ")
		b.WriteString(styleDim.Render(right))
		b.WriteString("\n")
	}
	kbd := func(key, desc string) {
		b.WriteString(fmt.Sprintf("  %-18s  %s\n", styleBadge.Render(key), styleDim.Render(desc)))
	}

	b.WriteString(styleTitle.Render("ccmcp — Help"))
	b.WriteString("  ")
	b.WriteString(styleDim.Render("(press ? or esc to close)"))
	b.WriteString("\n")

	section("Source badges")
	row("[u]", "user scope — ~/.claude.json#/mcpServers (loads in every project)")
	row("[l]", "local scope — ~/.claude.json#/projects[<cwd>]/mcpServers (this dir only)")
	row("[p]", "project — ./.mcp.json (shared, git-tracked)")
	row("[P]", "plugin — bundled .mcp.json inside an enabled plugin")
	row("[@]", "claude.ai — OAuth remote integration")
	row("[s]", "stash — ~/.claude-mcp-stash.json (parked, not loaded anywhere)")
	row("[?]", "unknown — in disabledMcpServers but no known source provides it")

	section("Row marks")
	row("[x]", "effective: will load in this project (any enabling source)")
	row("[~]", "effective: disabled here via per-project override (disabledMcpServers)")
	row("[ ]", "not active in this scope (or, in effective view: not loaded by any source)")
	row("[!]", "project scope: in ./.mcp.json deny-list (or excluded from allow-list)")

	section("MCPs tab")
	kbd("space", "toggle current row in the active scope")
	kbd("A / N", "bulk enable / disable every visible row (respects filter)")
	kbd("S", "stash current row (or unstash if already in stash)")
	kbd("m", "move MCP config between scopes — opens a picker:")
	kbd("  └ u / l / s", "  pick target user / local / stash (esc to cancel)")
	kbd("s", "cycle scope: effective → local → user → project → stash")
	kbd("/", "filter by substring (enter to lock, esc to cancel)")
	kbd("c", "clear filter")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom; pgup/pgdn page")

	section("Plugins tab")
	kbd("space", "toggle plugin enabled/disabled")
	kbd("A / N", "bulk enable / disable every visible plugin")
	kbd("U", "update current plugin (re-fetch source)")
	kbd("B", "bulk update — re-fetch every installed plugin")
	kbd("F", "show last bulk-update failures (stderr + hint; R retries one)")
	kbd("I", "browse marketplace plugins (sub-view; I again to install)")
	kbd("x", "remove (two-step confirm)")
	kbd("R", "refresh update-available probe")
	kbd("f", "cycle filter: all → enabled → disabled")
	kbd("/", "filter by substring; c clears")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom")
	kbd("↑", "shown next to outdated plugins (newer upstream version available)")

	section("Marketplaces tab")
	kbd("a", "add marketplace (multi-step prompt: name → source → repo/path)")
	kbd("u", "git pull current marketplace")
	kbd("B", "bulk update — git pull every cloned marketplace")
	kbd("x", "remove (two-step confirm; also deletes the clone dir)")
	kbd("I", "browse plugins in current marketplace (hint to Plugins tab)")
	kbd("R", "refresh update-available probe")
	kbd("/", "filter by substring; c clears")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom")

	section("Discover tab")
	kbd("enter", "drill in: marketplace → plugin list → preview-clone + conflict report")
	kbd("b / esc", "go back one level")
	kbd("r", "force-refresh discovery cache")
	kbd("/", "filter by substring; c clears")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom")

	section("Skills tab")
	kbd("space", "toggle skill enabled/disabled (writes skillOverrides)")
	kbd("A / N", "bulk enable / disable every visible skill")
	kbd("/", "filter by substring; c clears")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom")

	section("Agents tab")
	kbd("/", "filter by substring; c clears")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom")
	kbd("(CRUD)", "use CLI: ccmcp agent new|move|rm|show")

	section("Commands tab")
	kbd("!", "toggle conflicts-only view (shows ⚠ rows)")
	kbd("r", "resolve conflict under cursor (skill off / ignore)")
	kbd("R", "bulk-resolve every visible conflict (skill off all / ignore all)")
	kbd("/", "filter by substring; c clears")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom")

	section("Profiles tab")
	kbd("enter / space", "apply profile (replaces current project's MCPs)")
	kbd("n", "create profile from current project")
	kbd("d", "delete profile")
	kbd("j/k or ↑/↓", "navigate")

	section("Summary tab")
	kbd("j/k or ↑/↓", "navigate fixable issues; ↑/↓ past the ends scrolls")
	kbd("pgup / pgdn", "page")
	kbd("g / home", "jump to top")
	kbd("f", "preview a fix for the selected issue (in-place or via claude CLI)")
	kbd("F", "bulk-fix all issues in the cursor's category via claude CLI")
	kbd("l", "run LLM review on the selected issue (claude CLI required)")
	kbd("y / n / esc", "approve / cancel in confirm panel; u also reverts a CLI fix")
	kbd("p", "bulk-prune orphan overrides (legacy; press twice to confirm)")

	section("Doctor tab")
	kbd("r", "re-run lint checks")
	kbd("l", "run one bundled LLM review across CLAUDE.md + MEMORY.md (Haiku)")
	kbd("a", "apply the bundled review back to disk (single Claude call)")
	kbd("j/k or ↑/↓", "scroll; g/G top/bottom")
	kbd("f", "preview a fix for the selected issue (programmatic when possible)")
	kbd("F", "bulk-fix every issue sharing the cursor's code in one keystroke")
	kbd("y / n", "approve / reject the previewed fix (in confirm panel)")
	kbd("u", "revert a CLI fix from its on-disk snapshot (in post-review panel)")

	section("Global")
	kbd("tab / shift+tab", "cycle tabs")
	kbd("1–9, 0", "jump to MCPs / Plugins / Marketplaces / Discover / Skills / Agents / Commands / Profiles / Summary / Doctor")
	kbd("w", "save staged changes (atomic + backup)")
	kbd("q", "quit (warns if unsaved)")
	kbd("Q", "force quit, discard pending changes")
	kbd("?", "toggle this help")

	return b.String()
}
