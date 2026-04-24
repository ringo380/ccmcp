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
	kbd("f", "cycle filter: all → enabled → disabled")
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
	kbd("/", "filter by substring; c clears")
	kbd("j/k or ↑/↓", "navigate; g/G top/bottom")

	section("Profiles tab")
	kbd("enter / space", "apply profile (replaces current project's MCPs)")
	kbd("n", "create profile from current project")
	kbd("d", "delete profile")
	kbd("j/k or ↑/↓", "navigate")

	section("Summary tab")
	kbd("j/k or ↑/↓", "scroll")
	kbd("pgup / pgdn", "page")
	kbd("g / home", "jump to top")

	section("Doctor tab")
	kbd("r", "re-run lint checks")
	kbd("j/k or ↑/↓", "scroll; g/G top/bottom")

	section("Global")
	kbd("tab / shift+tab", "cycle tabs")
	kbd("1–8", "jump to MCPs / Plugins / Skills / Agents / Commands / Profiles / Summary / Doctor")
	kbd("w", "save staged changes (atomic + backup)")
	kbd("q", "quit (warns if unsaved)")
	kbd("Q", "force quit, discard pending changes")
	kbd("?", "toggle this help")

	return b.String()
}
