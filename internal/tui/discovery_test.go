package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/discovery"
	"github.com/ringo380/ccmcp/internal/install"
)

var errInstallFixture = errors.New("simulated install failure")

// stripANSI is defined in doctor_test.go (same package).

func TestDiscoveryViewFromCacheRendersList(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)

	// Inject pre-fetched results so we don't touch the network.
	res := &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "demo", Source: "github", Repo: "owner/demo", Description: "a demo", Origin: "embedded"},
		},
	}
	v.update(discoveryFetchedMsg{res: res})

	out := stripANSI(v.render())
	if !strings.Contains(out, "demo") {
		t.Fatalf("list should mention demo; got:\n%s", out)
	}
	if !strings.Contains(out, "owner/demo") {
		t.Fatalf("list should show source; got:\n%s", out)
	}
}

func TestOriginBadge(t *testing.T) {
	cases := map[string]string{
		"embedded":                            "[emb]",
		"anthropic":                           "[ant]",
		"awesome-list:hesreallyhim/awesome-x": "[awe]",
		"user:https://example.com/reg.json":   "[usr]",
		"":                                    "[   ]",
		"something-unknown":                   "[   ]",
	}
	for origin, want := range cases {
		if got := stripANSI(originBadge(origin)); got != want {
			t.Errorf("originBadge(%q)=%q, want %q", origin, got, want)
		}
	}
}

func TestDiscoveryListShowsOriginBadge(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)

	res := &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "curated", Source: "github", Repo: "owner/curated", Origin: "embedded"},
			{Name: "scraped", Source: "github", Repo: "owner/scraped", Origin: "awesome-list:owner/list"},
		},
	}
	v.update(discoveryFetchedMsg{res: res})

	out := stripANSI(v.render())
	if !strings.Contains(out, "[emb]") {
		t.Errorf("list should show [emb] badge; got:\n%s", out)
	}
	if !strings.Contains(out, "[awe]") {
		t.Errorf("list should show [awe] badge; got:\n%s", out)
	}
}

func TestDiscoveryViewBackFromDetail(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.mode = modeDetail

	// `b` should pop back to plugin list.
	v.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if v.mode != modePlugins {
		t.Fatalf("after b: mode=%d, want modePlugins(%d)", v.mode, modePlugins)
	}

	v.update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.mode != modeList {
		t.Fatalf("after esc: mode=%d, want modeList(%d)", v.mode, modeList)
	}
}

func TestDiscoveryViewSortsByStars(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "low", Source: "github", Repo: "o/low", Stars: 10},
			{Name: "high", Source: "github", Repo: "o/high", Stars: 9000},
			{Name: "mid", Source: "github", Repo: "o/mid", Stars: 500},
		},
	}})
	if v.rows[0].Name != "high" || v.rows[1].Name != "mid" || v.rows[2].Name != "low" {
		t.Fatalf("rows not sorted by stars desc: %+v", v.rows)
	}
}

func TestDiscoveryViewFilter(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "alpha", Source: "github", Repo: "o/alpha", Tags: []string{"agents"}},
			{Name: "beta", Source: "github", Repo: "o/beta", Tags: []string{"security"}},
		},
	}})

	// `/` enters filter mode and captures input.
	v.update(key("/"))
	if !v.filterActive || !v.capturingInput() {
		t.Fatalf("expected filterActive + capturingInput after /")
	}
	// Type a query that matches a tag, not a name.
	v.filter.SetValue("security")
	if vis := v.visibleRows(); len(vis) != 1 || vis[0].Name != "beta" {
		t.Fatalf("filter by tag should yield only beta, got %+v", vis)
	}
	// esc exits filter mode.
	v.update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.filterActive || v.capturingInput() {
		t.Fatalf("expected filter mode off after esc")
	}
}

func TestDiscoveryViewAddAlreadyInstalled(t *testing.T) {
	st, _ := buildState(t)
	// Seed an extra known marketplace so the row is flagged installed.
	if err := st.settings.AddMarketplace(config.Marketplace{Name: "already-here", SourceType: "github", Repo: "o/already-here"}); err != nil {
		t.Fatal(err)
	}
	v := newDiscoveryView(st)
	v.resize(120, 30)
	// Installed marketplaces are hidden by default; reveal them so the row is
	// selectable and the "already added" guard is exercised.
	v.showInstalled = true
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "already-here", Source: "github", Repo: "o/already-here"},
		},
	}})
	cmd := v.update(key("a"))
	if cmd != nil {
		t.Fatalf("add on installed row should be a no-op cmd")
	}
	if !strings.Contains(stripANSI(v.flash), "already added") {
		t.Fatalf("expected 'already added' flash, got %q", stripANSI(v.flash))
	}
}

func TestDiscoveryInstallResultHandlerEnables(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.installBusy = true
	res := install.Result{QualifiedID: "newp@newmkt", InstallPath: "/x/new", Version: "1.0"}
	v.update(discoveryInstallResultMsg{name: "newmkt", result: &res})
	if v.installBusy {
		t.Fatalf("installBusy should clear on result")
	}
	if !st.dirtyPlugins || !st.dirtySettings {
		t.Fatalf("expected dirty flags set; plugins=%v settings=%v", st.dirtyPlugins, st.dirtySettings)
	}
	if en, _ := st.settings.PluginEnabled("newp@newmkt"); !en {
		t.Fatalf("installed plugin should be enabled")
	}
	if !strings.Contains(stripANSI(v.flash), "installed newp@newmkt") {
		t.Fatalf("expected install flash, got %q", stripANSI(v.flash))
	}
}

func TestDiscoveryInstallReinstallNeedsConfirm(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.mode = modeDetail
	v.detailReady = true
	v.report = discovery.ConflictReport{PluginIDClash: true}
	v.curMP = discovery.RemoteMarketplace{Name: "mkt", Source: "github", Repo: "o/mkt"}
	v.curPlugin = discovery.RemotePlugin{Name: "dup"}

	// First `i` only arms the confirm - no install kicks off.
	if cmd := v.update(key("i")); cmd != nil {
		t.Fatalf("first i on a clashing plugin should not start install")
	}
	if v.installBusy {
		t.Fatalf("install should not be busy after first i")
	}
	if v.pendingInstall != "dup" {
		t.Fatalf("expected pendingInstall=dup, got %q", v.pendingInstall)
	}
	if !strings.Contains(stripANSI(v.flash), "press i again") {
		t.Fatalf("expected reinstall-confirm flash, got %q", stripANSI(v.flash))
	}
}

func TestDiscoveryViewFilterThenEnterDoesNotPanic(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "alpha", Source: "github", Repo: "o/alpha"},
			{Name: "beta", Source: "github", Repo: "o/beta"},
			{Name: "gamma", Source: "github", Repo: "o/gamma"},
		},
	}})
	// Cursor on the last row, then narrow the filter to a single match so
	// v.index (2) is now past the visible bound (1). Pressing enter must clamp
	// instead of indexing visible[2] out of range.
	v.index = 2
	v.filter.SetValue("alpha")
	cmd := v.update(key("enter")) // would panic pre-fix
	if v.mode != modePlugins {
		t.Fatalf("enter should drill into the single visible row; mode=%d", v.mode)
	}
	if v.curMP.Name != "alpha" {
		t.Fatalf("expected to drill into alpha, got %q", v.curMP.Name)
	}
	_ = cmd
}

func TestDiscoveryInstallFailureRollsBackAddedMarketplace(t *testing.T) {
	st, _ := buildState(t)
	// Seed the marketplace as if install.AddMarketplace had already added it.
	if err := st.settings.AddMarketplace(config.Marketplace{Name: "tmpmkt", SourceType: "github", Repo: "o/tmpmkt"}); err != nil {
		t.Fatal(err)
	}
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.installBusy = true
	v.update(discoveryInstallResultMsg{
		name:             "tmpmkt",
		addedMarketplace: true,
		err:              errInstallFixture,
	})
	for _, mp := range st.settings.ExtraMarketplaces() {
		if mp.Name == "tmpmkt" {
			t.Fatalf("failed install should have rolled back the added marketplace")
		}
	}
	if _, ok := v.installedNames["tmpmkt"]; ok {
		t.Fatalf("installedNames snapshot should no longer contain rolled-back marketplace")
	}
}

func TestDiscoveryViewSourceErrorsRender(t *testing.T) {
	st, _ := buildState(t)
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{{Name: "ok", Source: "github", Repo: "o/r"}},
		Errors:       map[string]string{"anthropic": "timeout"},
	}})
	out := stripANSI(v.render())
	if !strings.Contains(out, "anthropic: timeout") {
		t.Fatalf("expected source error rendered, got:\n%s", out)
	}
}

// TestDiscoveryHidesInstalledByDefault: an already-installed marketplace is
// filtered out of the default list; a not-yet-installed one is shown.
func TestDiscoveryHidesInstalledByDefault(t *testing.T) {
	st, _ := buildState(t)
	if err := st.settings.AddMarketplace(config.Marketplace{Name: "installed-mp", SourceType: "github", Repo: "o/installed-mp"}); err != nil {
		t.Fatal(err)
	}
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "installed-mp", Source: "github", Repo: "o/installed-mp"},
			{Name: "fresh-mp", Source: "github", Repo: "o/fresh-mp"},
		},
	}})

	vis := v.visibleRows()
	if len(vis) != 1 || vis[0].Name != "fresh-mp" {
		t.Fatalf("default list should show only fresh-mp, got %+v", vis)
	}
	out := stripANSI(v.render())
	if strings.Contains(out, "installed-mp") {
		t.Fatalf("installed marketplace should be hidden by default; got:\n%s", out)
	}
	if !strings.Contains(out, "fresh-mp") {
		t.Fatalf("new marketplace should be visible; got:\n%s", out)
	}
	if !strings.Contains(out, "1 installed hidden") {
		t.Fatalf("expected installed-hidden hint; got:\n%s", out)
	}
}

// TestDiscoveryToggleShowInstalled: `H` reveals installed marketplaces, marked [=].
func TestDiscoveryToggleShowInstalled(t *testing.T) {
	st, _ := buildState(t)
	if err := st.settings.AddMarketplace(config.Marketplace{Name: "installed-mp", SourceType: "github", Repo: "o/installed-mp"}); err != nil {
		t.Fatal(err)
	}
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "installed-mp", Source: "github", Repo: "o/installed-mp"},
			{Name: "fresh-mp", Source: "github", Repo: "o/fresh-mp"},
		},
	}})

	v.update(key("H"))
	if !v.showInstalled {
		t.Fatalf("H should toggle showInstalled on")
	}
	if len(v.visibleRows()) != 2 {
		t.Fatalf("after H both rows should be visible, got %d", len(v.visibleRows()))
	}
	out := stripANSI(v.render())
	if !strings.Contains(out, "installed-mp") || !strings.Contains(out, "[=]") {
		t.Fatalf("installed marketplace should show with [=] marker; got:\n%s", out)
	}

	// Toggling again hides it.
	v.update(key("H"))
	if v.showInstalled {
		t.Fatalf("second H should toggle showInstalled off")
	}
	if len(v.visibleRows()) != 1 {
		t.Fatalf("after second H only fresh-mp should be visible, got %d", len(v.visibleRows()))
	}
}

// TestDiscoveryAllInstalledEmptyState: when every discovered marketplace is
// installed, the empty state points to the Marketplaces tab.
func TestDiscoveryAllInstalledEmptyState(t *testing.T) {
	st, _ := buildState(t)
	if err := st.settings.AddMarketplace(config.Marketplace{Name: "only-mp", SourceType: "github", Repo: "o/only-mp"}); err != nil {
		t.Fatal(err)
	}
	v := newDiscoveryView(st)
	v.resize(120, 30)
	v.update(discoveryFetchedMsg{res: &discovery.DiscoveryResult{
		Marketplaces: []discovery.RemoteMarketplace{
			{Name: "only-mp", Source: "github", Repo: "o/only-mp"},
		},
	}})

	if len(v.visibleRows()) != 0 {
		t.Fatalf("expected empty default list, got %+v", v.visibleRows())
	}
	out := stripANSI(v.render())
	if !strings.Contains(out, "Marketplaces tab") {
		t.Fatalf("empty state should point to Marketplaces tab; got:\n%s", out)
	}
}
