package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/discovery"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/skills"
)

// discoveryView is the "Discover" tab — browse remote marketplaces, drill into
// their plugin lists, and preview-clone individual plugins to surface conflict
// reports against the currently-installed state.
type discoveryView struct {
	st *state

	w, h   int
	loaded bool
	flash  string

	// modeList → modePlugins → modeDetail. Esc/`b` pops up one level.
	mode discoveryMode

	// list state
	rows  []discovery.RemoteMarketplace
	index int
	top   int

	// plugin-list state (modePlugins)
	curMP    discovery.RemoteMarketplace
	plugins  []discovery.RemotePlugin
	pIndex   int
	pTop     int
	pErr     error
	pLoading bool

	// detail state (modeDetail)
	curPlugin   discovery.RemotePlugin
	preview     *discovery.PreviewResult
	report      discovery.ConflictReport
	detailErr   error
	detailReady bool
	detailBusy  bool

	// last fetch metadata
	fetchedAt   time.Time
	fromCache   bool
	srcErrors   map[string]string
	fetchBusy   bool
}

type discoveryMode int

const (
	modeList discoveryMode = iota
	modePlugins
	modeDetail
)

// async messages
type discoveryFetchedMsg struct{ res *discovery.DiscoveryResult; err error }
type discoveryManifestMsg struct {
	mp      discovery.RemoteMarketplace
	plugins []discovery.RemotePlugin
	err     error
}
type discoveryPreviewMsg struct {
	plugin  discovery.RemotePlugin
	preview *discovery.PreviewResult
	report  discovery.ConflictReport
	err     error
}

func newDiscoveryView(st *state) *discoveryView { return &discoveryView{st: st} }

func (v *discoveryView) resize(w, h int)         { v.w, v.h = w, h }
func (v *discoveryView) capturingInput() bool    { return false }

func (v *discoveryView) helpText() string {
	switch v.mode {
	case modeList:
		return "enter: drill in  r: refresh  j/k: nav  g/G: top/bottom"
	case modePlugins:
		return "enter: preview plugin  b/esc: back  j/k: nav"
	case modeDetail:
		return "b/esc: back"
	}
	return ""
}

// initialCheckCmd kicks off the first network fetch when the user navigates to
// the Discover tab. Subsequent visits reuse cached state until `r` is pressed.
func (v *discoveryView) initialCheckCmd() tea.Cmd {
	if v.loaded {
		return nil
	}
	v.loaded = true
	v.fetchBusy = true
	return v.fetchCmd(false)
}

func (v *discoveryView) fetchCmd(refresh bool) tea.Cmd {
	// Capture immutable values on the bubbletea goroutine. Reading
	// v.st.settings.DiscoverySources() inside the closure would race with
	// concurrent settings mutations on other tabs. Same defensive pattern
	// landed for plugin bulk-update in PR #17.
	sources := buildDiscoverySources(v.st.settings.DiscoverySources())
	cachePath := discovery.CachePath(v.st.paths.ClaudeConfigDir)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		opts := discovery.Options{
			Sources:   sources,
			CachePath: cachePath,
			Refresh:   refresh,
		}
		res, err := discovery.Discover(ctx, opts)
		return discoveryFetchedMsg{res: res, err: err}
	}
}

// buildDiscoverySources merges DefaultSources with a snapshot of user-
// configured registry URLs. Takes a slice (not a *Settings) so callers must
// snapshot on their own goroutine.
func buildDiscoverySources(userURLs []string) []discovery.Source {
	out := discovery.DefaultSources()
	for _, u := range userURLs {
		out = append(out, discovery.UserURLSource(u))
	}
	return out
}

func (v *discoveryView) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case discoveryFetchedMsg:
		v.fetchBusy = false
		if m.err != nil {
			v.flash = styleErr.Render("discover: " + m.err.Error())
			return nil
		}
		v.rows = m.res.Marketplaces
		v.fetchedAt = m.res.FetchedAt
		v.fromCache = m.res.FromCache
		v.srcErrors = m.res.Errors
		if v.index >= len(v.rows) {
			v.index = 0
			v.top = 0
		}
		return nil
	case discoveryManifestMsg:
		v.pLoading = false
		v.pErr = m.err
		v.plugins = m.plugins
		v.pIndex = 0
		v.pTop = 0
		return nil
	case discoveryPreviewMsg:
		v.detailBusy = false
		v.detailErr = m.err
		v.preview = m.preview
		v.report = m.report
		v.curPlugin = m.plugin
		v.detailReady = m.err == nil
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	switch v.mode {
	case modeList:
		return v.updateList(key)
	case modePlugins:
		return v.updatePlugins(key)
	case modeDetail:
		return v.updateDetail(key)
	}
	return nil
}

func (v *discoveryView) updateList(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "up", "k":
		if v.index > 0 {
			v.index--
		}
	case "down", "j":
		if v.index < len(v.rows)-1 {
			v.index++
		}
	case "g":
		v.index = 0
		v.top = 0
	case "G":
		v.index = len(v.rows) - 1
	case "r":
		if v.fetchBusy {
			return nil
		}
		v.fetchBusy = true
		v.flash = styleProgress.Render("refreshing discovery sources…")
		return v.fetchCmd(true)
	case "enter":
		if len(v.rows) == 0 {
			return nil
		}
		v.curMP = v.rows[v.index]
		v.plugins = nil
		v.pErr = nil
		v.pIndex = 0
		v.mode = modePlugins
		v.pLoading = true
		mp := v.curMP
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pls, err := discovery.FetchManifestPlugins(ctx, discovery.NewHTTPClient(15*time.Second), mp)
			return discoveryManifestMsg{mp: mp, plugins: pls, err: err}
		}
	}
	return nil
}

func (v *discoveryView) updatePlugins(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc", "b":
		v.mode = modeList
	case "up", "k":
		if v.pIndex > 0 {
			v.pIndex--
		}
	case "down", "j":
		if v.pIndex < len(v.plugins)-1 {
			v.pIndex++
		}
	case "enter":
		if len(v.plugins) == 0 || v.detailBusy {
			return nil
		}
		plugin := v.plugins[v.pIndex]
		v.detailBusy = true
		v.detailReady = false
		v.detailErr = nil
		v.preview = nil
		v.report = discovery.ConflictReport{}
		v.mode = modeDetail
		mp := v.curMP
		paths := v.st.paths
		state := buildTUIConflictState(v.st)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			pr, err := discovery.PreviewClone(ctx, paths, mp, plugin)
			if err != nil {
				return discoveryPreviewMsg{plugin: plugin, err: err}
			}
			rep := discovery.DetectConflicts(pr.Dir, mp, plugin, state)
			return discoveryPreviewMsg{plugin: plugin, preview: pr, report: rep}
		}
	}
	return nil
}

func (v *discoveryView) updateDetail(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc", "b":
		v.mode = modePlugins
	}
	return nil
}

// buildTUIConflictState assembles a discovery.ConflictState from the loaded
// in-memory state. Mirrors cmd/discover.go but uses the already-loaded state.
func buildTUIConflictState(st *state) discovery.ConflictState {
	skillRows := skills.Discover(st.paths.ClaudeConfigDir, st.project, st.settings, st.installed, st.paths.PluginsDir)
	agentRows := agents.Discover(st.paths.ClaudeConfigDir, st.project, st.settings, st.installed, st.paths.PluginsDir)
	cmdRows := commands.Discover(st.paths.ClaudeConfigDir, st.project, st.settings, st.installed, st.paths.PluginsDir)

	var mcpKeys []string
	for k := range st.cj.UserMCPs() {
		mcpKeys = append(mcpKeys, k)
	}
	for k := range st.cj.ProjectMCPs(st.project) {
		mcpKeys = append(mcpKeys, k)
	}

	var mktNames []string
	for _, mp := range st.settings.ExtraMarketplaces() {
		mktNames = append(mktNames, mp.Name)
	}
	cloned, _ := install.ListLocalMarketplaces(st.paths)
	mktNames = append(mktNames, cloned...)

	var ids []string
	for _, ip := range st.installed.List() {
		ids = append(ids, ip.ID)
	}

	return discovery.BuildState(skillRows, agentRows, cmdRows, mcpKeys, nil, mktNames, ids)
}

// ---- render ----------------------------------------------------------------

func (v *discoveryView) render() string {
	switch v.mode {
	case modeList:
		return v.renderList()
	case modePlugins:
		return v.renderPlugins()
	case modeDetail:
		return v.renderDetail()
	}
	return ""
}

func (v *discoveryView) renderList() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Discover — %d marketplace(s)", len(v.rows)))
	if v.fromCache {
		b.WriteString("  " + styleDim.Render(fmt.Sprintf("(cached %s ago)", time.Since(v.fetchedAt).Round(time.Second))))
	}
	b.WriteString("\n")
	if v.fetchBusy {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("fetching…") + "\n")
	}
	if len(v.rows) == 0 && !v.fetchBusy {
		b.WriteString(styleDim.Render("  (no marketplaces — press r to retry)") + "\n")
	}

	listH := v.h - 6
	if listH < 5 {
		listH = 5
	}
	if v.index < v.top {
		v.top = v.index
	}
	if v.index >= v.top+listH {
		v.top = v.index - listH + 1
	}
	end := v.top + listH
	if end > len(v.rows) {
		end = len(v.rows)
	}

	installedNames := installedMarketplaceNames(v.st)
	for i := v.top; i < end; i++ {
		r := v.rows[i]
		marker := styleDim.Render("[+]")
		if _, ok := installedNames[r.Name]; ok {
			marker = styleWarn.Render("[=]")
		}
		src := r.Source
		if r.Repo != "" {
			src = r.Source + " " + r.Repo
		}
		line := fmt.Sprintf("%s %s  %s", marker, r.Name, styleDim.Render("("+src+")"))
		if r.Description != "" {
			line += "  " + styleDim.Render(truncateD(r.Description, 60))
		}
		if i == v.index {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}

	if len(v.srcErrors) > 0 {
		b.WriteString("\n" + styleDim.Render("source errors:") + "\n")
		ids := make([]string, 0, len(v.srcErrors))
		for k := range v.srcErrors {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		for _, id := range ids {
			b.WriteString("  " + styleErr.Render(id+": "+truncateD(v.srcErrors[id], 80)) + "\n")
		}
	}
	return b.String()
}

func (v *discoveryView) renderPlugins() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Discover › %s — %d plugin(s)", v.curMP.Name, len(v.plugins)))
	b.WriteString("\n")
	if v.pLoading {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("fetching manifest…") + "\n")
	}
	if v.pErr != nil {
		b.WriteString(styleErr.Render("  error: "+v.pErr.Error()) + "\n")
		return b.String()
	}
	listH := v.h - 4
	if listH < 5 {
		listH = 5
	}
	if v.pIndex < v.pTop {
		v.pTop = v.pIndex
	}
	if v.pIndex >= v.pTop+listH {
		v.pTop = v.pIndex - listH + 1
	}
	end := v.pTop + listH
	if end > len(v.plugins) {
		end = len(v.plugins)
	}
	for i := v.pTop; i < end; i++ {
		p := v.plugins[i]
		line := "  " + p.Name
		if i == v.pIndex {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (v *discoveryView) renderDetail() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Discover › %s › %s", v.curMP.Name, v.curPlugin.Name))
	b.WriteString("\n")
	if v.detailBusy {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("cloning + scanning…") + "\n")
		return b.String()
	}
	if v.detailErr != nil {
		b.WriteString(styleErr.Render("  error: "+v.detailErr.Error()) + "\n")
		return b.String()
	}
	if !v.detailReady {
		return b.String()
	}
	b.WriteString(styleDim.Render(fmt.Sprintf("  preview: %s @ %s", v.preview.Repo, shortShaTUI(v.preview.Sha))) + "\n")
	b.WriteString(styleDim.Render("  cache:   "+v.preview.Dir) + "\n\n")

	if v.report.Empty() {
		b.WriteString(styleOK.Render("  ✓ no conflicts") + "\n")
		return b.String()
	}
	b.WriteString(styleWarn.Render(fmt.Sprintf("  %d conflict(s)", v.report.Total())) + "\n")
	if v.report.MarketplaceNameClash {
		b.WriteString("    " + styleErr.Render("marketplace name already known") + "\n")
	}
	if v.report.PluginIDClash {
		b.WriteString("    " + styleErr.Render("plugin ID already installed") + "\n")
	}
	renderConflictGroup(&b, "skill", v.report.Skills)
	renderConflictGroup(&b, "agent", v.report.Agents)
	renderConflictGroup(&b, "command", v.report.Commands)
	renderConflictGroup(&b, "mcp-server", v.report.MCPServers)
	renderConflictGroup(&b, "hook", v.report.Hooks)
	return b.String()
}

func renderConflictGroup(b *strings.Builder, kind string, conflicts []discovery.Conflict) {
	for _, c := range conflicts {
		b.WriteString("    ")
		b.WriteString(styleErr.Render(kind + " " + c.Name))
		b.WriteString(styleDim.Render("  ← " + c.ExistingSource))
		b.WriteString("\n")
	}
}

func installedMarketplaceNames(st *state) map[string]struct{} {
	out := map[string]struct{}{}
	for _, mp := range st.settings.ExtraMarketplaces() {
		out[mp.Name] = struct{}{}
	}
	cloned, _ := install.ListLocalMarketplaces(st.paths)
	for _, n := range cloned {
		out[n] = struct{}{}
	}
	return out
}

func truncateD(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func shortShaTUI(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "HEAD"
	}
	return sha
}
