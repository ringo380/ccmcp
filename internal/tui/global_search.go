package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

// searchEntry is one indexed row from a tab, used by the global search overlay.
type searchEntry struct {
	tab      tabID
	tabLabel string // badge text, e.g. "mcps" (lowercased tab label)
	label    string // display text in the result list
	key      string // stable identifier handed back to focusSearch
	haystack string // lowercased text matched against the query
}

// searchProvider is implemented by every view that participates in global
// search. searchEntries ensures the view's data is loaded and returns its full
// (unfiltered) row set; focusSearch clears the view's local filter state and
// positions the cursor on the row identified by key.
type searchProvider interface {
	searchEntries() []searchEntry
	focusSearch(key string)
}

// globalSearchState holds the cross-tab search overlay. Owned by the top-level
// model (not a tab) so it can dispatch into any view.
type globalSearchState struct {
	active  bool
	input   textinput.Model
	all     []searchEntry // full index built when the overlay opens
	results []searchEntry // current filtered subset
	index   int           // cursor within results
	top     int           // scroll offset within results
}

func newGlobalSearchState() globalSearchState {
	ti := textinput.New()
	ti.Prompt = "search: "
	ti.CharLimit = 64
	return globalSearchState{input: ti}
}

// searchProviders returns the views in tab order so a result's badge/tab can be
// derived from its position. Every view implements searchProvider.
func (m *model) searchProviders() []searchProvider {
	return []searchProvider{
		tabMCPs:         m.mcps,
		tabPlugins:      m.plugins,
		tabMarketplaces: m.marketplaces,
		tabDiscover:     m.discover,
		tabSkills:       m.skills,
		tabAgents:       m.agents,
		tabCommands:     m.commands,
		tabProfiles:     m.profiles,
		tabSummary:      m.summary,
		tabDoctor:       m.doctor,
	}
}

// openGlobalSearch builds the index from every provider and activates the overlay.
func (m *model) openGlobalSearch() tea.Cmd {
	gs := &m.globalSearch
	gs.all = gs.all[:0]
	for id, p := range m.searchProviders() {
		entries := p.searchEntries()
		label := strings.ToLower(tabs[id].label)
		for _, e := range entries {
			e.tab = tabID(id)
			e.tabLabel = label
			gs.all = append(gs.all, e)
		}
	}
	gs.active = true
	gs.index = 0
	gs.top = 0
	gs.input.SetValue("")
	gs.input.Focus()
	gs.applyFilter()
	return textinput.Blink
}

// applyFilter recomputes results from all using a case-insensitive subsequence
// match against each entry's haystack. An empty query matches everything.
func (gs *globalSearchState) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(gs.input.Value()))
	if q == "" {
		gs.results = append(gs.results[:0], gs.all...)
	} else {
		gs.results = gs.results[:0]
		for _, e := range gs.all {
			if subsequenceMatch(e.haystack, q) {
				gs.results = append(gs.results, e)
			}
		}
	}
	if gs.index >= len(gs.results) {
		gs.index = len(gs.results) - 1
	}
	if gs.index < 0 {
		gs.index = 0
	}
	gs.top = 0
}

// subsequenceMatch reports whether every rune of needle appears in haystack in
// order (fzf-lite). Both args are expected lowercased.
func subsequenceMatch(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	ni := 0
	nr := []rune(needle)
	for _, hr := range haystack {
		if hr == nr[ni] {
			ni++
			if ni == len(nr) {
				return true
			}
		}
	}
	return false
}

// updateGlobalSearch handles all messages while the overlay is active.
func (m *model) updateGlobalSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	gs := &m.globalSearch
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		// Forward non-key messages (e.g. cursor blink) to the input.
		var cmd tea.Cmd
		gs.input, cmd = gs.input.Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "esc":
		gs.active = false
		gs.input.Blur()
		return m, nil
	case "enter":
		if gs.index >= 0 && gs.index < len(gs.results) {
			e := gs.results[gs.index]
			gs.active = false
			gs.input.Blur()
			m.tab = e.tab
			m.message = ""
			m.searchProviders()[e.tab].focusSearch(e.key)
			return m, m.tabEnterCmd()
		}
		return m, nil
	case "up", "ctrl+p":
		if gs.index > 0 {
			gs.index--
		}
		return m, nil
	case "down", "ctrl+n":
		if gs.index < len(gs.results)-1 {
			gs.index++
		}
		return m, nil
	case "pgup":
		gs.index -= 10
		if gs.index < 0 {
			gs.index = 0
		}
		return m, nil
	case "pgdn":
		gs.index += 10
		if gs.index >= len(gs.results) {
			gs.index = len(gs.results) - 1
		}
		if gs.index < 0 {
			gs.index = 0
		}
		return m, nil
	default:
		var cmd tea.Cmd
		gs.input, cmd = gs.input.Update(msg)
		gs.applyFilter()
		return m, cmd
	}
}

// renderGlobalSearch draws the overlay body: the input line and a scrollable,
// cursor-highlighted result list with a per-row tab badge.
func (m *model) renderGlobalSearch() string {
	gs := &m.globalSearch
	var b strings.Builder
	fmt.Fprintf(&b, "Global search (%d)\n", len(gs.results))
	b.WriteString(gs.input.View() + "\n")
	if len(gs.results) == 0 {
		b.WriteString(styleDim.Render("  (no matches)"))
		return b.String()
	}
	pageH := m.height - reservedHeight - 2
	if pageH < 4 {
		pageH = 4
	}
	if gs.index < gs.top {
		gs.top = gs.index
	}
	if gs.index >= gs.top+pageH {
		gs.top = gs.index - pageH + 1
	}
	end := gs.top + pageH
	if end > len(gs.results) {
		end = len(gs.results)
	}
	for i := gs.top; i < end; i++ {
		e := gs.results[i]
		badge := styleBadge.Render(e.tabLabel)
		line := fmt.Sprintf("  %s  %s", badge, e.label)
		if i == gs.index {
			b.WriteString(styleSelected.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- per-view searchProvider implementations ---
//
// Colocated here so the global-search surface lives in one file. Each
// searchEntries ensures the view's data is loaded then emits one entry per
// full-set row; each focusSearch clears the view's local filter so the target
// is visible, then positions the cursor by rescanning for the identifier
// (robust to index drift).

func (v *mcpView) searchEntries() []searchEntry {
	if len(v.rows) == 0 {
		v.rebuild()
	}
	out := make([]searchEntry, 0, len(v.rows))
	for _, r := range v.rows {
		label := r.Name
		if b := badgeFor(r.Source); b != "" {
			label += " (" + b + ")"
		}
		out = append(out, searchEntry{
			label:    label,
			key:      r.RowKey(),
			haystack: strings.ToLower(r.Name + " " + r.Description),
		})
	}
	return out
}

func (v *mcpView) focusSearch(key string) {
	v.filter.SetValue("")
	v.filterActive = false
	v.showHidden = true
	for i, r := range v.visibleRows() {
		if r.RowKey() == key {
			v.index = i
			break
		}
	}
	v.top = 0
}

func (v *pluginView) searchEntries() []searchEntry {
	if len(v.rows) == 0 {
		v.rebuild()
	}
	out := make([]searchEntry, 0, len(v.rows))
	for _, r := range v.rows {
		out = append(out, searchEntry{
			label:    r.ID,
			key:      r.ID,
			haystack: strings.ToLower(r.ID + " " + r.RemoteKey),
		})
	}
	return out
}

func (v *pluginView) focusSearch(key string) {
	v.filter.SetValue("")
	v.filterActive = false
	for i, r := range v.visibleRows() {
		if r.ID == key {
			v.index = i
			break
		}
	}
}

func (v *marketplaceView) searchEntries() []searchEntry {
	if len(v.rows) == 0 {
		v.rebuild()
	}
	out := make([]searchEntry, 0, len(v.rows))
	for _, r := range v.rows {
		out = append(out, searchEntry{
			label:    r.Name,
			key:      r.Name,
			haystack: strings.ToLower(r.Name + " " + r.Source),
		})
	}
	return out
}

func (v *marketplaceView) focusSearch(key string) {
	v.filter.SetValue("")
	v.filterActive = false
	for i, r := range v.visibleRows() {
		if r.Name == key {
			v.index = i
			break
		}
	}
}

func (v *discoveryView) searchEntries() []searchEntry {
	// Discover is network-backed - only index rows already fetched. Never force
	// a fetch from the overlay.
	if !v.loaded {
		return nil
	}
	out := make([]searchEntry, 0, len(v.rows))
	for _, r := range v.rows {
		out = append(out, searchEntry{
			label:    r.Name,
			key:      r.Name,
			haystack: strings.ToLower(r.Name + " " + r.Description),
		})
	}
	return out
}

func (v *discoveryView) focusSearch(key string) {
	v.filter.SetValue("")
	v.filterActive = false
	for i, r := range v.visibleRows() {
		if r.Name == key {
			v.index = i
			break
		}
	}
}

func (v *skillView) searchEntries() []searchEntry {
	if v.allSkills == nil {
		v.load()
	}
	out := make([]searchEntry, 0, len(v.allSkills))
	for _, s := range v.allSkills {
		out = append(out, searchEntry{
			label:    s.Name,
			key:      s.Name,
			haystack: strings.ToLower(s.Name + " " + s.Description),
		})
	}
	return out
}

func (v *skillView) focusSearch(key string) {
	v.filterText = ""
	v.filter.SetValue("")
	v.filterActive = false
	v.applyFilter()
	for i, s := range v.rows {
		if s.Name == key {
			v.index = i
			break
		}
	}
	v.top = 0
}

func (v *agentView) searchEntries() []searchEntry {
	if v.allAgents == nil {
		v.load()
	}
	out := make([]searchEntry, 0, len(v.allAgents))
	for _, a := range v.allAgents {
		out = append(out, searchEntry{
			label:    a.Name,
			key:      a.Name,
			haystack: strings.ToLower(a.Name + " " + a.Description),
		})
	}
	return out
}

func (v *agentView) focusSearch(key string) {
	v.filterText = ""
	v.filter.SetValue("")
	v.filterActive = false
	v.applyFilter()
	for i, a := range v.rows {
		if a.Name == key {
			v.index = i
			break
		}
	}
	v.top = 0
}

func (v *commandView) searchEntries() []searchEntry {
	if v.allCmds == nil {
		v.load()
	}
	out := make([]searchEntry, 0, len(v.allCmds))
	for _, c := range v.allCmds {
		out = append(out, searchEntry{
			label:    c.Effective,
			key:      c.Effective,
			haystack: strings.ToLower(c.Effective + " " + c.Description),
		})
	}
	return out
}

func (v *commandView) focusSearch(key string) {
	v.filterText = ""
	v.filter.SetValue("")
	v.filterActive = false
	v.conflictsOnly = false
	v.applyFilter()
	for i, c := range v.rows {
		if c.Effective == key {
			v.index = i
			break
		}
	}
	v.top = 0
}

func (v *profileView) searchEntries() []searchEntry {
	v.rebuild()
	out := make([]searchEntry, 0, len(v.names))
	for _, n := range v.names {
		out = append(out, searchEntry{
			label:    n,
			key:      n,
			haystack: strings.ToLower(n),
		})
	}
	return out
}

func (v *profileView) focusSearch(key string) {
	for i, n := range v.names {
		if n == key {
			v.index = i
			break
		}
	}
}

func (v *summaryView) searchEntries() []searchEntry {
	v.rows = v.buildRows()
	fixable := v.fixableRows()
	out := make([]searchEntry, 0, len(fixable))
	for _, r := range fixable {
		label := r.key
		if r.project != "" {
			label += " (" + r.project + ")"
		}
		out = append(out, searchEntry{
			label:    label,
			key:      summaryRowKey(r),
			haystack: strings.ToLower(r.key + " " + r.project),
		})
	}
	return out
}

func (v *summaryView) focusSearch(key string) {
	for i, r := range v.fixableRows() {
		if summaryRowKey(r) == key {
			v.cursor = i
			break
		}
	}
	v.top = 0
}

// summaryRowKey builds a stable identifier for a fixable summary row. r.key
// alone can repeat across projects (same override key, different project), so
// the project path is folded in.
func summaryRowKey(r summaryRow) string {
	return r.key + "\x00" + r.project
}

func (v *doctorView) searchEntries() []searchEntry {
	if !v.loaded {
		v.runLint()
	}
	out := make([]searchEntry, 0, len(v.allIssues))
	for _, iss := range v.allIssues {
		out = append(out, searchEntry{
			label:    iss.String(),
			key:      doctorIssueKey(iss),
			haystack: strings.ToLower(iss.Code + " " + iss.File + " " + iss.Message),
		})
	}
	return out
}

func (v *doctorView) focusSearch(key string) {
	if !v.loaded {
		v.runLint()
	}
	for i, iss := range v.allIssues {
		if doctorIssueKey(iss) == key {
			v.cursor = i
			break
		}
	}
	v.top = 0
}

func doctorIssueKey(iss doctor.Issue) string {
	return fmt.Sprintf("%s|%s|%d", iss.Code, iss.File, iss.Line)
}
