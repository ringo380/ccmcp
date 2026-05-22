package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/config"
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

	// list filter (modeList)
	filter       textinput.Model
	filterActive bool

	// plugin-list state (modePlugins)
	curMP    discovery.RemoteMarketplace
	plugins  []discovery.RemotePlugin
	pIndex   int
	pTop     int
	pErr     error
	pLoading bool

	// plugin filter (modePlugins)
	pFilter       textinput.Model
	pFilterActive bool

	// detail state (modeDetail)
	curPlugin   discovery.RemotePlugin
	preview     *discovery.PreviewResult
	report      discovery.ConflictReport
	detailErr   error
	detailReady bool
	detailBusy  bool

	// install/add state (mutation guards + reinstall confirm)
	addBusy        bool
	installBusy    bool
	pendingInstall string // plugin name awaiting a confirm second-press (PluginIDClash)

	// installedNames is a snapshot of already-added marketplace names, refreshed
	// only on the bubbletea goroutine (construction, fetch, post-mutation). render
	// and key handlers read this instead of calling settings.ExtraMarketplaces()
	// directly — the add/install closures mutate that map off-goroutine, and a
	// live read during the spinner-driven render would be a concurrent map
	// read/write panic.
	installedNames map[string]struct{}

	// last fetch metadata
	fetchedAt time.Time
	fromCache bool
	srcErrors map[string]string
	fetchBusy bool
}

type discoveryMode int

const (
	modeList discoveryMode = iota
	modePlugins
	modeDetail
)

// async messages
type discoveryFetchedMsg struct {
	res *discovery.DiscoveryResult
	err error
}
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

// discoveryAddResultMsg is the result of adding a discovered marketplace to
// settings + cloning it (the `a` key in modeList).
type discoveryAddResultMsg struct {
	name string
	err  error
}

// discoveryInstallResultMsg is the result of installing a plugin straight from
// the Discover tab (the `i` key). addedMarketplace records whether the
// marketplace had to be added+cloned as part of the install, so the handler
// knows to persist settings synchronously.
type discoveryInstallResultMsg struct {
	name             string
	addedMarketplace bool
	result           *install.Result
	err              error
}

func newDiscoveryView(st *state) *discoveryView {
	mk := func() textinput.Model {
		ti := textinput.New()
		ti.Prompt = "/"
		ti.CharLimit = 64
		return ti
	}
	v := &discoveryView{st: st, filter: mk(), pFilter: mk()}
	v.refreshInstalled()
	return v
}

// refreshInstalled snapshots the set of already-added marketplace names. MUST be
// called only on the bubbletea goroutine (it reads the settings map + scans the
// clone dir), never from a context where an add/install closure could be
// mutating settings concurrently.
func (v *discoveryView) refreshInstalled() {
	v.installedNames = installedMarketplaceNames(v.st)
}

func (v *discoveryView) resize(w, h int) { v.w, v.h = w, h }

// capturingInput reports whether a filter textinput has focus, so the global
// key handler in model.go stops stealing keystrokes (digits, q, etc.).
func (v *discoveryView) capturingInput() bool { return v.filterActive || v.pFilterActive }

func (v *discoveryView) helpText() string {
	switch v.mode {
	case modeList:
		if v.filterActive {
			return "type to filter  enter/esc: done"
		}
		return "enter: drill in  a: add  r: refresh  /: filter  j/k: nav  g/G: top/bottom"
	case modePlugins:
		if v.pFilterActive {
			return "type to filter  enter/esc: done"
		}
		return "enter: preview  i: install  /: filter  b/esc: back  j/k: nav"
	case modeDetail:
		return "i: install  b/esc: back"
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
		v.sortRows()
		v.refreshInstalled()
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
	case discoveryAddResultMsg:
		v.addBusy = false
		if m.err != nil {
			// Roll back the in-memory settings entry left behind when the clone
			// failed mid-AddMarketplace, so settings doesn't diverge from disk.
			v.st.settings.RemoveMarketplace(m.name)
			v.flash = styleErr.Render("add error: " + m.err.Error())
			return nil
		}
		if err := v.persistSettings(); err != nil {
			v.flash = styleErr.Render("save error: " + err.Error())
			return nil
		}
		v.refreshInstalled()
		v.flash = styleOK.Render("added marketplace " + m.name)
		return nil
	case discoveryInstallResultMsg:
		v.installBusy = false
		if m.err != nil {
			if m.addedMarketplace {
				// install.AddMarketplace had already cloned the marketplace to disk
				// before install.Install failed. Roll back BOTH the settings entry
				// and the clone dir so we don't strand an orphaned clone with no
				// settings reference. RemoveMarketplace is safe here: the failed
				// install registered no plugin against this marketplace.
				if rerr := install.RemoveMarketplace(v.st.paths, v.st.settings, v.st.installed, m.name, true); rerr != nil {
					v.st.settings.RemoveMarketplace(m.name)
				}
				v.refreshInstalled()
			}
			v.flash = styleErr.Render("install error: " + m.err.Error())
			return nil
		}
		install.RegisterInstall(v.st.settings, v.st.installed, m.result)
		v.st.dirtySettings = true
		v.st.dirtyPlugins = true
		v.st.rescanPluginMCPs()
		v.refreshInstalled()
		// The marketplace clone already touched disk; persist settings now so a
		// force-quit can't leave settings.json behind the on-disk state.
		if m.addedMarketplace {
			if err := v.persistSettings(); err != nil {
				v.flash = styleErr.Render("save error: " + err.Error())
				return nil
			}
		}
		v.flash = styleOK.Render("installed " + m.result.QualifiedID)
		return nil
	}

	// Filter input drainage — must come after the async-msg switch (so result
	// messages aren't swallowed) but before key dispatch.
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
	if v.pFilterActive {
		var cmd tea.Cmd
		v.pFilter, cmd = v.pFilter.Update(msg)
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "enter", "esc":
				v.pFilterActive = false
				v.pFilter.Blur()
			}
		}
		return cmd
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
	visible := v.visibleRows()
	// Clamp here as well as in render(): a filter change can leave v.index past
	// the visible bound, and the key handlers below index visible[v.index]
	// directly. Relying on render() alone leaves a window where update() panics.
	v.index = clampIndex(v.index, len(visible))
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
		v.top = 0
	case "G":
		v.index = len(visible) - 1
	case "/":
		v.filterActive = true
		v.filter.Focus()
		return textinput.Blink
	case "c":
		v.filter.SetValue("")
		v.index = 0
		v.top = 0
	case "r":
		if v.fetchBusy {
			return nil
		}
		v.fetchBusy = true
		v.flash = styleProgress.Render("refreshing discovery sources…")
		return v.fetchCmd(true)
	case "a":
		// Block while any mutation closure is in flight — both add and install
		// call install.AddMarketplace off-goroutine, and two concurrent writers to
		// the shared settings map would panic.
		if v.addBusy || v.installBusy || len(visible) == 0 {
			return nil
		}
		r := visible[v.index]
		if _, ok := v.installedNames[r.Name]; ok {
			v.flash = styleDim.Render(r.Name + " already added")
			return nil
		}
		mp, err := toConfigMarketplace(r)
		if err != nil {
			v.flash = styleErr.Render(err.Error())
			return nil
		}
		v.addBusy = true
		v.flash = styleProgress.Render("adding " + r.Name + "…")
		p := v.st.paths
		settings := v.st.settings
		name := r.Name
		return func() tea.Msg {
			err := install.AddMarketplace(p, settings, mp)
			return discoveryAddResultMsg{name: name, err: err}
		}
	case "enter":
		if len(visible) == 0 {
			return nil
		}
		v.curMP = visible[v.index]
		v.plugins = nil
		v.pErr = nil
		v.pIndex = 0
		v.pFilter.SetValue("")
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
	visible := v.visiblePlugins()
	v.pIndex = clampIndex(v.pIndex, len(visible))
	switch key.String() {
	case "esc", "b":
		v.mode = modeList
	case "up", "k":
		if v.pIndex > 0 {
			v.pIndex--
		}
	case "down", "j":
		if v.pIndex < len(visible)-1 {
			v.pIndex++
		}
	case "/":
		v.pFilterActive = true
		v.pFilter.Focus()
		return textinput.Blink
	case "c":
		v.pFilter.SetValue("")
		v.pIndex = 0
		v.pTop = 0
	case "i":
		if len(visible) == 0 {
			return nil
		}
		return v.installPluginCmd(visible[v.pIndex])
	case "enter":
		if len(visible) == 0 || v.detailBusy {
			return nil
		}
		plugin := visible[v.pIndex]
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
		v.pendingInstall = ""
	case "i":
		if v.detailBusy || !v.detailReady {
			return nil
		}
		return v.installPluginCmd(v.curPlugin)
	default:
		// Any other key dismisses a pending reinstall-confirm.
		if v.pendingInstall != "" {
			v.pendingInstall = ""
		}
	}
	return nil
}

// installPluginCmd builds the async install command for a plugin, gating a
// reinstall behind a double-press confirm when the plugin ID already collides
// with an installed one.
func (v *discoveryView) installPluginCmd(plugin discovery.RemotePlugin) tea.Cmd {
	// Block while any mutation closure is in flight (see the `a` handler) — a
	// concurrent add and install would both write the shared settings map.
	if v.installBusy || v.addBusy {
		return nil
	}
	// Reinstall confirm: only meaningful in modeDetail where the conflict report
	// is computed. PluginIDClash means "<plugin>@<marketplace>" is already installed.
	if v.mode == modeDetail && v.report.PluginIDClash {
		if v.pendingInstall != plugin.Name {
			v.pendingInstall = plugin.Name
			v.flash = styleWarn.Render("already installed — press i again to reinstall")
			return nil
		}
	}
	v.pendingInstall = ""
	mp := v.curMP
	cfgMP, err := toConfigMarketplace(mp)
	if err != nil {
		v.flash = styleErr.Render(err.Error())
		return nil
	}
	v.installBusy = true
	v.flash = styleProgress.Render("installing " + plugin.Name + "…")
	p := v.st.paths
	settings := v.st.settings
	pluginName := plugin.Name
	mktName := mp.Name
	alreadyInSettings := false
	for _, e := range settings.ExtraMarketplaces() {
		if e.Name == mktName {
			alreadyInSettings = true
			break
		}
	}
	return func() tea.Msg {
		addedMarketplace := false
		if !install.IsMarketplaceCloned(p, mktName) {
			// Ensure the marketplace is on disk before install.Install reads its
			// manifest. If it's in settings already (but not cloned) just clone;
			// otherwise add+clone.
			var aerr error
			if alreadyInSettings {
				aerr = install.CloneMarketplace(p, cfgMP)
			} else {
				aerr = install.AddMarketplace(p, settings, cfgMP)
				addedMarketplace = aerr == nil
			}
			if aerr != nil {
				return discoveryInstallResultMsg{name: mktName, addedMarketplace: addedMarketplace, err: aerr}
			}
		}
		res, err := install.Install(p, mktName, pluginName)
		return discoveryInstallResultMsg{name: mktName, addedMarketplace: addedMarketplace, result: res, err: err}
	}
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

// toConfigMarketplace converts a discovered RemoteMarketplace into the
// config.Marketplace shape the install pipeline consumes. config.Marketplace
// has no "url" source type (only github|git|local) and no branch field — a
// "url" source maps to "git" (CloneMarketplace passes the URL verbatim to
// git clone), and any pinned discovery Branch is dropped (clones HEAD).
func toConfigMarketplace(r discovery.RemoteMarketplace) (config.Marketplace, error) {
	srcType, ok := mapSource(r.Source)
	if !ok {
		return config.Marketplace{}, fmt.Errorf("unsupported source type %q for %s", r.Source, r.Name)
	}
	return config.Marketplace{
		Name:       r.Name,
		SourceType: srcType,
		Repo:       r.Repo,
		AutoUpdate: true,
	}, nil
}

func mapSource(src string) (string, bool) {
	switch src {
	case "github":
		return "github", true
	case "git":
		return "git", true
	case "url":
		return "git", true
	default:
		return "", false
	}
}

// persistSettings flushes settings.json synchronously after a disk side-effect
// (a marketplace clone) so on-disk state and settings.json don't diverge if the
// user later force-quits. Mirrors marketplaceView.persistSettings.
func (v *discoveryView) persistSettings() error {
	if err := config.Backup(v.st.settings.Path, v.st.paths.BackupsDir); err != nil {
		return err
	}
	return v.st.settings.Save()
}

// sortRows orders discovered marketplaces by star count (desc) then name (asc),
// so the most-recognised marketplaces float to the top.
func (v *discoveryView) sortRows() {
	sort.SliceStable(v.rows, func(i, j int) bool {
		if v.rows[i].Stars != v.rows[j].Stars {
			return v.rows[i].Stars > v.rows[j].Stars
		}
		return strings.ToLower(v.rows[i].Name) < strings.ToLower(v.rows[j].Name)
	})
}

// visibleRows applies the modeList filter (matches name, description, or tags).
func (v *discoveryView) visibleRows() []discovery.RemoteMarketplace {
	q := strings.ToLower(strings.TrimSpace(v.filter.Value()))
	if q == "" {
		return v.rows
	}
	out := make([]discovery.RemoteMarketplace, 0, len(v.rows))
	for _, r := range v.rows {
		if matchesQuery(q, r.Name, r.Description, r.Tags) {
			out = append(out, r)
		}
	}
	return out
}

// visiblePlugins applies the modePlugins filter (matches name, description, tags).
func (v *discoveryView) visiblePlugins() []discovery.RemotePlugin {
	q := strings.ToLower(strings.TrimSpace(v.pFilter.Value()))
	if q == "" {
		return v.plugins
	}
	out := make([]discovery.RemotePlugin, 0, len(v.plugins))
	for _, p := range v.plugins {
		if matchesQuery(q, p.Name, p.Description, p.Tags) {
			out = append(out, p)
		}
	}
	return out
}

func matchesQuery(q, name, desc string, tags []string) bool {
	if strings.Contains(strings.ToLower(name), q) || strings.Contains(strings.ToLower(desc), q) {
		return true
	}
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
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
	visible := v.visibleRows()
	b.WriteString(fmt.Sprintf("Discover — %d marketplace(s)", len(v.rows)))
	if v.filter.Value() != "" {
		b.WriteString(styleDim.Render(fmt.Sprintf("  (%d shown)", len(visible))))
	}
	if v.fromCache {
		b.WriteString("  " + styleDim.Render(fmt.Sprintf("(cached %s ago)", time.Since(v.fetchedAt).Round(time.Second))))
	}
	b.WriteString("\n")
	if v.filterActive || v.filter.Value() != "" {
		b.WriteString(v.filter.View() + "\n")
	}
	if v.fetchBusy {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("fetching…") + "\n")
	}
	if v.addBusy || v.installBusy {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("working…") + "\n")
	}
	if len(visible) == 0 && !v.fetchBusy {
		if v.filter.Value() != "" {
			b.WriteString(styleDim.Render("  (no matches — press c to clear filter)") + "\n")
		} else {
			b.WriteString(styleDim.Render("  (no marketplaces — press r to retry)") + "\n")
		}
	}

	listH := v.h - 6
	if listH < 5 {
		listH = 5
	}
	if v.index >= len(visible) {
		v.index = len(visible) - 1
	}
	if v.index < 0 {
		v.index = 0
	}
	if v.index < v.top {
		v.top = v.index
	}
	if v.index >= v.top+listH {
		v.top = v.index - listH + 1
	}
	end := v.top + listH
	if end > len(visible) {
		end = len(visible)
	}

	for i := v.top; i < end; i++ {
		r := visible[i]
		marker := styleDim.Render("[+]")
		if _, ok := v.installedNames[r.Name]; ok {
			marker = styleWarn.Render("[=]")
		}
		src := r.Source
		if r.Repo != "" {
			src = r.Source + " " + r.Repo
		}
		line := fmt.Sprintf("%s %s  %s", marker, r.Name, styleDim.Render("("+src+")"))
		if r.Stars > 0 {
			line += "  " + styleDim.Render(fmt.Sprintf("★ %s", formatStars(r.Stars)))
		}
		if i == v.index {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
		// Secondary line: description + tags, indented under the row.
		var meta []string
		if r.Description != "" {
			meta = append(meta, truncateD(r.Description, 70))
		}
		if len(r.Tags) > 0 {
			meta = append(meta, "["+strings.Join(r.Tags, ", ")+"]")
		}
		if len(meta) > 0 {
			b.WriteString("      " + styleDim.Render(strings.Join(meta, "  ")) + "\n")
		}
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
	visible := v.visiblePlugins()
	b.WriteString(fmt.Sprintf("Discover › %s — %d plugin(s)", v.curMP.Name, len(v.plugins)))
	if v.pFilter.Value() != "" {
		b.WriteString(styleDim.Render(fmt.Sprintf("  (%d shown)", len(visible))))
	}
	b.WriteString("\n")
	if v.pFilterActive || v.pFilter.Value() != "" {
		b.WriteString(v.pFilter.View() + "\n")
	}
	if v.pLoading {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("fetching manifest…") + "\n")
	}
	if v.installBusy {
		b.WriteString("  " + v.st.spinnerFrame + styleProgress.Render("installing…") + "\n")
	}
	if v.pErr != nil {
		b.WriteString(styleErr.Render("  error: "+v.pErr.Error()) + "\n")
		return b.String()
	}
	listH := v.h - 5
	if listH < 5 {
		listH = 5
	}
	if v.pIndex >= len(visible) {
		v.pIndex = len(visible) - 1
	}
	if v.pIndex < 0 {
		v.pIndex = 0
	}
	if v.pIndex < v.pTop {
		v.pTop = v.pIndex
	}
	if v.pIndex >= v.pTop+listH {
		v.pTop = v.pIndex - listH + 1
	}
	end := v.pTop + listH
	if end > len(visible) {
		end = len(visible)
	}
	for i := v.pTop; i < end; i++ {
		p := visible[i]
		line := p.Name
		if p.Description != "" {
			line += "  " + styleDim.Render(truncateD(p.Description, 60))
		}
		if i == v.pIndex {
			b.WriteString(styleSelected.Render("  " + line))
		} else {
			b.WriteString("  " + line)
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
	if v.curPlugin.Description != "" {
		b.WriteString("  " + styleDim.Render(truncateD(v.curPlugin.Description, 76)) + "\n")
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

// clampIndex keeps a cursor index within [0, n-1], or 0 when the list is empty.
func clampIndex(i, n int) int {
	if i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}

// formatStars renders a GitHub star count compactly (1234 → "1.2k").
func formatStars(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
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
