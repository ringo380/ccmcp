package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/stringslice"
	"github.com/ringo380/ccmcp/internal/updates"
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

	// showHidden, only meaningful in scopeEffective: when false (the default), rows that
	// can never load in this project are filtered out — stash entries, plugin MCPs whose
	// plugin is globally disabled, and orphan entries (UnknownReason set). The point of
	// the effective scope is to mirror what `/mcp` shows; those rows just clutter it.
	// Press `H` to flip and reveal them (rows are interleaved alphabetically — no divider).
	showHidden bool

	loaded bool // lazy-load gate for update probes

	flash string
}

type mcpUpdateCheckMsg struct {
	name   string
	status updates.Status
}

// mcpRow represents one (display-name, source) pair. Two rows can share a Name
// if the same name is registered by different sources — this is the correct model
// for Claude Code's world, where e.g. `context7` as a stdio MCP is a different
// entity than `context7` registered by the context7 plugin.
type mcpRow struct {
	Name         string           // display name shown to the user
	Source       config.MCPSource // primary source of this row
	OverrideKey  string           // key to write into disabledMcpServers when toggling (empty for stash rows — they can't be disabled per-project, but their MatchKey still attracts stale plain-name overrides)
	MatchKey     string           // key compared against existing disabledMcpServers entries (may differ from OverrideKey for stash rows, which should match the plain name they were parked under)
	PluginName   string           // only for SourcePlugin rows (the plugin's name without @mkt)
	PluginIDs    []string         // qualified plugin IDs (e.g. "context7@claude-plugins-official")
	PluginEnabled bool            // for SourcePlugin rows: true if the plugin is globally enabled; false means "installed but off — MCP won't load"
	DisabledHere bool             // MatchKey appears in projects[cwd].disabledMcpServers
	McpjsonDeny  bool             // .mcp.json allow/deny list excludes this row (project-source only)
	Description  string
	UnknownReason string          // for bucket-3/4 orphan rows: a human-readable explanation shown instead of Description
	Config       any              // raw config entry (for move/copy)
}

// RowKey returns a stable identifier for the (source, display-name) pair.
// Uses MatchKey so stash rows (which have empty OverrideKey) still get a unique identity,
// and disabled-plugin rows stay distinct from the plain-name rows they might share a Name with.
func (r mcpRow) RowKey() string {
	if r.MatchKey != "" {
		return string(r.Source) + "|" + r.MatchKey + "|" + boolTag(r.PluginEnabled, r.Source)
	}
	return string(r.Source) + "|" + r.Name
}

// boolTag disambiguates enabled vs disabled plugin rows in the RowKey; a no-op
// for non-plugin sources (where the flag is always false / irrelevant).
func boolTag(pluginEnabled bool, src config.MCPSource) string {
	if src != config.SourcePlugin {
		return ""
	}
	if pluginEnabled {
		return "on"
	}
	return "off"
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
// Sources: user, local, project (.mcp.json), plugin (enabled + disabled-but-installed),
// claude.ai, stash. Any leftover key in disabledMcpServers is classified as an orphan
// row with a specific UnknownReason rather than a generic "unknown" placeholder.
func (v *mcpView) rebuild() {
	disabled := stringslice.Set(v.st.cj.ProjectDisabledMcpServers(v.st.project))
	allow := stringslice.Set(v.st.cj.ProjectMcpjsonEnabled(v.st.project))
	deny := stringslice.Set(v.st.cj.ProjectMcpjsonDisabled(v.st.project))

	rows := []mcpRow{}
	// Keys we've accounted for via a concrete row. An entry in disabledMcpServers that
	// doesn't appear here falls through to the orphan classifier.
	seenKeys := map[string]bool{}

	// 1) user scope
	for name, cfg := range v.st.cj.UserMCPs() {
		key := config.OverrideKey(config.SourceUser, name, "")
		rows = append(rows, mcpRow{
			Name:         name,
			Source:       config.SourceUser,
			OverrideKey:  key,
			MatchKey:     key,
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
			MatchKey:     key,
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
				MatchKey:     key,
				McpjsonDeny:  denied,
				DisabledHere: disabled[key],
				Description:  config.DescribeMCP(cfg),
				Config:       cfg,
			})
			seenKeys[key] = true
		}
	}
	// 4) plugin-sourced — include BOTH enabled and disabled-but-installed plugins.
	// Disabled plugins still contribute rows (with PluginEnabled=false) so that stale
	// `plugin:X:Y` override entries from when X was enabled are still classified correctly.
	// state.pluginMCPs is now populated from ScanAllInstalledPluginMCPs.
	for name, srcs := range v.st.pluginMCPs {
		for _, s := range srcs {
			pluginName, _ := config.ParsePluginID(s.PluginID)
			key := config.OverrideKey(config.SourcePlugin, name, pluginName)
			desc := "via plugin: " + pluginName
			if !s.Enabled {
				desc += " (currently disabled)"
			}
			rows = append(rows, mcpRow{
				Name:          name,
				Source:        config.SourcePlugin,
				OverrideKey:   key,
				MatchKey:      key,
				PluginName:    pluginName,
				PluginIDs:     []string{s.PluginID},
				PluginEnabled: s.Enabled,
				DisabledHere:  disabled[key],
				Description:   desc,
				Config:        s.Config,
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
			MatchKey:     key,
			DisabledHere: disabled[key],
			Description:  "via claude.ai",
		})
		seenKeys[key] = true
	}
	// 6) stash. Register the plain name as the MatchKey so a pre-stash disable entry
	// (e.g. project had `"dropbox"` disabled back when it was a stdio MCP) attaches
	// back to the stash row instead of falling through to the orphan classifier.
	// Stash rows have empty OverrideKey because Claude Code doesn't honor stash entries —
	// there's nothing meaningful to toggle per-project.
	for name, cfg := range v.st.stash.Entries() {
		rows = append(rows, mcpRow{
			Name:         name,
			Source:       config.SourceStash,
			MatchKey:     name,
			DisabledHere: disabled[name], // stale-override marker; informational only
			Description:  config.DescribeMCP(cfg),
			Config:       cfg,
		})
		seenKeys[name] = true
	}
	// 7) orphans — anything in disabledMcpServers we haven't accounted for. Each gets a
	// specific UnknownReason so the user can see exactly why the entry is unrecognized.
	for k := range disabled {
		if seenKeys[k] {
			continue
		}
		src, name, pluginName := config.ParseOverrideKey(k)
		reason := v.classifyOrphan(k, src, name, pluginName)
		rows = append(rows, mcpRow{
			Name:          name,
			Source:        src,
			OverrideKey:   k,
			MatchKey:      k,
			PluginName:    pluginName,
			DisabledHere:  true,
			UnknownReason: reason,
			Description:   reason,
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

// isHiddenInEffective: in the effective scope, rows that are neither loading now nor
// merely overridden-here have no business cluttering the default view. They're stash
// entries, MCPs from globally-disabled plugins, and orphan rows for stale override keys.
// Press `H` to reveal them.
//
// Order matters: sources that can never load (stash, disabled plugins, orphans) are
// hidden BEFORE the DisabledHere check, because a per-project override on top of a
// non-loading source is redundant — the override won't kick in until the source itself
// becomes loadable, at which point the user is in the Plugins/Stash tab anyway.
//
// UnknownReason is the orphan-row contract from rebuild() (set only inside the orphan
// loop). If a future bucket reuses that field for a row that should still be visible,
// this gate must be revisited.
func isHiddenInEffective(r mcpRow) bool {
	if r.UnknownReason != "" {
		return true // orphan — no source to recover from
	}
	switch r.Source {
	case config.SourceStash:
		return true
	case config.SourcePlugin:
		if !r.PluginEnabled {
			return true // installed-but-disabled plugin: MCP can't load regardless of DisabledHere
		}
	}
	if r.DisabledHere {
		return false // overridden here but recoverable with `space` — keep visible
	}
	return false
}

// isEffective: would Claude Code actually load this row in the current project?
// Sources that can be effective: user, local, project (.mcp.json unless denied),
// enabled plugins (PluginEnabled=true), claude.ai. Disabled-but-installed plugin
// rows and stash rows never load.
func isEffective(r mcpRow) bool {
	if r.DisabledHere {
		return false
	}
	switch r.Source {
	case config.SourceUser, config.SourceLocal, config.SourceClaude:
		return true
	case config.SourcePlugin:
		// Plugin-registered MCPs load only when the plugin itself is globally enabled.
		return r.PluginEnabled
	case config.SourceProject:
		return !r.McpjsonDeny
	default:
		return false // stash, unknown
	}
}

// classifyOrphan produces the UnknownReason text for an entry in disabledMcpServers that
// didn't match any concrete row emitted above. Differentiates "plugin not installed"
// (safe to prune) from "plugin installed but doesn't register this name" (stale config)
// from "plain name, no source anywhere" (the nebulous bucket 4).
func (v *mcpView) classifyOrphan(key string, src config.MCPSource, name, pluginName string) string {
	if src == config.SourcePlugin && pluginName != "" {
		if v.st.installed != nil && len(v.st.installed.ByName(pluginName)) > 0 {
			return "plugin '" + pluginName + "' is installed but doesn't register '" + name + "' — stale override"
		}
		return "plugin '" + pluginName + "' is not installed — stale override (safe to prune)"
	}
	if src == config.SourceClaude {
		return "claude.ai integration not in the ever-connected list — stale override"
	}
	return "no active MCP source found for '" + name + "' — stale override (likely deleted or renamed)"
}

// --- update (input handling) -----------------------------------------------

func (v *mcpView) update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(mcpUpdateCheckMsg); ok {
		// Match the plugin/marketplace pattern: store + trigger a re-render so any
		// derived counts (e.g. summary aggregate) refresh promptly. formatRow reads
		// the cache live so this isn't strictly required today, but mirrors the rest
		// of the codebase and keeps the contract consistent if rendering is cached
		// later.
		v.st.updates.PutMCP(m.name, m.status)
		return nil
	}
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
	case "S":
		// Dedicated stash/unstash shortcut — smart toggle based on current row's source:
		//   stash row → move to user scope  (unstash)
		//   anything else mutable → move to stash  (stash)
		//   plugin / claude.ai / project(.mcp.json) / orphan → refused with explanation
		// Saves a keypress over `m` + picker and gives the operation a discoverable name.
		if len(visible) == 0 {
			return nil
		}
		v.stashToggle(visible[v.index])
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
	case "A":
		v.bulkToggle(visible, true)
	case "N":
		v.bulkToggle(visible, false)
	case "H":
		// Reveal/hide the noise rows (stash, disabled-plugin, orphans) in the effective scope.
		// In other scopes this is a no-op — visibleRows() doesn't apply the filter there.
		// Reset cursor + viewport so toggling doesn't leave the cursor pointing at an
		// arbitrary row whose content shifted under it.
		v.showHidden = !v.showHidden
		v.index = 0
		v.top = 0
		if v.showHidden {
			v.flash = styleDim.Render("showing hidden rows (stash / disabled plugins / orphans)")
		} else {
			v.flash = styleDim.Render("hiding inactive rows — press H to show again")
		}
	}
	return nil
}

// bulkToggle applies the equivalent of `space` to every currently-visible row,
// in the direction indicated by `on` (true = enable, false = disable). Semantics
// depend on the active scope and match the per-row toggle exactly — just batched.
//
// The target set is "visible rows", which respects any active filter. So the common
// workflow "filter to only plugin-registered MCPs, then turn them all off for this
// project" becomes `/plugin` enter `N`.
func (v *mcpView) bulkToggle(rows []mcpRow, on bool) {
	if len(rows) == 0 {
		return
	}
	var changed int
	for _, r := range rows {
		if v.bulkApplyRow(r, on) {
			changed++
		}
	}
	if changed == 0 {
		v.flash = styleDim.Render(fmt.Sprintf("no changes (%d rows already in target state)", len(rows)))
	} else {
		verb := "enabled"
		if !on {
			verb = "disabled"
		}
		v.flash = styleOK.Render(fmt.Sprintf("%s %d row(s) in %s scope (unsaved)", verb, changed, v.scope))
	}
	v.rebuild()
}

// bulkApplyRow applies a single in-scope toggle; returns true if state actually changed.
// Keeps logic per-scope rather than delegating to toggle() because toggle() issues its
// own flash message per row and calls rebuild() — both would be quadratic during bulk.
func (v *mcpView) bulkApplyRow(r mcpRow, on bool) bool {
	switch v.scope {
	case scopeEffective:
		if r.OverrideKey == "" || r.Source == config.SourceStash {
			return false // stash can't be "effective"; unknown rows skipped
		}
		if on && r.DisabledHere {
			return v.st.cj.RemoveProjectDisabledMcpServer(v.st.project, r.OverrideKey) && markDirty(&v.st.dirtyClaude)
		}
		if !on && !r.DisabledHere && isEffective(r) {
			return v.st.cj.AddProjectDisabledMcpServer(v.st.project, r.OverrideKey) && markDirty(&v.st.dirtyClaude)
		}
		return false
	case scopeLocal:
		inLocal := r.Source == config.SourceLocal
		if on && !inLocal {
			cfg, ok := pickConfig(v.st, r.Name, r)
			if !ok {
				return false
			}
			v.st.cj.SetProjectMCP(v.st.project, r.Name, cfg)
			v.st.dirtyClaude = true
			return true
		}
		if !on && inLocal {
			v.st.cj.DeleteProjectMCP(v.st.project, r.Name)
			v.st.dirtyClaude = true
			return true
		}
		return false
	case scopeUser:
		inUser := r.Source == config.SourceUser
		if on && !inUser {
			cfg, ok := pickConfig(v.st, r.Name, r)
			if !ok {
				return false
			}
			v.st.cj.SetUserMCP(r.Name, cfg)
			v.st.dirtyClaude = true
			return true
		}
		if !on && inUser {
			v.st.cj.DeleteUserMCP(r.Name)
			v.st.dirtyClaude = true
			return true
		}
		return false
	case scopeStash:
		inStash := r.Source == config.SourceStash
		if on && !inStash {
			cfg, ok := pickConfig(v.st, r.Name, r)
			if !ok {
				return false
			}
			v.st.stash.Put(r.Name, cfg)
			v.st.dirtyStash = true
			return true
		}
		if !on && inStash {
			v.st.stash.Delete(r.Name)
			v.st.dirtyStash = true
			return true
		}
		return false
	case scopeProject:
		if r.Source != config.SourceProject {
			return false
		}
		allow := v.st.cj.ProjectMcpjsonEnabled(v.st.project)
		deny := v.st.cj.ProjectMcpjsonDisabled(v.st.project)
		if on {
			if stringslice.Contains(allow, r.Name) {
				return false
			}
			allow = stringslice.UniqueAppend(allow, r.Name)
			deny = stringslice.Remove(deny, r.Name)
		} else {
			if stringslice.Contains(deny, r.Name) {
				return false
			}
			deny = stringslice.UniqueAppend(deny, r.Name)
			allow = stringslice.Remove(allow, r.Name)
		}
		v.st.cj.SetProjectMcpjsonEnabled(v.st.project, allow)
		v.st.cj.SetProjectMcpjsonDisabled(v.st.project, deny)
		v.st.dirtyClaude = true
		return true
	}
	return false
}

// markDirty flips the given dirty flag to true and returns true, so bulkApplyRow can
// fold "the write succeeded AND we should count this as a change" into one expression.
func markDirty(flag *bool) bool {
	*flag = true
	return true
}

// stashToggle handles the `S` key: route the current row into stash or out of stash
// based on where it lives now. Delegates to doMove for the actual mutation + flash so
// behavior is identical to the manual `m` + picker flow.
func (v *mcpView) stashToggle(row mcpRow) {
	switch row.Source {
	case config.SourceStash:
		// Row already in stash → unstash (move to user scope).
		v.doMove(row.RowKey(), scopeUser)
	case config.SourceUser, config.SourceLocal:
		// Row in user/local scope → stash it.
		v.doMove(row.RowKey(), scopeStash)
	case config.SourcePlugin:
		v.flash = styleWarn.Render("can't stash plugin-registered MCPs — disable the plugin or override per-project instead")
	case config.SourceClaude:
		v.flash = styleWarn.Render("can't stash claude.ai integrations — they live in Claude.ai; override per-project with `space` instead")
	case config.SourceProject:
		v.flash = styleWarn.Render("can't stash .mcp.json entries — they're git-tracked; use the allow/deny list (cycle scope to project + space) instead")
	default:
		v.flash = styleDim.Render("nothing to stash here — row source is unknown or orphaned")
	}
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
	hideNoise := v.scope == scopeEffective && !v.showHidden
	out := make([]mcpRow, 0, len(v.rows))
	for _, r := range v.rows {
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		if hideNoise && isHiddenInEffective(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// hiddenCount returns how many rows the effective-scope filter is currently suppressing
// (after the user's filter input is applied). Used in the title bar so users know noise
// exists even when collapsed.
func (v *mcpView) hiddenCount() int {
	if v.scope != scopeEffective {
		return 0
	}
	q := strings.ToLower(strings.TrimSpace(v.filter.Value()))
	n := 0
	for _, r := range v.rows {
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		if isHiddenInEffective(r) {
			n++
		}
	}
	return n
}

func (v *mcpView) render() string {
	visible := v.visibleRows()
	title := fmt.Sprintf("MCPs — scope: %s  %s", styleBadge.Render(v.scope), styleDim.Render(scopeDesc[v.scope]))
	if v.scope == scopeEffective {
		// Break out the effective-scope counts so the user sees what's loading vs
		// merely-overridden vs hidden, instead of a single ambiguous "shown" total.
		var active, dis int
		for _, r := range visible {
			// Orphans (UnknownReason set) are not "recoverable overrides" — they're stale
			// entries with no source to re-enable. Don't lump them in with `dis` when the
			// user has H toggled on, since the count means "press space to recover" rows.
			if r.UnknownReason != "" {
				continue
			}
			if r.DisabledHere {
				dis++
			} else if isEffective(r) {
				active++
			}
		}
		hidden := v.hiddenCount()
		title += fmt.Sprintf("  (%d active · %d disabled here", active, dis)
		if hidden > 0 {
			if v.showHidden {
				title += fmt.Sprintf(" · %d hidden shown", hidden)
			} else {
				title += fmt.Sprintf(" · %d hidden — press H", hidden)
			}
		}
		title += ")"
	} else {
		title += fmt.Sprintf("  (%d shown)", len(visible))
	}
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
	// Count effective rows per name so we can flag duplicates inline. Two rows for the
	// same display name are usually distinct entities (e.g. a user-scope `context7` and a
	// plugin-registered `context7`), but Claude Code will try to load both — that's worth
	// surfacing rather than letting it look like a redundant duplicate.
	//
	// Counting walks v.rows (not `visible`) so the warning survives any UI filter — a
	// duplicate-load is a duplicate-load whether or not one of the rows is currently
	// hidden by a text filter or by !showHidden.
	effDup := map[string]int{}
	for _, r := range v.rows {
		if isEffective(r) {
			effDup[r.Name]++
		}
	}
	for i := v.top; i < end; i++ {
		row := visible[i]
		line := v.formatRow(row)
		if effDup[row.Name] > 1 && isEffective(row) {
			line += "  " + styleWarn.Render(fmt.Sprintf("⚠ %dx (also loads from another source)", effDup[row.Name]))
		}
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
	line := fmt.Sprintf("%s %-28s %s  %s", mark, r.Name, badgeStr, styleDim.Render(suffix))
	if s, ok := v.st.updates.MCP(r.Name); ok && s.Outdated {
		line += "  " + styleWarn.Render("↑ "+s.Remote)
	}
	return line
}

// initialCheckCmd kicks off update probes for every MCP with a detectable npm/pypi
// launcher pattern. Only runs once per session; press R to refresh.
func (v *mcpView) initialCheckCmd() tea.Cmd {
	if v.loaded {
		return nil
	}
	v.loaded = true
	return v.buildCheckCmd()
}

func (v *mcpView) buildCheckCmd() tea.Cmd {
	type target struct {
		name     string
		launcher updates.MCPLauncher
	}
	seen := map[string]bool{}
	var targets []target
	for _, r := range v.rows {
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		cfg, _ := r.Config.(map[string]any)
		if cfg == nil {
			continue
		}
		cmdStr, _ := cfg["command"].(string)
		var args []string
		if raw, ok := cfg["args"].([]any); ok {
			for _, a := range raw {
				if s, ok := a.(string); ok {
					args = append(args, s)
				}
			}
		}
		l := updates.DetectMCPLauncher(cmdStr, args)
		if l.Pkg == "" {
			continue
		}
		targets = append(targets, target{name: r.Name, launcher: l})
	}
	if len(targets) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(targets))
	for _, t := range targets {
		tc := t
		cmds = append(cmds, func() tea.Msg {
			return mcpUpdateCheckMsg{name: tc.name, status: updates.CheckMCPLauncher(updates.DefaultRunner, tc.launcher)}
		})
	}
	return tea.Batch(cmds...)
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
	return "space: toggle  A/N: all on/off  S: stash/unstash  m: move  s: scope  H: show hidden  /: filter"
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
