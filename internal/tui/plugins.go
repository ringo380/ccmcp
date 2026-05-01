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

type pluginInstallResultMsg struct {
	result *install.Result
	err    error
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
	updating  bool
	installing bool

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
		rows = append(rows, pluginRowView{ID: e.ID, Enabled: e.Enabled, Known: true, Installed: ip.InstallPath != "", Version: ip.Version})
	}
	for _, ip := range v.st.installed.List() {
		if seen[ip.ID] {
			continue
		}
		rows = append(rows, pluginRowView{ID: ip.ID, Installed: true, Version: ip.Version})
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
	if v.index >= len(rows) {
		v.index = 0
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
		v.flash = styleDim.Render("updating " + r.ID + "…")
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
		v.flash = styleDim.Render("loading marketplace catalogs…")
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
	}
	return nil
}

func (v *pluginView) updateAvailable(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		v.mode = ""
		v.availErr = ""
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
		v.availIndex = len(v.availRows) - 1
	case "pgup":
		v.availIndex -= 10
		if v.availIndex < 0 {
			v.availIndex = 0
		}
	case "pgdn":
		v.availIndex += 10
		if v.availIndex >= len(v.availRows) {
			v.availIndex = len(v.availRows) - 1
		}
	case "I":
		if v.installing || v.availLoading || len(v.availRows) == 0 {
			return nil
		}
		r := v.availRows[v.availIndex]
		v.installing = true
		v.flash = styleDim.Render("installing " + r.QualifiedID + "…")
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
	title := fmt.Sprintf("Plugins — %s  (showing %d/%d local, %d remote; %d enabled, %d disabled)",
		mode, localCount, len(v.rows)-remoteCount, remoteVis, enabled, disabled)

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	if v.filterActive || v.filter.Value() != "" {
		b.WriteString(v.filter.View() + "\n")
	}
	if v.updating {
		b.WriteString(styleDim.Render("  (update in progress…)") + "\n")
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
		b.WriteString(styleDim.Render("  fetching marketplace catalogs…"))
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
		b.WriteString(styleDim.Render("  (install in progress…)") + "\n")
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
	return "space: toggle  U: update  x: remove  I: browse available  f: filter-mode  A/N: all on/off  /: search"
}

func (v *pluginView) capturingInput() bool { return v.filterActive }
