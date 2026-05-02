package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/updates"
)

// ---------------------------------------------------------------------------
// Row types
// ---------------------------------------------------------------------------

type marketplaceRowView struct {
	Name         string
	SourceType   string // github | git | local | (cloned-only)
	Source       string // owner/repo or url or path
	Cloned       bool
	NumPlugins   int
	NumInstalled int
	AutoUpdate   bool
	// update-status fields, populated async
	UpdateStatus  updates.Status
	UpdateChecked bool
}

// ---------------------------------------------------------------------------
// Async messages
// ---------------------------------------------------------------------------

type marketplaceUpdateCheckMsg struct {
	name   string
	status updates.Status
}

type marketplaceCheckDoneMsg struct{}

type marketplaceOpResultMsg struct {
	op   string // "add" | "remove" | "update" | "bulkUpdate"
	name string
	err  error
	// bulk fields
	updated []string
	failed  []string
}

// ---------------------------------------------------------------------------
// Add-form state
// ---------------------------------------------------------------------------

type marketplaceAddForm struct {
	step       int // 0 = name, 1 = source type cycle (text-prompt cycle), 2 = repo/path
	name       string
	sourceType string // github | git | local
	repo       string
	path       string
	field      textinput.Model
}

// ---------------------------------------------------------------------------
// View struct
// ---------------------------------------------------------------------------

type marketplaceView struct {
	st *state

	rows  []marketplaceRowView
	index int
	top   int
	w, h  int

	filter       textinput.Model
	filterActive bool

	loaded         bool
	checking       bool
	updating       bool
	bulkUpdating   bool
	pendingRemove  string
	addMode        bool
	addForm        marketplaceAddForm

	flash string
}

func newMarketplaceView(st *state) *marketplaceView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	v := &marketplaceView{st: st, filter: ti}
	v.rebuild()
	return v
}

// ---------------------------------------------------------------------------
// rebuild — assemble the row set from settings + cloned dirs + installed plugins
// ---------------------------------------------------------------------------

func (v *marketplaceView) rebuild() {
	// Map of name -> row, populated from:
	//  1. extraKnownMarketplaces (settings)
	//  2. cloned directories under pluginsDir/marketplaces
	//  3. marketplaces referenced by installed plugins (in case the dir exists but
	//     wasn't listed in settings)
	rowMap := map[string]*marketplaceRowView{}

	for _, mp := range v.st.settings.ExtraMarketplaces() {
		row := &marketplaceRowView{
			Name:       mp.Name,
			SourceType: mp.SourceType,
			AutoUpdate: mp.AutoUpdate,
		}
		switch mp.SourceType {
		case "github", "git":
			row.Source = mp.Repo
		case "local":
			row.Source = mp.Path
		}
		row.Cloned = install.IsMarketplaceCloned(v.st.paths, mp.Name)
		rowMap[mp.Name] = row
	}

	cloned, _ := install.ListLocalMarketplaces(v.st.paths)
	for _, name := range cloned {
		row, ok := rowMap[name]
		if !ok {
			row = &marketplaceRowView{Name: name, SourceType: "(cloned)", Cloned: true}
			rowMap[name] = row
		}
		row.Cloned = true
		// Count plugins from manifest.
		if m, _, err := install.LoadMarketplace(v.st.paths, name); err == nil {
			row.NumPlugins = len(m.Plugins)
		}
	}

	// Count installed-per-marketplace.
	for _, ip := range v.st.installed.List() {
		_, mkt := config.ParsePluginID(ip.ID)
		if mkt == "" {
			continue
		}
		if row, ok := rowMap[mkt]; ok {
			row.NumInstalled++
		} else {
			rowMap[mkt] = &marketplaceRowView{Name: mkt, SourceType: "(installed-only)", NumInstalled: 1}
		}
	}

	// Apply cached update status.
	for name, row := range rowMap {
		if s, ok := v.st.updates.Marketplace(name); ok {
			row.UpdateStatus = s
			row.UpdateChecked = true
		}
	}

	rows := make([]marketplaceRowView, 0, len(rowMap))
	for _, r := range rowMap {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	v.rows = rows
	if visible := v.visibleRows(); v.index >= len(visible) {
		v.index = 0
		v.top = 0
	}
}

func (v *marketplaceView) visibleRows() []marketplaceRowView {
	q := strings.ToLower(strings.TrimSpace(v.filter.Value()))
	if q == "" {
		return v.rows
	}
	out := make([]marketplaceRowView, 0, len(v.rows))
	for _, r := range v.rows {
		if strings.Contains(strings.ToLower(r.Name), q) {
			out = append(out, r)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

func (v *marketplaceView) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case marketplaceUpdateCheckMsg:
		v.st.updates.PutMarketplace(m.name, m.status)
		v.rebuild()
		return nil
	case marketplaceCheckDoneMsg:
		v.checking = false
		v.flash = styleDim.Render("update check complete")
		return nil
	case marketplaceOpResultMsg:
		v.updating = false
		v.bulkUpdating = false
		switch m.op {
		case "add":
			if m.err != nil {
				v.flash = styleErr.Render("add error: " + m.err.Error())
				return nil
			}
			v.st.dirtySettings = true
			v.rebuild()
			v.flash = styleOK.Render("added marketplace " + m.name)
			return v.checkAll()
		case "remove":
			if m.err != nil {
				v.flash = styleErr.Render("remove error: " + m.err.Error())
				return nil
			}
			v.st.dirtySettings = true
			v.st.updates.InvalidateMarketplace(m.name)
			v.rebuild()
			v.flash = styleOK.Render("removed marketplace " + m.name)
			return nil
		case "update":
			if m.err != nil {
				v.flash = styleErr.Render("update error: " + m.err.Error())
				return nil
			}
			v.st.updates.InvalidateMarketplace(m.name)
			v.rebuild()
			v.flash = styleOK.Render("pulled marketplace " + m.name)
			return v.checkOne(m.name)
		case "bulkUpdate":
			parts := []string{}
			if len(m.updated) > 0 {
				parts = append(parts, fmt.Sprintf("%d updated", len(m.updated)))
			}
			if len(m.failed) > 0 {
				parts = append(parts, styleErr.Render(fmt.Sprintf("%d failed", len(m.failed))))
			}
			if len(parts) == 0 {
				v.flash = styleDim.Render("no marketplaces to update")
			} else {
				v.flash = styleOK.Render("bulk update: " + strings.Join(parts, ", "))
			}
			for _, n := range m.updated {
				v.st.updates.InvalidateMarketplace(n)
			}
			v.rebuild()
			return v.checkAll()
		}
		return nil
	}

	// Filter input.
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

	// Add-form input.
	if v.addMode {
		return v.updateAddForm(msg)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	visible := v.visibleRows()
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
	case "/":
		v.filterActive = true
		v.filter.Focus()
		return textinput.Blink
	case "c":
		v.filter.SetValue("")
	case "R":
		return v.checkAll()
	case "a":
		v.addMode = true
		v.addForm = marketplaceAddForm{step: 0, sourceType: "github", field: textinputForm("name (e.g. claude-plugins-official)")}
		return textinput.Blink
	case "x":
		if len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		if v.pendingRemove == r.Name {
			name := r.Name
			v.pendingRemove = ""
			v.updating = true
			v.flash = styleDim.Render("removing " + name + "…")
			p := v.st.paths
			settings := v.st.settings
			installed := v.st.installed
			return func() tea.Msg {
				err := install.RemoveMarketplace(p, settings, installed, name, true)
				return marketplaceOpResultMsg{op: "remove", name: name, err: err}
			}
		}
		v.pendingRemove = r.Name
		v.flash = styleWarn.Render("press x again to remove " + r.Name + " (purges clone dir)")
	case "u":
		if v.updating || len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		if !r.Cloned {
			v.flash = styleDim.Render(r.Name + " is not cloned (add it first)")
			return nil
		}
		v.updating = true
		v.flash = styleDim.Render("git pull " + r.Name + "…")
		p := v.st.paths
		name := r.Name
		return func() tea.Msg {
			err := install.UpdateMarketplace(p, name)
			return marketplaceOpResultMsg{op: "update", name: name, err: err}
		}
	case "B":
		if v.bulkUpdating || v.updating {
			return nil
		}
		var targets []string
		for _, r := range v.rows {
			if r.Cloned {
				targets = append(targets, r.Name)
			}
		}
		if len(targets) == 0 {
			v.flash = styleDim.Render("no cloned marketplaces to update")
			return nil
		}
		v.bulkUpdating = true
		v.flash = styleDim.Render(fmt.Sprintf("updating %d marketplace(s)…", len(targets)))
		p := v.st.paths
		return func() tea.Msg {
			var updated, failed []string
			for _, n := range targets {
				if err := install.UpdateMarketplace(p, n); err != nil {
					failed = append(failed, n)
					continue
				}
				updated = append(updated, n)
			}
			return marketplaceOpResultMsg{op: "bulkUpdate", updated: updated, failed: failed}
		}
	case "I":
		if len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		if !r.Cloned {
			v.flash = styleDim.Render(r.Name + " is not cloned — press u to clone via git pull")
			return nil
		}
		// No actual install here — surface a hint pointing to the Plugins tab's
		// existing browse-and-install workflow. Plumbing a marketplace filter into
		// pluginView later would be a follow-up.
		v.flash = styleDim.Render(fmt.Sprintf("%d plugins in %s — switch to Plugins tab and press I to browse", r.NumPlugins, r.Name))
	}
	return nil
}

// updateAddForm drives the multi-step add wizard. The sequence is:
//   1. name
//   2. source type (cycle github|git|local with space; enter to accept)
//   3. repo (for github/git) or path (for local)
//   4. submit — clones and saves
func (v *marketplaceView) updateAddForm(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			v.addMode = false
			v.flash = styleDim.Render("add cancelled")
			return nil
		case "enter":
			val := strings.TrimSpace(v.addForm.field.Value())
			switch v.addForm.step {
			case 0:
				if val == "" {
					return nil
				}
				v.addForm.name = val
				v.addForm.step = 1
				v.addForm.field = textinputForm("source type [github/git/local] (default: github)")
				return textinput.Blink
			case 1:
				if val == "" {
					val = "github"
				}
				if val != "github" && val != "git" && val != "local" {
					v.flash = styleErr.Render("invalid source type — use github, git, or local")
					return nil
				}
				v.addForm.sourceType = val
				v.addForm.step = 2
				switch val {
				case "github":
					v.addForm.field = textinputForm("repo (owner/name)")
				case "git":
					v.addForm.field = textinputForm("git URL")
				case "local":
					v.addForm.field = textinputForm("local path")
				}
				return textinput.Blink
			case 2:
				if val == "" {
					return nil
				}
				name := v.addForm.name
				mp := config.Marketplace{Name: name, SourceType: v.addForm.sourceType, AutoUpdate: true}
				switch v.addForm.sourceType {
				case "github", "git":
					mp.Repo = val
				case "local":
					mp.Path = val
				}
				v.addMode = false
				v.updating = true
				v.flash = styleDim.Render("cloning " + name + "…")
				p := v.st.paths
				settings := v.st.settings
				return func() tea.Msg {
					err := install.AddMarketplace(p, settings, mp)
					return marketplaceOpResultMsg{op: "add", name: name, err: err}
				}
			}
			return nil
		}
	}
	var cmd tea.Cmd
	v.addForm.field, cmd = v.addForm.field.Update(msg)
	return cmd
}

// ---------------------------------------------------------------------------
// async helpers
// ---------------------------------------------------------------------------

func (v *marketplaceView) checkOne(name string) tea.Cmd {
	p := v.st.paths
	return func() tea.Msg {
		s := updates.CheckMarketplace(p, name)
		return marketplaceUpdateCheckMsg{name: name, status: s}
	}
}

func (v *marketplaceView) checkAll() tea.Cmd {
	if v.checking {
		return nil
	}
	var targets []string
	for _, r := range v.rows {
		if r.Cloned {
			targets = append(targets, r.Name)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	v.checking = true
	p := v.st.paths
	cmds := make([]tea.Cmd, 0, len(targets)+1)
	for _, n := range targets {
		name := n
		cmds = append(cmds, func() tea.Msg {
			return marketplaceUpdateCheckMsg{name: name, status: updates.CheckMarketplace(p, name)}
		})
	}
	cmds = append(cmds, func() tea.Msg { return marketplaceCheckDoneMsg{} })
	return tea.Batch(cmds...)
}

// initialCheckCmd returns the lazy-load update check Cmd if it hasn't fired yet.
// Called by the model when the user first switches into this tab.
func (v *marketplaceView) initialCheckCmd() tea.Cmd {
	if v.loaded {
		return nil
	}
	v.loaded = true
	return v.checkAll()
}

// ---------------------------------------------------------------------------
// render
// ---------------------------------------------------------------------------

func (v *marketplaceView) render() string {
	if v.addMode {
		return v.renderAddForm()
	}

	visible := v.visibleRows()
	outdated := 0
	for _, r := range v.rows {
		if r.UpdateStatus.Outdated {
			outdated++
		}
	}
	title := fmt.Sprintf("Marketplaces — %d total, %d cloned", len(v.rows), countCloned(v.rows))
	if outdated > 0 {
		title += "  " + styleWarn.Render(fmt.Sprintf("(%d update available)", outdated))
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	if v.filterActive || v.filter.Value() != "" {
		b.WriteString(v.filter.View() + "\n")
	}
	if v.checking {
		b.WriteString(styleDim.Render("  (checking for updates…)") + "\n")
	}
	if v.updating {
		b.WriteString(styleDim.Render("  (operation in progress…)") + "\n")
	}
	if v.bulkUpdating {
		b.WriteString(styleDim.Render("  (bulk update in progress…)") + "\n")
	}

	if len(visible) == 0 {
		b.WriteString(styleDim.Render("  (no marketplaces — press a to add)"))
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
		r := visible[i]
		mark := "[ ]"
		if r.Cloned {
			mark = styleOK.Render("[c]")
		} else {
			mark = styleDim.Render("[?]")
		}
		src := r.SourceType
		if r.Source != "" {
			src += " " + r.Source
		}
		line := fmt.Sprintf("%s %s  %s", mark, r.Name, styleDim.Render("("+src+")"))
		if r.NumPlugins > 0 {
			line += "  " + styleDim.Render(fmt.Sprintf("%d plugin(s), %d installed", r.NumPlugins, r.NumInstalled))
		}
		if r.UpdateStatus.Outdated {
			line += "  " + styleWarn.Render("↑ update available")
		} else if r.UpdateChecked && r.UpdateStatus.Err == nil && r.UpdateStatus.Remote != "" {
			line += "  " + styleDim.Render("✓ up to date")
		}
		if v.pendingRemove == r.Name {
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

func (v *marketplaceView) renderAddForm() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Add marketplace"))
	b.WriteString("\n")
	b.WriteString(styleDim.Render("  esc: cancel  enter: next/submit"))
	b.WriteString("\n\n")
	if v.addForm.name != "" {
		b.WriteString("  name: " + styleOK.Render(v.addForm.name) + "\n")
	}
	if v.addForm.step >= 1 {
		b.WriteString("  source type: " + styleOK.Render(v.addForm.sourceType) + "\n")
	}
	b.WriteString("\n  ")
	b.WriteString(v.addForm.field.View())
	return b.String()
}

func countCloned(rows []marketplaceRowView) int {
	n := 0
	for _, r := range rows {
		if r.Cloned {
			n++
		}
	}
	return n
}

func textinputForm(prompt string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = prompt + ": "
	ti.Focus()
	return ti
}

func (v *marketplaceView) resize(w, h int) { v.w, v.h = w, h }

func (v *marketplaceView) helpText() string {
	if v.addMode {
		return "esc: cancel  enter: next/submit"
	}
	return "a: add  x: remove  u: update  B: update all  R: refresh check  /: filter  I: browse plugins"
}

func (v *marketplaceView) capturingInput() bool { return v.filterActive || v.addMode }
