package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/discovery"
)

// stripANSI removes ANSI CSI sequences so tests can assert on plain text.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

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
