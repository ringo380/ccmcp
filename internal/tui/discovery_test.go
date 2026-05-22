package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/discovery"
	"github.com/ringo380/ccmcp/internal/install"
)

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

	// First `i` only arms the confirm — no install kicks off.
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
