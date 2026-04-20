package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/stringslice"
)

// Scope naming aligns with Claude Code's own terminology. "effective" is the default
// read-only-ish view showing everything that will load in the current project.
const (
	scopeEffective = "effective"
	scopeUser      = "user"
	scopeLocal     = "local"
	scopeProject   = "project" // ./.mcp.json
	scopeStash     = "stash"
)

var scopeDesc = map[string]string{
	scopeEffective: "all MCPs that will load in this project (matches /mcp)",
	scopeUser:      "~/.claude.json#/mcpServers  (global — every project)",
	scopeLocal:     "~/.claude.json > projects  (this dir only, private)",
	scopeProject:   "./.mcp.json  (shared, git-tracked)",
	scopeStash:     "~/.claude-mcp-stash.json  (parked, not active)",
}

var scopeCycle = []string{scopeEffective, scopeLocal, scopeUser, scopeProject, scopeStash}

type mcpView struct {
	st *state

	scope string
	rows  []mcpRow
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool

	moveActive bool
	moveForKey string // RowKey (not just Name — we need the source too)

	flash string
}

// mcpRow represents one (display-name, source) pair. Two rows can share a Name
// if the same name is registered by different sources — this is the correct model
// for Claude Code's world, where e.g. `context7` as a stdio MCP is a different
// entity than `context7` registered by the context7 plugin.
type mcpRow struct {
	Name         string           // display name shown to the user
	Source       config.MCPSource // primary source of this row
	OverrideKey  string           // what to write into disabledMcpServers (empty for stash)
	PluginName   string           // only for SourcePlugin rows (the plugin's name without @mkt)
	PluginIDs    []string         // qualified plugin IDs (e.g. "context7@claude-plugins-official")
	DisabledHere bool             // OverrideKey appears in projects[cwd].disabledMcpServers
	McpjsonDeny  bool             // .mcp.json allow/deny list excludes this row (project-source only)
	Description  string
	Config       any // raw config entry (for move/copy)
}

// RowKey returns a stable identifier for the (source, display-name) pair.
func (r mcpRow) RowKey() string {
	if r.OverrideKey != "" {
		return string(r.Source) + "|" + r.OverrideKey
	}
	return string(r.Source) + "|" + r.Name
}

// --- load / rebuild ---------------------------------------------------------

func newMCPView(st *state) *mcpView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	v := &mcpView{st: st, scope: scopeEffective, filter: ti}
	v.rebuild()
	return v
}

// rebuild scans every source of MCPs and emits one row per (name, source) pair.
// Sources (six): user, local, project (.mcp.json), plugin-sourced, claude.ai, stash.
// Plus a 7th synthetic source, "unknown", for leftover entries in disabledMcpServers
// that don't match any known source — so the user can see and clean them up.
func (v *mcpView) rebuild() {
	disabled := stringslice.Set(v.st.cj.ProjectDisabledMcpServers(v.st.project))
	allow := stringslice.Set(v.st.cj.ProjectMcpjsonEnabled(v.st.project))
	deny := stringslice.Set(v.st.cj.ProjectMcpjsonDisabled(v.st.project))

	rows := []mcpRow{}
	// (source, overrideKey) pairs we've emitted — used to catch unknowns at the end.
	seenKeys := map[string]bool{}

	// 1) user scope
	for name, cfg := range v.st.cj.UserMCPs() {
		key := config.OverrideKey(config.SourceUser, name, "")
		rows = append(rows, mcpRow{
			Name:         name,
			Source:       config.SourceUser,
			OverrideKey:  key,
			DisabledHere: disabled[key],
			Description:  config.DescribeMCP(cfg),
			Config:       cfg,
		})
		seenKeys[key] = true
	}
	// 2) local scope
	for name, cfg := range v.st.cj.ProjectMCPs(v.st.project) {
		key := config.OverrideKey(config.SourceLocal, name, "")
		rows = append(rows, mcpRow{
			Name:         name,
			Source:       config.SourceLocal,
			OverrideKey:  key,
			DisabledHere: disabled[key],
			Description:  config.DescribeMCP(cfg),
			Config:       cfg,
		})
		seenKeys[key] = true
	}
	// 3) project (./.mcp.json)
	if m, err := config.LoadMCPJson(v.st.project + "/.mcp.json"); err == nil {
		for name, cfg := range m.Servers() {
			key := config.OverrideKey(config.SourceProject, name, "")
			denied := deny[name] || (len(allow) > 0 && !allow[name])
			rows = append(rows, mcpRow{
				Name:         name,
				Source:       config.SourceProject,
				OverrideKey:  key,
				McpjsonDeny:  denied,
				DisabledHere: disabled[key],
				Description:  config.DescribeMCP(cfg),
				Config:       cfg,
			})
			seenKeys[key] = true
		}
	}
	// 4) plugin-sourced — one row per (mcpName, pluginName) so e.g. next-devtools (registered
	// by both next-devtools and next-project-starter) shows up twice.
	for name, srcs := range v.st.pluginMCPs {
		for _, s := range srcs {
			pluginName, _ := config.ParsePluginID(s.PluginID)
			key := config.OverrideKey(config.SourcePlugin, name, pluginName)
			rows = append(rows, mcpRow{
				Name:         name,
				Source:       config.SourcePlugin,
				OverrideKey:  key,
				PluginName:   pluginName,
				PluginIDs:    []string{s.PluginID},
				DisabledHere: disabled[key],
				Description:  "via plugin: " + pluginName,
				Config:       s.Config,
			})
			seenKeys[key] = true
		}
	}
	// 5) claude.ai integrations
	for _, full := range v.st.claudeAi {
		if !strings.HasPrefix(full, "claude.ai ") {
			continue
		}
		name := strings.TrimPrefix(full, "claude.ai ")
		key := full // already the full override key
		rows = append(rows, mcpRow{
			Name:         name,
			Source:       config.SourceClaude,
			OverrideKey:  key,
			DisabledHere: disabled[key],
			Description:  "via claude.ai",
		})
		seenKeys[key] = true
	}
	// 6) stash (no override key; can't be disabled per-project because Claude Code doesn't load it)
	for name, cfg := range v.st.stash.Entries() {
		rows = append(rows, mcpRow{
			Name:        name,
			Source:      config.SourceStash,
			Description: config.DescribeMCP(cfg),
			Config:      cfg,
		})
	}
	// 7) unknown — anything in disabledMcpServers we haven't accounted for.
	for k := range disabled {
		if seenKeys[k] {
			continue
		}
		src, name, pluginName := config.ParseOverrideKey(k)
		rows = append(rows, mcpRow{
			Name:         name,
			Source:       src,
			OverrideKey:  k,
			PluginName:   pluginName,
			DisabledHere: true,
			Description:  "(unknown source — in disabledMcpServers but not provided by any known scope)",
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		li, lj := strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)
		if li != lj {
			return li < lj
		}
		// tie-break by source so stable ordering
		return rows[i].Source < rows[j].Source
	})
	v.rows = rows
	if v.index >= len(rows) {
		v.index = 0
	}
}

// isEffective: would Claude Code actually load this row in the current project?
func isEffective(r mcpRow) bool {
	if r.DisabledHere {
		return false
	}
	switch r.Source {
	case config.SourceUser, config.SourceLocal, config.SourcePlugin, config.SourceClaude:
		return true
	case config.SourceProject:
		return !r.McpjsonDeny
	default:
		// stash, unknown — never effective
		return false
	}
}

// --- update (input handling) -----------------------------------------------

func (v *mcpView) update(msg tea.Msg) tea.Cmd {
	if v.filterActive {
		var cmd tea.Cmd
		v.filter, cmd = v.filter.Update(msg)
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "enter", "esc":
				v.filterActive = false
				v.filter.Blur()
			}
		}
		return cmd
	}
	if v.moveActive {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc", "ctrl+c":
				v.moveActive = false
				v.moveForKey = ""
				v.flash = styleDim.Render("move cancelled")
				return nil
			case "u":
				v.doMove(v.moveForKey, scopeUser)
			case "l":
				v.doMove(v.moveForKey, scopeLocal)
			case "s":
				v.doMove(v.moveForKey, scopeStash)
			case "p":
				v.flash = styleWarn.Render("moving into .mcp.json (git-tracked) not yet supported")
				v.moveActive = false
				v.moveForKey = ""
			}
		}
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	visible := v.visibleRows()
	switch key.String() {
	case "up", "k":
		if v.index > 0 {
			v.index--
		}
	case "down", "j":
		if v.index < len(visible)-1 {
			v.index++
		}
	case "g", "home":
		v.index = 0
	case "G", "end":
		v.index = len(visible) - 1
	case "pgup":
		v.index -= 10
		if v.index < 0 {
			v.index = 0
		}
	case "pgdn":
		v.index += 10
		if v.index >= len(visible) {
			v.index = len(visible) - 1
		}
	case "s":
		v.cycleScope()
	case " ":
		if len(visible) == 0 {
			return nil
		}
		v.toggle(visible[v.index])
	case "m":
		if len(visible) == 0 {
			return nil
		}
		v.moveForKey = visible[v.index].RowKey()
		v.moveActive = true
	case "/":
		v.filterActive = true
		v.filter.Focus()
		return textinput.Blink
	case "c":
		v.filter.SetValue("")
		v.rebuild()
	}
	return nil
}

func (v *mcpView) cycleScope() {
	for i, s := range scopeCycle {
		if s == v.scope {
			v.scope = scopeCycle[(i+1)%len(scopeCycle)]
			return
		}
	}
	v.scope = scopeEffective
}

// --- toggle / move ----------------------------------------------------------

func (v *mcpView) toggle(row mcpRow) {
	switch v.scope {
	case scopeEffective:
		v.toggleEffective(row)
		return
	case scopeLocal:
		v.toggleMembership(row, config.SourceLocal)
	case scopeUser:
		v.toggleMembership(row, config.SourceUser)
	case scopeStash:
		v.toggleStash(row)
	case scopeProject:
		v.toggleMcpjsonAllow(row)
	}
	v.rebuild()
}

// toggleEffective: flip the per-project disabledMcpServers entry for this row.
// This is the unified "enable/disable here" action that matches /mcp exactly.
func (v *mcpView) toggleEffective(row mcpRow) {
	// Stash rows can't be toggled in effective view — they don't load anywhere by themselves.
	if row.Source == config.SourceStash {
		v.flash = styleDim.Render("stashed MCPs aren't loaded anywhere — press 'm' to move into user/local/project scope to activate")
		return
	}
	if row.OverrideKey == "" {
		v.flash = styleErr.Render(row.Name + ": no override key (unexpected source)")
		return
	}
	if row.DisabledHere {
		// Remove from disabledMcpServers -> re-enable here.
		if v.st.cj.RemoveProjectDisabledMcpServer(v.st.project, row.OverrideKey) {
			v.st.dirtyClaude = true
			v.flash = styleOK.Render(row.Name + " → re-enabled for this project")
		}
	} else {
		if v.st.cj.AddProjectDisabledMcpServer(v.st.project, row.OverrideKey) {
			v.st.dirtyClaude = true
			v.flash = styleWarn.Render(row.Name + " → disabled for this project only")
		}
	}
	v.rebuild()
}

// toggleMembership adds/removes the MCP from user or local scope.
func (v *mcpView) toggleMembership(row mcpRow, target config.MCPSource) {
	in := (target == config.SourceUser && row.Source == config.SourceUser) ||
		(target == config.SourceLocal && row.Source == config.SourceLocal)
	name := row.Name
	if in {
		if target == config.SourceUser {
			v.st.cj.DeleteUserMCP(name)
		} else {
			v.st.cj.DeleteProjectMCP(v.st.project, name)
		}
		v.flash = styleDim.Render(name + " removed from " + string(target) + " scope")
	} else {
		cfg, ok := pickConfig(v.st, name, row)
		if !ok {
			v.flash = styleErr.Render(name + ": no config found to enable")
			return
		}
		if target == config.SourceUser {
			v.st.cj.SetUserMCP(name, cfg)
		} else {
			v.st.cj.SetProjectMCP(v.st.project, name, cfg)
		}
		v.flash = styleOK.Render(name + " enabled in " + string(target) + " scope")
	}
	v.st.dirtyClaude = true
}

func (v *mcpView) toggleStash(row mcpRow) {
	name := row.Name
	if row.Source == config.SourceStash {
		v.st.stash.Delete(name)
		v.flash = styleDim.Render(name + " removed from stash")
	} else {
		cfg, ok := pickConfig(v.st, name, row)
		if !ok {
			v.flash = styleErr.Render(name + ": no config to stash")
			return
		}
		v.st.stash.Put(name, cfg)
		v.flash = styleOK.Render(name + " parked in stash")
	}
	v.st.dirtyStash = true
}

func (v *mcpView) toggleMcpjsonAllow(row mcpRow) {
	// Only makes sense for SourceProject rows
	if row.Source != config.SourceProject {
		v.flash = styleDim.Render("allow/deny toggling applies to .mcp.json entries only — switch to a different scope")
		return
	}
	name := row.Name
	allow := v.st.cj.ProjectMcpjsonEnabled(v.st.project)
	deny := v.st.cj.ProjectMcpjsonDisabled(v.st.project)
	if stringslice.Contains(allow, name) {
		allow = stringslice.Remove(allow, name)
		deny = stringslice.UniqueAppend(deny, name)
		v.flash = styleDim.Render(name + " moved to .mcp.json deny list")
	} else {
		deny = stringslice.Remove(deny, name)
		allow = stringslice.UniqueAppend(allow, name)
		v.flash = styleOK.Render(name + " added to .mcp.json allow list")
	}
	v.st.cj.SetProjectMcpjsonEnabled(v.st.project, allow)
	v.st.cj.SetProjectMcpjsonDisabled(v.st.project, deny)
	v.st.dirtyClaude = true
}

// doMove copies the row's config into the target scope. For plugin-sourced rows
// the plugin's entry is copied — both will load unless the user separately disables
// the plugin version (via space on the plugin row, or via the Plugins tab).
func (v *mcpView) doMove(rowKey, target string) {
	defer func() {
		v.moveActive = false
		v.moveForKey = ""
	}()
	var row mcpRow
	for _, r := range v.rows {
		if r.RowKey() == rowKey {
			row = r
			break
		}
	}
	if row.Name == "" {
		v.flash = styleErr.Render("move target row disappeared")
		return
	}
	cfg := row.Config
	if cfg == nil {
		c, ok := pickConfig(v.st, row.Name, row)
		if !ok {
			v.flash = styleErr.Render(row.Name + ": no config found to move")
			return
		}
		cfg = c
	}

	// Plugin-sourced MCPs: COPY to the target and warn about duplicate loading.
	// Do NOT remove the plugin's entry (we can't — it's inside the plugin cache dir).
	isPluginCopy := row.Source == config.SourcePlugin

	var removed []string
	// Remove from mutable source scopes that aren't the target (except plugin — immutable).
	if !isPluginCopy {
		if row.Source == config.SourceUser && target != scopeUser {
			v.st.cj.DeleteUserMCP(row.Name)
			removed = append(removed, "user")
			v.st.dirtyClaude = true
		}
		if row.Source == config.SourceLocal && target != scopeLocal {
			v.st.cj.DeleteProjectMCP(v.st.project, row.Name)
			removed = append(removed, "local")
			v.st.dirtyClaude = true
		}
		if row.Source == config.SourceStash && target != scopeStash {
			v.st.stash.Delete(row.Name)
			removed = append(removed, "stash")
			v.st.dirtyStash = true
		}
	}

	// Write into target.
	switch target {
	case scopeUser:
		v.st.cj.SetUserMCP(row.Name, cfg)
		v.st.dirtyClaude = true
	case scopeLocal:
		v.st.cj.SetProjectMCP(v.st.project, row.Name, cfg)
		v.st.dirtyClaude = true
	case scopeStash:
		v.st.stash.Put(row.Name, cfg)
		v.st.dirtyStash = true
	default:
		v.flash = styleErr.Render("unknown move target: " + target)
		return
	}

	if isPluginCopy {
		v.flash = styleWarn.Render(fmt.Sprintf(
			"copied %s from plugin '%s' to %s — both will load; press space on the plugin row to disable per-project, or disable the plugin in the Plugins tab",
			row.Name, row.PluginName, target))
	} else {
		fromStr := "(nowhere)"
		if len(removed) > 0 {
			fromStr = strings.Join(removed, "+")
		}
		v.flash = styleOK.Render(fmt.Sprintf("moved %s: %s → %s", row.Name, fromStr, target))
	}
	v.rebuild()
}

// --- filtering + rendering --------------------------------------------------

func (v *mcpView) visibleRows() []mcpRow {
	q := strings.ToLower(strings.TrimSpace(v.filter.Value()))
	out := make([]mcpRow, 0, len(v.rows))
	for _, r := range v.rows {
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (v *mcpView) render() string {
	visible := v.visibleRows()
	title := fmt.Sprintf("MCPs — scope: %s  %s  (%d shown)", styleBadge.Render(v.scope), styleDim.Render(scopeDesc[v.scope]), len(visible))
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	if v.moveActive {
		b.WriteString(styleWarn.Render(fmt.Sprintf("Move to: [u]ser  [l]ocal  [s]tash  (esc to cancel)")))
		b.WriteString("\n")
	}
	if v.filterActive || v.filter.Value() != "" {
		b.WriteString(v.filter.View() + "\n")
	}

	if len(visible) == 0 {
		b.WriteString(styleDim.Render("  (no entries)"))
		return b.String()
	}
	if v.index >= len(visible) {
		v.index = len(visible) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
	listHeight := v.h - 4
	if listHeight < 5 {
		listHeight = 5
	}
	if v.index < v.top {
		v.top = v.index
	}
	if v.index >= v.top+listHeight {
		v.top = v.index - listHeight + 1
	}
	end := v.top + listHeight
	if end > len(visible) {
		end = len(visible)
	}
	for i := v.top; i < end; i++ {
		row := visible[i]
		line := v.formatRow(row)
		if i == v.index {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	if len(visible) > listHeight {
		b.WriteString(styleDim.Render(fmt.Sprintf("  [%d-%d of %d]", v.top+1, end, len(visible))))
	}
	return b.String()
}

func (v *mcpView) formatRow(r mcpRow) string {
	mark := v.markFor(r)
	badge := badgeFor(r.Source)
	badgeStr := ""
	if badge != "" {
		badgeStr = styleDim.Render("[" + badge + "]")
	}
	suffix := truncate(r.Description, 54)
	return fmt.Sprintf("%s %-28s %s  %s", mark, r.Name, badgeStr, styleDim.Render(suffix))
}

// markFor produces the [x] / [~] / [ ] mark per row for the current scope.
//
//	effective: [x] = loads here, [~] = disabled per-project, [ ] = not active here
//	scoped:    [x] = member of that scope, [ ] = not
func (v *mcpView) markFor(r mcpRow) string {
	switch v.scope {
	case scopeEffective:
		switch {
		case r.DisabledHere:
			return styleWarn.Render("[~]")
		case isEffective(r):
			return styleOK.Render("[x]")
		default:
			return styleDim.Render("[ ]")
		}
	case scopeUser:
		if r.Source == config.SourceUser {
			return styleOK.Render("[x]")
		}
		return "[ ]"
	case scopeLocal:
		if r.Source == config.SourceLocal {
			return styleOK.Render("[x]")
		}
		return "[ ]"
	case scopeStash:
		if r.Source == config.SourceStash {
			return styleOK.Render("[x]")
		}
		return "[ ]"
	case scopeProject:
		if r.Source != config.SourceProject {
			return styleDim.Render("   ")
		}
		if r.McpjsonDeny {
			return styleErr.Render("[!]")
		}
		return styleOK.Render("[x]")
	}
	return "[ ]"
}

func badgeFor(s config.MCPSource) string {
	switch s {
	case config.SourceUser:
		return "u"
	case config.SourceLocal:
		return "l"
	case config.SourceProject:
		return "p"
	case config.SourcePlugin:
		return "P"
	case config.SourceClaude:
		return "@"
	case config.SourceStash:
		return "s"
	case config.SourceUnknown:
		return "?"
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func (v *mcpView) resize(w, h int) { v.w, v.h = w, h }

func (v *mcpView) helpText() string {
	return "space: toggle  m: move→scope  s: cycle scope  /: filter  j/k: move"
}

func (v *mcpView) capturingInput() bool { return v.filterActive || v.moveActive }

// --- helpers ---------------------------------------------------------------

// pickConfig returns an MCP's config for enable/move/stash operations.
// Called when the user wants to copy an entry from its source into a new scope.
// Plugin sources are included so moving a plugin-registered MCP "works" (copies the
// plugin's bundled config — a warning is shown to avoid duplicate-load confusion).
//
// The `row` argument is optional context (may be zero value); when provided, we try
// its Config field first before scanning sources.
func pickConfig(st *state, name string, row mcpRow) (any, bool) {
	if row.Config != nil {
		return row.Config, true
	}
	if v, ok := st.stash.Get(name); ok {
		return v, true
	}
	if v, ok := st.cj.ProjectMCPs(st.project)[name]; ok {
		return v, true
	}
	if v, ok := st.cj.UserMCPs()[name]; ok {
		return v, true
	}
	if m, err := config.LoadMCPJson(st.project + "/.mcp.json"); err == nil {
		if v, ok := m.Servers()[name]; ok {
			return v, true
		}
	}
	// Plugin sources — last resort (may produce duplicate-load if caller copies to another scope).
	if srcs, ok := st.pluginMCPs[name]; ok && len(srcs) > 0 {
		return srcs[0].Config, true
	}
	return nil, false
}
