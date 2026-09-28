package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/ctxcost"
	"github.com/ringo380/ccmcp/internal/mcpprobe"
	"github.com/ringo380/ccmcp/internal/mcpscope"
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
	scopeUser:      "~/.claude.json#/mcpServers  (global - every project)",
	scopeLocal:     "~/.claude.json > projects  (this dir only, private)",
	scopeProject:   "./.mcp.json  (shared, git-tracked)",
	scopeStash:     "~/.claude-mcp-stash.json  (parked, not active)",
}

var scopeCycle = []string{scopeEffective, scopeLocal, scopeUser, scopeProject, scopeStash}

// reasonNotProbedYet is the unmeasured reason for a server that simply has no
// cache entry. Named because costReason has to recognize it: it is the one
// reason not worth repeating on every row.
const reasonNotProbedYet = "not probed yet - press p"

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
	moveForKey string // RowKey (not just Name - we need the source too)

	// showHidden, only meaningful in scopeEffective: when false (the default), rows that
	// can never load in this project are filtered out - stash entries, plugin MCPs whose
	// plugin is globally disabled, and orphan entries (UnknownReason set). The point of
	// the effective scope is to mirror what `/mcp` shows; those rows just clutter it.
	// Press `H` to flip and reveal them (rows are interleaved alphabetically - no divider).
	showHidden bool

	loaded bool // lazy-load gate for update probes

	// probeInFlight counts probes currently running so the header can say so.
	probeInFlight int

	// probeSessions tracks every probe currently running so it can be stopped -
	// by `esc` during a sweep, or by the quit path. Without a handle here the
	// only thing bounding a probe was mcpprobe's own 10s timeout, and quitting
	// mid-probe orphaned the server: mcpprobe puts the child in its own process
	// tree (a process group on Unix, a Job Object on Windows; a terminal SIGINT
	// never reaches it) and the tree kill lives in a defer that never runs if
	// tea.Quit exits the process first.
	//
	// Only ever touched from the update goroutine (bubbletea serializes it) plus
	// the quit path, which runs after the last Update returns.
	probeSessions []*probeSession

	// bulkProbeTargets/bulkProbeIndex drive the `P` sweep, which runs serially -
	// one server at a time, each result chaining the next - rather than spawning
	// every server at once. bulkProbeConfirm is the pending confirmation: `P`
	// spawns processes, so unlike single-row `p` it asks first.
	bulkProbeConfirm bool
	bulkProbeTargets []mcpprobe.Target
	bulkProbeIndex   int

	flash string
}

type mcpUpdateCheckMsg struct {
	name   string
	status updates.Status
}

// probeSession is a handle on one running probe. cancel stops the probe (and,
// through mcpprobe's own teardown, kills the server's whole process tree);
// done closes once the probe goroutine has returned, so the quit path can wait
// for that teardown instead of racing the process exit.
type probeSession struct {
	cancel context.CancelFunc
	done   chan struct{}

	// cancelled marks a probe the USER stopped. Its Result is a fact about the
	// cancellation, not a measurement of the server, so probeDone must not cache
	// it - caching it would leave a sticky failure that reads as the server's
	// fault, the same defect the CLI's --timeout rule exists to prevent.
	cancelled bool
}

// probeTeardownWait bounds how long the quit path waits for a cancelled probe to
// tear its process group down. mcpprobe's own cmd.WaitDelay is 2s, so this is
// comfortably above the worst case while still being a bound rather than a hope.
const probeTeardownWait = 3 * time.Second

// mcpProbeDoneMsg carries one completed probe. mcpprobe.Probe never returns an
// error - a failure is a cached fact - so there is no err field here.
type mcpProbeDoneMsg struct {
	res mcpprobe.Result

	// session identifies which probe produced this result, so a cancelled one
	// can be discarded. Nil from a synthesized message (tests, or any future
	// direct sender), which is treated as "not cancelled".
	session *probeSession

	// bulk marks this result as one the `P` sweep started. The sweep must only
	// ever count its OWN probes: attributing any arriving result to it advanced
	// bulkProbeIndex and fired the "probed N server(s)" completion while probes
	// were still in flight, so the sweep both lost its serial guarantee and
	// claimed to be finished when it was not.
	bulk bool
}

// mcpRow represents one (display-name, source) pair. Two rows can share a Name
// if the same name is registered by different sources - this is the correct model
// for Claude Code's world, where e.g. `context7` as a stdio MCP is a different
// entity than `context7` registered by the context7 plugin.
type mcpRow struct {
	Name         string           // display name shown to the user
	Source       config.MCPSource // primary source of this row
	OverrideKey  string           // key to write into disabledMcpServers when toggling (empty for stash rows - they can't be disabled per-project, but their MatchKey still attracts stale plain-name overrides)
	MatchKey     string           // key compared against existing disabledMcpServers entries (may differ from OverrideKey for stash rows, which should match the plain name they were parked under)
	PluginName   string           // only for SourcePlugin rows (the plugin's name without @mkt)
	PluginIDs    []string         // qualified plugin IDs (e.g. "context7@claude-plugins-official")
	PluginEnabled bool            // for SourcePlugin rows: true if the plugin is globally enabled; false means "installed but off - MCP won't load"
	DisabledHere bool             // MatchKey appears in projects[cwd].disabledMcpServers
	EnabledHere  bool             // Name appears in projects[cwd].enabledMcpServers (SourceBuiltin rows: this is the only reason they load)
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
	enabled := stringslice.Set(v.st.cj.ProjectEnabledMcpServers(v.st.project))
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
			// One definition, shared with internal/mcpscope (and therefore with
			// `ccmcp context`): this rule previously existed as two live copies
			// with no test catching them drifting.
			denied := mcpscope.McpjsonExcluded(name, allow, deny)
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
	// 4) plugin-sourced - include BOTH enabled and disabled-but-installed plugins.
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
	// Stash rows have empty OverrideKey because Claude Code doesn't honor stash entries -
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
	// 7) orphans - anything in disabledMcpServers we haven't accounted for. Each gets a
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
		// Mark seen like every other section so a later section (e.g. the built-in loop
		// below) can't re-emit a second row for the same plain name - a name present in
		// BOTH disabledMcpServers and enabledMcpServers would otherwise produce two
		// contradictory rows ([~] orphan here + [x] built-in).
		seenKeys[k] = true
	}

	// 8) built-ins / externally-managed MCPs explicitly enabled for this project via
	// enabledMcpServers - the positive counterpart to disabledMcpServers. These have no
	// source ccmcp enumerates (e.g. the "computer-use" Claude-in-Chrome built-in, which
	// ships disabled). Surfacing them keeps the effective view honest: it claims to mirror
	// /mcp, and /mcp counts these as loaded. A name already represented by an enumerated
	// source (plain-key collision) is skipped - the enable entry is redundant there.
	for name := range enabled {
		if seenKeys[name] {
			continue
		}
		rows = append(rows, mcpRow{
			Name:        name,
			Source:      config.SourceBuiltin,
			MatchKey:    name,
			EnabledHere: true,
			Description: "enabled here (built-in or externally-managed - no local config)",
		})
		seenKeys[name] = true
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
	v.publishCostStates()
}

// publishCostStates hands the context estimator one entry per server that loads
// in this project - NOT just the ones the probe cache knows about.
//
// The enumeration and the accounting both live in internal/mcpscope, shared with
// `ccmcp context` and `ccmcp mcp probe`, so the tab and the CLI cannot disagree
// about the same project. They already did: a name that was both stashed and
// listed in enabledMcpServers was effective in the CLI's own former copy of these
// rules and suppressed here, so the two printed different unmeasured counts. See
// mcpscope.CostStates for the completeness and duplicate-name contracts.
//
// Runs from rebuild() (construction plus every mutation) rather than render() so
// the states, and the generation bump they cause, are in place before the first
// keypress - and so no render path can ever be the thing that triggers a probe.
//
// It reads v.st, not v.rows: the state is the source of truth for what loads, and
// the tab's rows are a display projection of it (TestMCPRowsAgreeWithMCPScope
// pins the two to the same effective set).
func (v *mcpView) publishCostStates() {
	servers := mcpscope.Effective(mcpscope.Inputs{
		CJ:         v.st.cj,
		Project:    v.st.project,
		Stash:      v.st.stash,
		PluginMCPs: v.st.pluginMCPs,
	})
	v.st.setMCPStates(mcpscope.CostStates(servers, v.st.probeCache(), reasonNotProbedYet))
}

// probeTargetFor resolves a row into a probeable target, or explains why it is
// not one. The two sources with no local config at all are named specifically:
// TargetFromConfig sees only a nil config there and would report it as malformed,
// which is true but useless to the user.
func probeTargetFor(r mcpRow) (mcpprobe.Target, bool, string) {
	switch r.Source {
	case config.SourceClaude:
		return mcpprobe.Target{}, false, "claude.ai integration - cannot probe locally"
	case config.SourceBuiltin:
		return mcpprobe.Target{}, false, "built-in with no local config - cannot probe locally"
	}
	if r.UnknownReason != "" {
		return mcpprobe.Target{}, false, "stale override with no source - nothing to probe"
	}
	if r.Config == nil {
		return mcpprobe.Target{}, false, "no config on disk - nothing to probe"
	}
	return mcpprobe.TargetFromConfig(r.Name, r.Config)
}

// isHiddenInEffective: in the effective scope, rows that are neither loading now nor
// merely overridden-here have no business cluttering the default view. They're stash
// entries, MCPs from globally-disabled plugins, and orphan rows for stale override keys.
// Press `H` to reveal them.
//
// Order matters: sources that can never load (stash, disabled plugins, orphans) are
// hidden BEFORE the DisabledHere check, because a per-project override on top of a
// non-loading source is redundant - the override won't kick in until the source itself
// becomes loadable, at which point the user is in the Plugins/Stash tab anyway.
//
// UnknownReason is the orphan-row contract from rebuild() (set only inside the orphan
// loop). If a future bucket reuses that field for a row that should still be visible,
// this gate must be revisited.
func isHiddenInEffective(r mcpRow) bool {
	if r.UnknownReason != "" {
		return true // orphan - no source to recover from
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
		return false // overridden here but recoverable with `space` - keep visible
	}
	return false
}

// isEffective: would Claude Code actually load this row in the current project?
// Sources that can be effective: user, local, project (.mcp.json unless denied),
// enabled plugins (PluginEnabled=true), claude.ai, and built-ins explicitly turned on
// via enabledMcpServers (EnabledHere=true). Disabled-but-installed plugin rows and stash
// rows never load.
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
	case config.SourceBuiltin:
		// Off-by-default built-in/external MCP; loads only because it's listed in
		// enabledMcpServers (EnabledHere). The DisabledHere check above already gates it.
		return r.EnabledHere
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
			return "plugin '" + pluginName + "' is installed but doesn't register '" + name + "' - stale override"
		}
		return "plugin '" + pluginName + "' is not installed - stale override (safe to prune)"
	}
	if src == config.SourceClaude {
		return "claude.ai integration not in the ever-connected list - stale override"
	}
	return "no active MCP source found for '" + name + "' - stale override (likely deleted or renamed)"
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
	if m, ok := msg.(mcpProbeDoneMsg); ok {
		return v.probeDone(m.res, m.bulk, m.session)
	}
	// A sweep in progress (confirmed, targets left) is interruptible. Handled
	// early, because esc/q were previously wired only inside the confirmation
	// block, which clears the moment the sweep starts - so a confirmed sweep was
	// uninterruptible for up to N x the probe timeout.
	//
	// But NOT ahead of a sub-mode that owns the keyboard. capturingInput is the
	// existing expression of exactly that, so it is reused rather than testing
	// filterActive/moveActive by hand: a third sub-mode added later would
	// otherwise silently reintroduce the bug this guard fixes - typing a literal
	// `q` into the filter cancelled the sweep and the keystroke was swallowed,
	// and esc in the move picker killed the sweep while leaving the picker open.
	if !v.capturingInput() && v.sweepActive() {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc", "q":
				v.stopSweep()
				return nil
			}
		}
	}
	if v.bulkProbeConfirm {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "P", "y", "enter":
				v.bulkProbeConfirm = false
				v.bulkProbeIndex = 0
				v.flash = styleProgress.Render(fmt.Sprintf("probing %d server(s)… (1/%d)",
					len(v.bulkProbeTargets), len(v.bulkProbeTargets)))
				return v.bulkProbeNext()
			case "esc", "ctrl+c", "n", "q":
				v.bulkProbeConfirm = false
				v.bulkProbeTargets = nil
				v.flash = styleDim.Render("probe cancelled")
			}
		}
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
		// Dedicated stash/unstash shortcut - smart toggle based on current row's source:
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
	case "p":
		// A keypress IS the consent for one probe: it spawns exactly the server
		// the user is looking at, so there is nothing more to confirm.
		if len(visible) == 0 {
			return nil
		}
		return v.probeRow(visible[v.index])
	case "P":
		return v.askBulkProbe(visible)
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
		// In other scopes this is a no-op - visibleRows() doesn't apply the filter there.
		// Reset cursor + viewport so toggling doesn't leave the cursor pointing at an
		// arbitrary row whose content shifted under it.
		v.showHidden = !v.showHidden
		v.index = 0
		v.top = 0
		if v.showHidden {
			v.flash = styleDim.Render("showing hidden rows (stash / disabled plugins / orphans)")
		} else {
			v.flash = styleDim.Render("hiding inactive rows - press H to show again")
		}
	}
	return nil
}

// bulkToggle applies the equivalent of `space` to every currently-visible row,
// in the direction indicated by `on` (true = enable, false = disable). Semantics
// depend on the active scope and match the per-row toggle exactly - just batched.
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
// own flash message per row and calls rebuild() - both would be quadratic during bulk.
func (v *mcpView) bulkApplyRow(r mcpRow, on bool) bool {
	switch v.scope {
	case scopeEffective:
		// Built-in rows toggle their enabledMcpServers membership, not disabledMcpServers.
		// Only the "off" direction is meaningful (removing the enable entry); there's no
		// way to synthesize a built-in back on without /mcp, so "on" is a no-op.
		if r.Source == config.SourceBuiltin {
			if !on && r.EnabledHere {
				return v.st.cj.RemoveProjectEnabledMcpServer(v.st.project, r.Name) && markDirty(&v.st.dirtyClaude)
			}
			return false
		}
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
		v.flash = styleWarn.Render("can't stash plugin-registered MCPs - disable the plugin or override per-project instead")
	case config.SourceClaude:
		v.flash = styleWarn.Render("can't stash claude.ai integrations - they live in Claude.ai; override per-project with `space` instead")
	case config.SourceProject:
		v.flash = styleWarn.Render("can't stash .mcp.json entries - they're git-tracked; use the allow/deny list (cycle scope to project + space) instead")
	case config.SourceBuiltin:
		v.flash = styleWarn.Render("can't stash built-in MCPs - they have no local config; press space to turn off here, or manage via /mcp")
	default:
		v.flash = styleDim.Render("nothing to stash here - row source is unknown or orphaned")
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
	// Stash rows can't be toggled in effective view - they don't load anywhere by themselves.
	if row.Source == config.SourceStash {
		v.flash = styleDim.Render("stashed MCPs aren't loaded anywhere - press 'm' to move into user/local/project scope to activate")
		return
	}
	// Built-in / externally-managed rows live only in enabledMcpServers. The honest inverse
	// of "enabled here" is to remove that entry (re-hiding the built-in), NOT to write the
	// name into disabledMcpServers. Once removed the row disappears; re-enable via /mcp.
	if row.Source == config.SourceBuiltin {
		if v.st.cj.RemoveProjectEnabledMcpServer(v.st.project, row.Name) {
			v.st.dirtyClaude = true
			v.flash = styleWarn.Render(row.Name + " → no longer enabled here (built-in; re-enable via /mcp)")
		}
		v.rebuild()
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
		v.flash = styleDim.Render("allow/deny toggling applies to .mcp.json entries only - switch to a different scope")
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
// the plugin's entry is copied - both will load unless the user separately disables
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
	// Do NOT remove the plugin's entry (we can't - it's inside the plugin cache dir).
	isPluginCopy := row.Source == config.SourcePlugin

	var removed []string
	// Remove from mutable source scopes that aren't the target (except plugin - immutable).
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
			"copied %s from plugin '%s' to %s - both will load; press space on the plugin row to disable per-project, or disable the plugin in the Plugins tab",
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

// --- probing ----------------------------------------------------------------

// probeRow starts a probe for one row, or explains why it cannot be probed.
// Never probes inline: the work runs as a tea.Cmd so a server that takes the
// full mcpprobe timeout does not freeze the UI.
func (v *mcpView) probeRow(r mcpRow) tea.Cmd {
	// Refused, not queued, while anything is already running: a single probe
	// started during a `P` sweep ran a second server concurrently, which is
	// exactly the one-at-a-time property the sweep promises. askBulkProbe has the
	// same guard.
	if v.probeInFlight > 0 || len(v.bulkProbeTargets) > 0 {
		v.flash = styleDim.Render("a probe is already running - wait for it to finish")
		return nil
	}
	t, ok, reason := probeTargetFor(r)
	if !ok {
		v.flash = styleWarn.Render("can't probe " + r.Name + " - " + reason)
		return nil
	}
	v.probeInFlight++
	v.flash = styleProgress.Render("probing " + r.Name + "… (spawning the server)")
	return v.startProbe(t, false)
}

// askBulkProbe stages a sweep over every probeable visible row and asks for
// confirmation. Unlike single-row `p` this spawns one process per server, so it
// never starts on the first keypress.
func (v *mcpView) askBulkProbe(rows []mcpRow) tea.Cmd {
	// Same in-flight guard the plugins tab puts on its bulk update: re-staging
	// targets while a result is still in flight would have probeDone chain into
	// the new list before the user confirmed it.
	if v.probeInFlight > 0 || len(v.bulkProbeTargets) > 0 {
		v.flash = styleDim.Render("a probe is already running - wait for it to finish")
		return nil
	}
	seen := map[string]bool{}
	var targets []mcpprobe.Target
	for _, r := range rows {
		t, ok, _ := probeTargetFor(r)
		if !ok || seen[t.Key()] {
			continue
		}
		seen[t.Key()] = true
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		v.flash = styleDim.Render("nothing here can be probed locally")
		return nil
	}
	v.bulkProbeTargets = targets
	v.bulkProbeIndex = 0
	v.bulkProbeConfirm = true
	return nil
}

// bulkProbeNext probes the target at bulkProbeIndex, or finishes the sweep.
func (v *mcpView) bulkProbeNext() tea.Cmd {
	if v.bulkProbeIndex >= len(v.bulkProbeTargets) {
		n := len(v.bulkProbeTargets)
		v.bulkProbeTargets = nil
		v.bulkProbeIndex = 0
		if n > 0 {
			v.flash = styleOK.Render(fmt.Sprintf("probed %d server(s)", n))
		}
		return nil
	}
	v.probeInFlight++
	return v.startProbe(v.bulkProbeTargets[v.bulkProbeIndex], true)
}

// probeDone records a completed probe and refreshes the estimate. The cache is
// written through immediately: a probe result is a measurement, not a pending
// edit, and caching a failure is what stops a hanging server re-hanging the user.
// fromBulk says the result belongs to the `P` sweep; a single-row probe that
// completes while a sweep is running must not advance it.
func (v *mcpView) probeDone(res mcpprobe.Result, fromBulk bool, session *probeSession) tea.Cmd {
	if v.probeInFlight > 0 {
		v.probeInFlight--
	}
	v.forgetProbeSession(session)
	if session != nil && session.cancelled {
		// The user stopped this one. Its Result describes the cancellation, not
		// the server, so it is neither cached nor allowed to advance a sweep -
		// stopSweep has already cleared the targets, and the flash it set says
		// what happened.
		return nil
	}
	cache := v.st.probeCache()
	cache.Put(res)
	saveErr := cache.Save()
	// rebuild republishes the cost states, which advances the MCP generation and
	// so rebuilds the estimate with this result folded in.
	v.rebuild()

	bulk := fromBulk && v.bulkProbeIndex < len(v.bulkProbeTargets)
	switch {
	case saveErr != nil:
		v.flash = styleErr.Render("probe cache not saved: " + saveErr.Error())
	case !res.OK:
		v.flash = styleWarn.Render(res.Name + " probe failed: " + truncateRunes(res.Err, 70))
	default:
		v.flash = styleOK.Render(fmt.Sprintf("%s probed - %s", res.Name,
			ctxcost.HumanCost(ctxcost.Cost{Loaded: res.Loaded(), Deferred: res.Deferred()})))
	}
	if !bulk {
		return nil
	}
	v.bulkProbeIndex++
	if v.bulkProbeIndex < len(v.bulkProbeTargets) {
		v.flash = styleProgress.Render(fmt.Sprintf("probing %s… (%d/%d)",
			v.bulkProbeTargets[v.bulkProbeIndex].Name, v.bulkProbeIndex+1, len(v.bulkProbeTargets)))
	}
	return v.bulkProbeNext()
}

// startProbe runs one probe off the UI goroutine under a cancellable context,
// and registers a session so `esc` or the quit path can stop it. mcpprobe.Probe
// still bounds itself with its own timeout and never returns an error; the
// context is what makes the wait interruptible rather than merely finite.
func (v *mcpView) startProbe(t mcpprobe.Target, bulk bool) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	s := &probeSession{cancel: cancel, done: make(chan struct{})}
	v.probeSessions = append(v.probeSessions, s)
	return func() tea.Msg {
		// Closed before the message is delivered, so a quit path waiting on it
		// knows the probe's own deferred process-group teardown has already run.
		defer close(s.done)
		res := mcpprobe.Probe(ctx, t)
		cancel() // release the context regardless of how the probe ended
		return mcpProbeDoneMsg{res: res, bulk: bulk, session: s}
	}
}

// forgetProbeSession drops a finished session.
func (v *mcpView) forgetProbeSession(s *probeSession) {
	if s == nil {
		return
	}
	kept := v.probeSessions[:0]
	for _, existing := range v.probeSessions {
		if existing != s {
			kept = append(kept, existing)
		}
	}
	v.probeSessions = kept
}

// cancelProbes stops every in-flight probe and reports how many it stopped.
//
// wait makes it block (bounded by probeTeardownWait) until each probe goroutine
// has returned. The quit path MUST wait: cancelling only asks, and mcpprobe's
// group SIGKILL runs in the probe goroutine's defer - if the process exits
// first, the server survives as an orphan, which is the leak this exists to
// close. Interactive cancellation does not wait, because blocking the UI for up
// to 3s is exactly what the user just asked to stop.
//
// Sessions stay in the list: probeDone needs to see the cancelled flag when the
// discarded result finally arrives, and removes its own entry then.
func (v *mcpView) cancelProbes(wait bool) int {
	n := 0
	for _, s := range v.probeSessions {
		if s.cancelled {
			continue
		}
		s.cancelled = true
		s.cancel()
		n++
	}
	if wait {
		for _, s := range v.probeSessions {
			select {
			case <-s.done:
			case <-time.After(probeTeardownWait):
			}
		}
	}
	return n
}

// sweepActive reports whether a confirmed `P` sweep still has targets to run.
// The confirmation state is NOT this: nothing is running there yet.
func (v *mcpView) sweepActive() bool {
	return !v.bulkProbeConfirm && len(v.bulkProbeTargets) > 0
}

// stopSweep cancels an active `P` sweep: the in-flight probe is stopped and the
// remaining targets are dropped so probeDone cannot chain into them.
func (v *mcpView) stopSweep() {
	v.cancelProbes(false)
	v.bulkProbeTargets = nil
	v.bulkProbeIndex = 0
	v.flash = styleDim.Render("probe sweep cancelled")
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
	title := fmt.Sprintf("MCPs - scope: %s  %s", styleBadge.Render(v.scope), styleDim.Render(scopeDesc[v.scope]))
	if v.scope == scopeEffective {
		// Break out the effective-scope counts so the user sees what's loading vs
		// merely-overridden vs hidden, instead of a single ambiguous "shown" total.
		var active, dis int
		for _, r := range visible {
			// Orphans (UnknownReason set) are not "recoverable overrides" - they're stale
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
				title += fmt.Sprintf(" · %d hidden - press H", hidden)
			}
		}
		title += ")"
	} else {
		title += fmt.Sprintf("  (%d shown)", len(visible))
	}
	var b strings.Builder
	// The title is budgeted as ONE logical line below, so it has to be clamped
	// like every other header line: at 80 columns the scope description plus the
	// count summary wrapped to two physical rows and cost the list a row.
	b.WriteString(fitWidth(title, v.w))
	b.WriteString("\n")

	idx := v.st.costIndex()
	// Every effective server has an entry (see publishCostStates), so the count
	// of unmeasured ones comes from the accounting itself rather than from a
	// missing-key scan. Only MCP sources increment Unmeasured - assets are folded
	// in through a different path - so this figure is servers, not assets.
	unmeasured := idx.Project.Unmeasured
	known := idx.Project.MCP
	// Keep each header line inside v.w: headerLines below budgets logical lines,
	// so a wrapped header costs the list a row it never gave back.
	switch {
	case v.st.costUnavailable() != "":
		b.WriteString(fitWidth(fmt.Sprintf("  per-turn context (MCP tool schemas)   %s   (unavailable: %s)",
			ctxcost.Human(ctxcost.Unmeasured), v.st.costUnavailable()), v.w))
	case known.Loaded == 0 && unmeasured > 0:
		b.WriteString(fitWidth(fmt.Sprintf("  per-turn context (MCP tool schemas)   %s   (%d server(s) unmeasured - tool schemas need a probe)",
			ctxcost.Human(ctxcost.Unmeasured), unmeasured), v.w))
	case unmeasured > 0:
		b.WriteString(fitWidth(fmt.Sprintf("  per-turn context (MCP tool schemas)   %s   (%d unmeasured)", ctxcost.HumanCost(known), unmeasured), v.w))
	default:
		b.WriteString(fitWidth(fmt.Sprintf("  per-turn context (MCP tool schemas)   %s", ctxcost.HumanCost(known)), v.w))
	}
	b.WriteString("\n")
	if mm, ok := v.st.measured(); ok {
		b.WriteString(fitWidth(fmt.Sprintf("  session-start prefix %s", ctxcost.Human(mm.PrefixTokens)), v.w))
		b.WriteString("\n")
	}

	if v.probeInFlight > 0 {
		// The hint is only shown for a sweep: a single `p` probe has nothing left
		// to chain, so esc would stop one already-running server for no gain.
		hint := ""
		if v.sweepActive() {
			hint = "  (esc to cancel)"
		}
		b.WriteString(fitWidth("  "+v.st.spinnerFrame+styleProgress.Render(fmt.Sprintf(
			"probing %d server(s)…%s", v.probeInFlight, hint)), v.w))
		b.WriteString("\n")
	}
	if v.bulkProbeConfirm {
		// Kept short enough to survive fitWidth at 80 columns WITH the cancel
		// hint: the previous wording was 82 columns, so the clamp ate "esc to
		// cancel" and left a confirmation with no visible way out.
		b.WriteString(fitWidth(styleWarn.Render(fmt.Sprintf(
			"Probe %d server(s)? Starts each one. P: confirm  esc: cancel",
			len(v.bulkProbeTargets))), v.w))
		b.WriteString("\n")
	}
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
	headerLines := strings.Count(b.String(), "\n")
	// -1 reserves the "[a-b of N]" scroll indicator appended after the list; the
	// help line lives in the footer, outside the body budget. Same reasoning as
	// plugins.go - a floor of 5 overflows a short terminal.
	listHeight := v.h - headerLines - 1
	switch {
	case v.h <= 0:
		listHeight = 5
	case listHeight < 1:
		listHeight = 1
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
	// plugin-registered `context7`), but Claude Code will try to load both - that's worth
	// surfacing rather than letting it look like a redundant duplicate.
	//
	// Counting walks v.rows (not `visible`) so the warning survives any UI filter - a
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
		line := v.formatRow(row, idx)
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

func (v *mcpView) formatRow(r mcpRow, idx *ctxcost.Index) string {
	mark := v.markFor(r)
	badge := badgeFor(r.Source)
	badgeStr := ""
	if badge != "" {
		badgeStr = styleDim.Render("[" + badge + "]")
	}
	// Description budget shrank from 54 to 44 to pay for the cost column, so the
	// row is no wider than it was before.
	desc := r.Description
	if why := v.costReason(r); why != "" {
		// Folded into the SAME 44-column budget rather than appended, so
		// explaining the "-" costs no width. An honest "-" the user cannot
		// account for is only half an answer.
		desc += " - " + why
	}
	suffix := truncate(desc, 44)
	line := fmt.Sprintf("%s %-28s %s %8s  %s", mark, r.Name, badgeStr, v.rowCost(idx, r), styleDim.Render(suffix))
	if s, ok := v.st.updates.MCP(r.Name); ok && s.Outdated {
		line += "  " + styleWarn.Render("↑ "+s.Remote)
	}
	return line
}

// rowCost renders this row's probed per-turn cost, or "-" when there is no
// measurement.
//
// The two-value ByMCP lookup is load-bearing: a missing key yields a zero-value
// Breakdown whose Total() is 0, and rows that never load (stash, disabled
// plugins, rows in other scopes) are deliberately absent from the accounting. A
// one-value lookup would print "≈0" for every one of them - a confident
// measurement of zero where nothing was measured at all.
func (v *mcpView) rowCost(idx *ctxcost.Index, r mcpRow) string {
	if v.st.costUnavailable() != "" {
		return ctxcost.Human(ctxcost.Unmeasured)
	}
	b, ok := idx.ByMCP[r.Name]
	if !ok || b.Items == 0 || b.Unmeasured > 0 {
		return ctxcost.Human(ctxcost.Unmeasured)
	}
	return ctxcost.Human(b.MCP.Loaded)
}

// costReason explains a row's "-" when the explanation is not obvious from the
// row itself.
//
// The plain "never probed" case is deliberately excluded: it applies to nearly
// every row before the user presses `p`, the `-` already says it, and repeating
// it on each line would crowd out the command the description column exists to
// show. What IS surfaced is the case the user cannot deduce - a name collision,
// a failed probe, a source that cannot be probed locally.
func (v *mcpView) costReason(r mcpRow) string {
	st, ok := v.st.mcpStates[r.Name]
	if !ok || st.Probed || st.Reason == "" || st.Reason == reasonNotProbedYet {
		return ""
	}
	return st.Reason
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
	case config.SourceBuiltin:
		return "b"
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
	return "space: toggle  A/N: all on/off  S: stash/unstash  m: move  s: scope  p: probe  H: show hidden  /: filter"
}

func (v *mcpView) capturingInput() bool {
	return v.filterActive || v.moveActive || v.bulkProbeConfirm
}

// --- helpers ---------------------------------------------------------------

// pickConfig returns an MCP's config for enable/move/stash operations.
// Called when the user wants to copy an entry from its source into a new scope.
// Plugin sources are included so moving a plugin-registered MCP "works" (copies the
// plugin's bundled config - a warning is shown to avoid duplicate-load confusion).
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
	// Plugin sources - last resort (may produce duplicate-load if caller copies to another scope).
	if srcs, ok := st.pluginMCPs[name]; ok && len(srcs) > 0 {
		return srcs[0].Config, true
	}
	return nil, false
}
