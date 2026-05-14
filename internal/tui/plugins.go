package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/stringslice"
	"github.com/ringo380/ccmcp/internal/updates"
)

// ---------------------------------------------------------------------------
// Row types
// ---------------------------------------------------------------------------

type pluginRowView struct {
	ID        string
	Enabled   bool
	Known     bool
	Installed bool
	Version   string
	IsRemote  bool   // claude.ai integration row
	RemoteKey string // override key e.g. "claude.ai Stripe"
	DisabledHere bool // per-project disabled (remote rows only)
	Outdated  bool   // a newer upstream version is available
}

type availPluginRow struct {
	Name        string
	QualifiedID string
	Marketplace string
}

// ---------------------------------------------------------------------------
// Async message types
// ---------------------------------------------------------------------------

type pluginUpdateResultMsg struct {
	id           string
	oldSha       string
	oldInstPath  string
	result       *install.Result
	err          error
}

type pluginBulkUpdateResultMsg struct {
	// applied carries each successful re-fetch so install.UpdateInstall runs on the
	// main bubbletea goroutine. Mutating v.st.installed inside the worker goroutine
	// races against rebuild() / List() on the render thread.
	applied []bulkUpdateApplied
	skipped []string
	failed  []bulkUpdateFailure
	// streamed=true means the per-item handler already applied each entry; the
	// result handler should skip its UpdateInstall+InvalidatePlugin loop to avoid
	// redundant work. Direct senders (existing tests, future CLI integrations)
	// leave it false so the result handler stays responsible for landing state.
	streamed bool
}

// bulkUpdateFailure captures a per-plugin failure during bulk update along with a
// human-readable hint computed from the error text. Kept around between bulk runs
// (and persisted to ~/.claude-mcp-backups/last-bulk-failures.json) so the user can
// re-open the failure panel with `F` even after switching tabs or restarting the TUI.
type bulkUpdateFailure struct {
	ID   string `json:"id"`
	Err  string `json:"err"`
	Hint string `json:"hint"`
}

type bulkUpdateApplied struct {
	id          string
	result      *install.Result
	oldInstPath string
}

// bulkUpdateTarget is one queued item in a bulk update sweep. The bulk worker
// processes targets serially, emitting a pluginBulkItemDoneMsg per item so the
// view can show live (N/M) progress and clear the "↑ update available" annotation
// for each plugin as soon as it lands instead of waiting for the entire batch.
type bulkUpdateTarget struct {
	id, name, mkt, oldSha, oldInstPath string
}

type pluginBulkItemDoneMsg struct {
	target bulkUpdateTarget
	result *install.Result
	err    error
}

type pluginInstallResultMsg struct {
	result *install.Result
	err    error
}

type pluginUpdateCheckMsg struct {
	id     string
	status updates.Status
}

type availLoadedMsg struct {
	rows []availPluginRow
	err  error
}

// ---------------------------------------------------------------------------
// View struct
// ---------------------------------------------------------------------------

type pluginView struct {
	st *state

	loaded bool

	rows  []pluginRowView
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool

	showOnly string // "" | "enabled" | "disabled"

	// available-plugins sub-view
	mode        string // "" (installed) | "available"
	availRows   []availPluginRow
	availIndex  int
	availTop    int
	availLoading bool
	availErr    string

	// async operation state
	updating     bool
	bulkUpdating bool
	installing   bool

	// per-item bulk-update progress. Active only while bulkUpdating=true.
	// bulkIndex is the count of items already processed (also = next index to run).
	bulkTargets []bulkUpdateTarget
	bulkIndex   int
	bulkApplied []bulkUpdateApplied
	bulkSkipped []string
	bulkFailed  []bulkUpdateFailure

	// lastFailures survives past bulkUpdating=false. Loaded from disk on first
	// render of this view; populated by bulk-update completion. `F` opens the
	// failures panel against this slice.
	lastFailures      []bulkUpdateFailure
	lastFailuresLoaded bool
	failuresIndex     int
	failuresTop       int
	failuresExpanded  bool

	// two-step remove confirmation
	pendingRemove string

	flash string
}

func newPluginView(st *state) *pluginView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	v := &pluginView{st: st, filter: ti}
	v.rebuild()
	return v
}

func (v *pluginView) rebuild() {
	installedIdx := map[string]config.InstalledPlugin{}
	for _, ip := range v.st.installed.List() {
		installedIdx[ip.ID] = ip
	}

	// Build regular plugin rows.
	seen := map[string]bool{}
	var rows []pluginRowView
	for _, e := range v.st.settings.PluginEntries() {
		seen[e.ID] = true
		ip := installedIdx[e.ID]
		row := pluginRowView{ID: e.ID, Enabled: e.Enabled, Known: true, Installed: ip.InstallPath != "", Version: ip.Version}
		if s, ok := v.st.updates.Plugin(e.ID); ok && s.Outdated {
			row.Outdated = true
		}
		rows = append(rows, row)
	}
	for _, ip := range v.st.installed.List() {
		if seen[ip.ID] {
			continue
		}
		row := pluginRowView{ID: ip.ID, Installed: true, Version: ip.Version}
		if s, ok := v.st.updates.Plugin(ip.ID); ok && s.Outdated {
			row.Outdated = true
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	// Append remote (claude.ai) rows.
	if len(v.st.claudeAi) > 0 {
		disabled := stringslice.Set(v.st.cj.ProjectDisabledMcpServers(v.st.project))
		for _, aiKey := range v.st.claudeAi {
			name := strings.TrimPrefix(aiKey, "claude.ai ")
			rows = append(rows, pluginRowView{
				ID:           name,
				IsRemote:     true,
				RemoteKey:    aiKey,
				DisabledHere: disabled[aiKey],
			})
		}
	}

	v.rows = rows
	if visible := v.visibleRows(); v.index >= len(visible) {
		v.index = 0
		v.top = 0
	}
}

// ---------------------------------------------------------------------------
// visibleRows: for installed mode, filters by showOnly + search
// Remote rows always pass through (ignoring showOnly; still searchable).
// ---------------------------------------------------------------------------

func (v *pluginView) visibleRows() []pluginRowView {
	q := strings.ToLower(strings.TrimSpace(v.filter.Value()))
	out := make([]pluginRowView, 0, len(v.rows))
	for _, r := range v.rows {
		if !r.IsRemote {
			switch v.showOnly {
			case "enabled":
				if !r.Enabled {
					continue
				}
			case "disabled":
				if r.Enabled || !r.Known {
					continue
				}
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(r.ID), q) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// firstRemoteIndex returns the index in `rows` of the first remote row, or -1.
func firstRemoteIdx(rows []pluginRowView) int {
	for i, r := range rows {
		if r.IsRemote {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

func (v *pluginView) update(msg tea.Msg) tea.Cmd {
	// Handle async result messages regardless of mode.
	switch m := msg.(type) {
	case pluginBulkItemDoneMsg:
		t := m.target
		switch {
		case m.err != nil || m.result == nil:
			// Treat a nil result as a failure even when err is nil — should never
			// happen given install.Install's contract, but a defensive guard keeps
			// the switch from dereferencing nil in the SHA comparison below.
			errText := ""
			if m.err != nil {
				errText = m.err.Error()
			} else {
				errText = "install returned nil result with no error"
			}
			v.bulkFailed = append(v.bulkFailed, bulkUpdateFailure{
				ID:   t.id,
				Err:  errText,
				Hint: classifyUpdateError(errText),
			})
		case m.result.GitCommitSha != "" && t.oldSha != "" && m.result.GitCommitSha == t.oldSha:
			// Real sha match: nothing changed. An empty oldSha plus empty result sha
			// (non-git source) would spuriously match, so the guard above gates on both.
			v.bulkSkipped = append(v.bulkSkipped, t.id)
		default:
			// Apply incrementally so the "↑ update available" indicator clears live for
			// each plugin instead of all at once at the end of the batch. Set
			// dirtyPlugins now so an in-flight Q quit-confirmation still prompts to
			// save even if the result handler never runs (e.g. session torn down).
			install.UpdateInstall(v.st.installed, m.result, t.oldInstPath)
			v.st.updates.InvalidatePlugin(t.id)
			v.st.dirtyPlugins = true
			v.bulkApplied = append(v.bulkApplied, bulkUpdateApplied{
				id: t.id, result: m.result, oldInstPath: t.oldInstPath,
			})
		}
		v.bulkIndex++
		v.rebuild()
		if v.bulkIndex < len(v.bulkTargets) {
			next := v.bulkTargets[v.bulkIndex]
			v.flash = styleProgress.Render(fmt.Sprintf(
				"updating %s… (%d/%d)", next.id, v.bulkIndex+1, len(v.bulkTargets),
			))
		}
		return v.bulkRunNextItem()

	case pluginBulkUpdateResultMsg:
		v.bulkUpdating = false
		// Drop bulk-progress scratch state so next B-press starts clean.
		v.bulkTargets = nil
		v.bulkIndex = 0
		v.bulkApplied = nil
		v.bulkSkipped = nil
		v.bulkFailed = nil
		// Apply UpdateInstall on the main goroutine to avoid racing with rebuild()'s
		// reads of v.st.installed. Skipped when streamed=true — the per-item handler
		// already landed each entry, and re-applying would just churn timestamps and
		// trigger redundant RemoveAll calls on already-deleted paths. Direct senders
		// (tests, future callers) leave streamed=false so this loop runs.
		if !m.streamed {
			for _, a := range m.applied {
				install.UpdateInstall(v.st.installed, a.result, a.oldInstPath)
				v.st.updates.InvalidatePlugin(a.id)
			}
		}
		if len(m.applied) > 0 {
			v.st.dirtyPlugins = true
			v.st.rescanPluginMCPs()
		}
		// Persist failures so `F` (capital) can re-open the panel later. Survives
		// tab switches and TUI restarts (loaded back via loadLastFailures on first
		// render). An empty failure set clears any prior on-disk file.
		v.lastFailures = m.failed
		_ = saveLastFailures(v.st.paths.BackupsDir, m.failed)
		parts := []string{}
		if len(m.applied) > 0 {
			parts = append(parts, fmt.Sprintf("%d updated", len(m.applied)))
		}
		if len(m.skipped) > 0 {
			parts = append(parts, fmt.Sprintf("%d already up to date", len(m.skipped)))
		}
		if len(m.failed) > 0 {
			parts = append(parts, styleErr.Render(fmt.Sprintf("%d failed (press F to view)", len(m.failed))))
		}
		if len(parts) == 0 {
			v.flash = styleDim.Render("no installed plugins to update")
		} else {
			v.flash = styleOK.Render("bulk update: " + strings.Join(parts, ", "))
		}
		v.rebuild()
		return nil

	case pluginUpdateResultMsg:
		v.updating = false
		if m.err != nil {
			v.flash = styleErr.Render("update error: " + m.err.Error())
			return nil
		}
		if m.result.GitCommitSha != "" && m.result.GitCommitSha == m.oldSha {
			v.flash = styleDim.Render(m.id + " already up to date")
			return nil
		}
		install.UpdateInstall(v.st.installed, m.result, m.oldInstPath)
		v.st.dirtyPlugins = true
		v.st.rescanPluginMCPs()
		v.st.updates.InvalidatePlugin(m.id)
		// A successful single-plugin update may have come from the failures
		// panel's R retry. Remove the now-resolved entry from lastFailures so
		// the panel doesn't keep showing it, and update the persisted file.
		// Cheap when lastFailures is empty (the loop short-circuits).
		if removed := dropFailure(&v.lastFailures, m.id); removed {
			_ = saveLastFailures(v.st.paths.BackupsDir, v.lastFailures)
			// If the panel just emptied, drop the user back to the regular
			// plugins view so they don't stare at an empty "Bulk-update
			// failures (0)" pane.
			if v.mode == "failures" && len(v.lastFailures) == 0 {
				v.mode = ""
				v.failuresIndex = 0
				v.failuresTop = 0
				v.failuresExpanded = false
			} else if v.mode == "failures" && v.failuresIndex >= len(v.lastFailures) {
				v.failuresIndex = len(v.lastFailures) - 1
			}
		}
		v.rebuild()
		oldS := pluginFirstN(m.oldSha, 8)
		newS := pluginFirstN(m.result.GitCommitSha, 8)
		v.flash = styleOK.Render(fmt.Sprintf("updated %s: %s → %s", m.id, oldS, newS))
		return nil

	case pluginInstallResultMsg:
		v.installing = false
		if m.err != nil {
			v.flash = styleErr.Render("install error: " + m.err.Error())
			return nil
		}
		install.RegisterInstall(v.st.settings, v.st.installed, m.result)
		v.st.dirtySettings = true
		v.st.dirtyPlugins = true
		v.st.rescanPluginMCPs()
		v.mode = ""
		v.rebuild()
		v.flash = styleOK.Render("installed " + m.result.QualifiedID)
		return nil

	case availLoadedMsg:
		v.availLoading = false
		if m.err != nil {
			v.availErr = m.err.Error()
		} else {
			v.availErr = ""
			v.availRows = m.rows
		}
		return nil

	case pluginUpdateCheckMsg:
		// Discard stale checks. The probe was scheduled before some intervening
		// update (single or bulk) landed a new local SHA on disk. Trusting it would
		// re-poison the cache with Outdated=true for a plugin we just upgraded —
		// that's the visible symptom of "10 updates available" sticking after a
		// successful bulk update. Re-fire the probe against current state instead.
		if m.status.Local != "" {
			stale := false
			stillInstalled := false
			for _, ip := range v.st.installed.List() {
				if ip.ID == m.id {
					stillInstalled = true
					if ip.GitCommitSha != "" && ip.GitCommitSha != m.status.Local {
						stale = true
					}
					break
				}
			}
			if stale && stillInstalled {
				return v.scheduleCheckPlugin(m.id)
			}
			if !stillInstalled {
				// Plugin was removed between probe-schedule and result. Drop
				// any cached entry so the count doesn't lag behind reality;
				// don't trust this stale result either.
				v.st.updates.InvalidatePlugin(m.id)
				v.rebuild()
				return nil
			}
		}
		v.st.updates.PutPlugin(m.id, m.status)
		v.rebuild()
		return nil
	}

	// Filter input mode.
	if v.filterActive {
		var cmd tea.Cmd
		v.filter, cmd = v.filter.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter", "esc":
				v.filterActive = false
				v.filter.Blur()
			}
		}
		return cmd
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	// Available sub-view mode.
	if v.mode == "available" {
		return v.updateAvailable(key)
	}

	// Failures panel sub-view.
	if v.mode == "failures" {
		return v.updateFailures(key)
	}

	// Installed mode.
	visible := v.visibleRows()

	// Clear pending remove if user presses anything other than x.
	if v.pendingRemove != "" && key.String() != "x" {
		v.pendingRemove = ""
	}

	switch key.String() {
	case "up", "k":
		if v.index > 0 {
			v.index--
		}
	case "down", "j":
		if v.index < len(visible)-1 {
			v.index++
		}
	case "g":
		v.index = 0
	case "G":
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

	case " ":
		if len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		if r.IsRemote {
			// Toggle per-project disable for claude.ai integration.
			if r.DisabledHere {
				v.st.cj.RemoveProjectDisabledMcpServer(v.st.project, r.RemoteKey)
				v.flash = styleOK.Render(r.ID + " → active here")
			} else {
				v.st.cj.AddProjectDisabledMcpServer(v.st.project, r.RemoteKey)
				v.flash = styleDim.Render(r.ID + " → disabled here")
			}
			v.st.dirtyClaude = true
			v.rebuild()
			return nil
		}
		newState := !r.Enabled
		v.st.settings.SetPluginEnabled(r.ID, newState)
		v.st.dirtySettings = true
		v.st.rescanPluginMCPs()
		if newState {
			v.flash = styleOK.Render(r.ID + " → enabled")
		} else {
			v.flash = styleDim.Render(r.ID + " → disabled")
		}
		v.rebuild()

	case "U":
		if v.updating || len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		if r.IsRemote {
			v.flash = styleDim.Render("claude.ai integrations are managed at claude.ai — cannot update here")
			return nil
		}
		if !r.Installed {
			v.flash = styleDim.Render(r.ID + " is not installed (install first)")
			return nil
		}
		name, mkt := config.ParsePluginID(r.ID)
		if mkt == "" {
			v.flash = styleErr.Render(r.ID + ": unqualified ID — cannot update")
			return nil
		}
		// Capture snapshot of current state for comparison in result handler.
		var oldSha, oldInstPath string
		for _, ip := range v.st.installed.List() {
			if ip.ID == r.ID {
				oldSha = ip.GitCommitSha
				oldInstPath = ip.InstallPath
				break
			}
		}
		v.updating = true
		v.flash = styleProgress.Render("updating " + r.ID + "…")
		id, p := r.ID, v.st.paths
		return func() tea.Msg {
			result, err := install.Install(p, mkt, name)
			return pluginUpdateResultMsg{id: id, oldSha: oldSha, oldInstPath: oldInstPath, result: result, err: err}
		}

	case "x":
		if len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		if r.IsRemote {
			v.flash = styleDim.Render("claude.ai integrations cannot be removed here — disconnect at claude.ai")
			v.pendingRemove = ""
			return nil
		}
		if v.pendingRemove == r.ID {
			// Confirmed: remove.
			v.st.settings.RemovePluginEntry(r.ID)
			v.st.installed.Remove(r.ID)
			v.st.dirtySettings = true
			v.st.dirtyPlugins = true
			v.st.rescanPluginMCPs()
			v.pendingRemove = ""
			v.rebuild()
			v.flash = styleDim.Render("removed " + r.ID + " (cache preserved)")
			return nil
		}
		v.pendingRemove = r.ID
		v.flash = styleWarn.Render("press x again to remove " + r.ID)

	case "I":
		if v.installing || v.availLoading {
			return nil
		}
		v.mode = "available"
		v.availLoading = true
		v.availIndex = 0
		v.availTop = 0
		v.flash = styleProgress.Render("loading marketplace catalogs…")
		p := v.st.paths
		installedSet := map[string]bool{}
		for _, ip := range v.st.installed.List() {
			installedSet[ip.ID] = true
		}
		return func() tea.Msg {
			names, err := install.ListLocalMarketplaces(p)
			if err != nil {
				return availLoadedMsg{err: err}
			}
			var rows []availPluginRow
			for _, mkt := range names {
				m, _, err := install.LoadMarketplace(p, mkt)
				if err != nil {
					continue
				}
				for _, mp := range m.Plugins {
					qid := mp.Name + "@" + mkt
					if !installedSet[qid] {
						rows = append(rows, availPluginRow{Name: mp.Name, QualifiedID: qid, Marketplace: mkt})
					}
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].QualifiedID < rows[j].QualifiedID })
			return availLoadedMsg{rows: rows}
		}

	case "f":
		switch v.showOnly {
		case "":
			v.showOnly = "enabled"
		case "enabled":
			v.showOnly = "disabled"
		default:
			v.showOnly = ""
		}
	case "/":
		v.filterActive = true
		v.filter.Focus()
		return textinput.Blink
	case "c":
		v.filter.SetValue("")
	case "A":
		for _, r := range visible {
			if !r.IsRemote && !r.Enabled {
				v.st.settings.SetPluginEnabled(r.ID, true)
			}
		}
		v.st.dirtySettings = true
		v.st.rescanPluginMCPs()
		v.flash = styleOK.Render(fmt.Sprintf("enabled %d plugins (unsaved)", len(visible)))
		v.rebuild()
	case "N":
		for _, r := range visible {
			if !r.IsRemote && r.Enabled {
				v.st.settings.SetPluginEnabled(r.ID, false)
			}
		}
		v.st.dirtySettings = true
		v.st.rescanPluginMCPs()
		v.flash = styleDim.Render(fmt.Sprintf("disabled %d plugins (unsaved)", len(visible)))
		v.rebuild()

	case "R":
		return v.initialCheckCmdForce()

	case "F":
		// Lazy-load last-bulk-failures from disk if we haven't already this session.
		if !v.lastFailuresLoaded {
			if loaded, ok := loadLastFailures(v.st.paths.BackupsDir); ok {
				v.lastFailures = loaded
			}
			v.lastFailuresLoaded = true
		}
		if len(v.lastFailures) == 0 {
			v.flash = styleDim.Render("no bulk-update failures recorded")
			return nil
		}
		v.mode = "failures"
		v.failuresIndex = 0
		v.failuresTop = 0
		v.failuresExpanded = false
		return nil

	case "B":
		if v.bulkUpdating || v.updating {
			return nil
		}
		var targets []bulkUpdateTarget
		for _, ip := range v.st.installed.List() {
			name, mkt := config.ParsePluginID(ip.ID)
			if mkt == "" {
				continue
			}
			targets = append(targets, bulkUpdateTarget{
				id:          ip.ID,
				name:        name,
				mkt:         mkt,
				oldSha:      ip.GitCommitSha,
				oldInstPath: ip.InstallPath,
			})
		}
		if len(targets) == 0 {
			v.flash = styleDim.Render("no installed plugins to update")
			return nil
		}
		v.bulkUpdating = true
		v.bulkTargets = targets
		v.bulkIndex = 0
		v.bulkApplied = nil
		v.bulkSkipped = nil
		v.bulkFailed = nil
		v.flash = styleProgress.Render(fmt.Sprintf("updating %d plugin(s)… (0/%d)", len(targets), len(targets)))
		return v.bulkRunNextItem()
	}
	return nil
}

// bulkRunNextItem returns a cmd that re-fetches v.bulkTargets[v.bulkIndex] and
// emits a pluginBulkItemDoneMsg. When all targets have been processed it emits
// the final pluginBulkUpdateResultMsg from the accumulated state.
func (v *pluginView) bulkRunNextItem() tea.Cmd {
	if v.bulkIndex >= len(v.bulkTargets) {
		applied, skipped, failed := v.bulkApplied, v.bulkSkipped, v.bulkFailed
		return func() tea.Msg {
			return pluginBulkUpdateResultMsg{applied: applied, skipped: skipped, failed: failed, streamed: true}
		}
	}
	t := v.bulkTargets[v.bulkIndex]
	p := v.st.paths
	return func() tea.Msg {
		result, err := install.Install(p, t.mkt, t.name)
		return pluginBulkItemDoneMsg{target: t, result: result, err: err}
	}
}

// initialCheckCmd is fired by the model when the user first switches to this tab. It
// kicks off update probes for every installed plugin (and the marketplaces they live
// in, so bare-string sources don't double-probe). Results arrive as pluginUpdateCheckMsg.
func (v *pluginView) initialCheckCmd() tea.Cmd {
	if v.loaded {
		return nil
	}
	v.loaded = true
	return v.buildCheckCmd()
}

// initialCheckCmdForce ignores the loaded flag (R key — manual refresh).
func (v *pluginView) initialCheckCmdForce() tea.Cmd {
	v.loaded = true
	for _, ip := range v.st.installed.List() {
		v.st.updates.InvalidatePlugin(ip.ID)
	}
	return v.buildCheckCmd()
}

func (v *pluginView) buildCheckCmd() tea.Cmd {
	installed := v.st.installed.List()
	if len(installed) == 0 {
		return nil
	}
	p := v.st.paths
	// Resolve marketplace HEADs first so bare-string plugins can reuse them.
	mktHeads := map[string]string{}
	for _, ip := range installed {
		_, mkt := config.ParsePluginID(ip.ID)
		if mkt == "" {
			continue
		}
		if _, ok := mktHeads[mkt]; ok {
			continue
		}
		mktHeads[mkt] = "" // placeholder; resolved in cmd
	}
	cmds := make([]tea.Cmd, 0, len(installed))
	for _, ipx := range installed {
		ip := ipx
		cmds = append(cmds, func() tea.Msg {
			_, mkt := config.ParsePluginID(ip.ID)
			head := install.LocalMarketplaceHead(p, mkt)
			s := updates.CheckPlugin(p, ip, head)
			return pluginUpdateCheckMsg{id: ip.ID, status: s}
		})
	}
	return tea.Batch(cmds...)
}

// scheduleCheckPlugin re-fires a single-plugin update probe. Used by the stale-check
// guard in pluginUpdateCheckMsg when an in-flight check arrives after a local update
// landed a new SHA, so the cache gets a fresh result instead of the pre-update one.
func (v *pluginView) scheduleCheckPlugin(id string) tea.Cmd {
	var target config.InstalledPlugin
	found := false
	for _, ip := range v.st.installed.List() {
		if ip.ID == id {
			target = ip
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	p := v.st.paths
	return func() tea.Msg {
		_, mkt := config.ParsePluginID(target.ID)
		head := install.LocalMarketplaceHead(p, mkt)
		s := updates.CheckPlugin(p, target, head)
		return pluginUpdateCheckMsg{id: target.ID, status: s}
	}
}

func (v *pluginView) updateAvailable(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		v.mode = ""
		v.availErr = ""
		v.index = 0
		v.top = 0
		return nil
	case "up", "k":
		if v.availIndex > 0 {
			v.availIndex--
		}
	case "down", "j":
		if v.availIndex < len(v.availRows)-1 {
			v.availIndex++
		}
	case "g":
		v.availIndex = 0
	case "G":
		if n := len(v.availRows); n > 0 {
			v.availIndex = n - 1
		}
	case "pgup":
		v.availIndex -= 10
		if v.availIndex < 0 {
			v.availIndex = 0
		}
	case "pgdn":
		if n := len(v.availRows); n > 0 {
			v.availIndex += 10
			if v.availIndex >= n {
				v.availIndex = n - 1
			}
		}
	case "I":
		if v.installing || v.availLoading || len(v.availRows) == 0 {
			return nil
		}
		r := v.availRows[v.availIndex]
		v.installing = true
		v.flash = styleProgress.Render("installing " + r.QualifiedID + "…")
		p, mkt, name := v.st.paths, r.Marketplace, r.Name
		return func() tea.Msg {
			result, err := install.Install(p, mkt, name)
			return pluginInstallResultMsg{result: result, err: err}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// render
// ---------------------------------------------------------------------------

func (v *pluginView) render() string {
	if v.mode == "available" {
		return v.renderAvailable()
	}
	if v.mode == "failures" {
		return v.renderFailures()
	}

	// First-render hook: lazy-load persisted failures from disk so an `F` keypress
	// (or the visible "(N failed)" hint) reflects the prior session's record. Cheap
	// stat + json read; runs only once per view.
	if !v.lastFailuresLoaded {
		if loaded, ok := loadLastFailures(v.st.paths.BackupsDir); ok {
			v.lastFailures = loaded
		}
		v.lastFailuresLoaded = true
	}

	visible := v.visibleRows()
	var enabled, disabled, remoteCount int
	for _, r := range v.rows {
		switch {
		case r.IsRemote:
			remoteCount++
		case r.Enabled:
			enabled++
		case r.Known:
			disabled++
		}
	}
	mode := "all"
	if v.showOnly != "" {
		mode = v.showOnly
	}
	localCount := len(visible) - countRemote(visible)
	remoteVis := countRemote(visible)
	outdated := 0
	for _, r := range v.rows {
		if r.Outdated {
			outdated++
		}
	}
	title := fmt.Sprintf("Plugins — %s  (showing %d/%d local, %d remote; %d enabled, %d disabled)",
		mode, localCount, len(v.rows)-remoteCount, remoteVis, enabled, disabled)
	if outdated > 0 {
		title += "  " + styleWarn.Render(fmt.Sprintf("(%d update available)", outdated))
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	if v.filterActive || v.filter.Value() != "" {
		b.WriteString(v.filter.View() + "\n")
	}
	if v.updating {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("update in progress…") + "\n")
	}
	if v.bulkUpdating {
		label := "bulk update in progress…"
		if total := len(v.bulkTargets); total > 0 {
			// Show "currently on item N of M", matching the flash counter semantics
			// instead of the prior "N completed of M" which was off-by-one from the
			// flash. Cap at total for the brief window between the last item landing
			// and the final result message arriving (when bulkIndex == total).
			current := v.bulkIndex + 1
			if current > total {
				current = total
			}
			label = fmt.Sprintf("bulk update in progress… (%d/%d)", current, total)
		}
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render(label) + "\n")
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

	// Find where remote rows start to insert a separator.
	remoteStart := firstRemoteIdx(visible)

	for i := v.top; i < end; i++ {
		// Separator before first remote row.
		if i == remoteStart && remoteStart >= 0 {
			b.WriteString(styleDim.Render("  ─── Remote (claude.ai) " + strings.Repeat("─", 40)))
			b.WriteString("\n")
		}

		r := visible[i]
		var mark string
		if r.IsRemote {
			if r.DisabledHere {
				mark = styleErr.Render("[-]")
			} else {
				mark = styleDim.Render("[~]")
			}
		} else {
			switch {
			case r.Enabled:
				mark = styleOK.Render("[x]")
			case !r.Known:
				mark = styleDim.Render("[?]")
			default:
				mark = "[ ]"
			}
		}
		line := fmt.Sprintf("%s %s", mark, r.ID)
		if !r.IsRemote && r.Version != "" {
			line += "  " + styleDim.Render("v"+r.Version)
		}
		if r.Outdated {
			line += "  " + styleWarn.Render("↑ update available")
		}
		if r.IsRemote && r.DisabledHere {
			line += "  " + styleDim.Render("(disabled here)")
		}
		if v.pendingRemove == r.ID {
			line += "  " + styleWarn.Render("← press x to confirm")
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

func (v *pluginView) renderAvailable() string {
	var b strings.Builder
	if v.availLoading {
		b.WriteString("Plugins — available  (loading…)\n")
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("fetching marketplace catalogs…"))
		return b.String()
	}
	if v.availErr != "" {
		b.WriteString("Plugins — available  (error)\n")
		b.WriteString(styleErr.Render("  " + v.availErr + "\n"))
		b.WriteString(styleDim.Render("  esc: back"))
		return b.String()
	}
	if len(v.availRows) == 0 {
		b.WriteString("Plugins — available  (none)\n")
		b.WriteString(styleDim.Render("  All marketplace plugins are already installed, or no marketplaces are cloned.\n"))
		b.WriteString(styleDim.Render("  esc: back"))
		return b.String()
	}

	b.WriteString(fmt.Sprintf("Plugins — available  (%d not installed)\n", len(v.availRows)))
	if v.installing {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("install in progress…") + "\n")
	}

	listHeight := v.h - 4
	if listHeight < 5 {
		listHeight = 5
	}
	if v.availIndex < v.availTop {
		v.availTop = v.availIndex
	}
	if v.availIndex >= v.availTop+listHeight {
		v.availTop = v.availIndex - listHeight + 1
	}
	end := v.availTop + listHeight
	if end > len(v.availRows) {
		end = len(v.availRows)
	}
	for i := v.availTop; i < end; i++ {
		r := v.availRows[i]
		line := fmt.Sprintf("[ ] %s  %s", r.QualifiedID, styleDim.Render("("+r.Marketplace+")"))
		if i == v.availIndex {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	if len(v.availRows) > listHeight {
		b.WriteString(styleDim.Render(fmt.Sprintf("  [%d-%d of %d]", v.availTop+1, end, len(v.availRows))))
	}
	return b.String()
}

func pluginFirstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func countRemote(rows []pluginRowView) int {
	n := 0
	for _, r := range rows {
		if r.IsRemote {
			n++
		}
	}
	return n
}

func (v *pluginView) resize(w, h int) { v.w, v.h = w, h }

func (v *pluginView) helpText() string {
	if v.mode == "available" {
		return "I: install selected  esc: back  j/k: navigate"
	}
	return "space: toggle  U: update  B: update all  x: remove  I: browse available  f: filter  A/N: all on/off  /: search"
}

func (v *pluginView) capturingInput() bool {
	// Treat sub-view modes as "input" for the dispatcher so the model's global
	// q/esc handler defers to the view first. Otherwise esc inside the failures
	// panel or available-plugins panel triggers a quit-confirm instead of closing
	// the panel.
	return v.filterActive || v.mode == "failures" || v.mode == "available"
}
