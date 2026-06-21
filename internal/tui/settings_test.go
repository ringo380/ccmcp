package tui

import (
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func toTweaksSettings(t *testing.T) *model {
	t.Helper()
	st, _ := buildState(t)
	m := newModel(st)
	m.width, m.height = 100, 40
	pressKey(m, "t") // lands on Settings
	return m
}

func TestSettingsToggleWritesConfigAndDirties(t *testing.T) {
	m := toTweaksSettings(t)
	sv := m.tweaks.settings
	// Move cursor to the offline-discovery toggle row.
	for i, r := range sv.rows {
		if r.key == config.KeyOfflineDiscovery {
			sv.cursor = i
			break
		}
	}
	before, _ := m.st.appcfg.OfflineDiscovery()
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	after, src := m.st.appcfg.OfflineDiscovery()
	if after == before {
		t.Fatal("toggle did not flip offlineDiscovery")
	}
	if src != config.SrcConfig {
		t.Fatalf("source after toggle = %q, want config", src)
	}
	if !m.st.dirtyAppConfig {
		t.Fatal("toggle did not mark appcfg dirty")
	}
}

func TestSettingsEnvRowIsReadOnly(t *testing.T) {
	t.Setenv("CCMCP_DISCOVERY_OFFLINE", "1")
	m := toTweaksSettings(t)
	sv := m.tweaks.settings
	for i, r := range sv.rows {
		if r.key == config.KeyOfflineDiscovery {
			sv.cursor = i
			break
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeySpace}) // should be a no-op edit
	if _, ok := m.st.appcfg.Raw[config.KeyOfflineDiscovery]; ok {
		t.Fatal("env-overridden row must not write to the config file")
	}
	out := sv.render()
	if !strings.Contains(out, "env") {
		t.Fatal("env-sourced row should show an [env] tag")
	}
}
