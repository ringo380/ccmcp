package config

import (
	"path/filepath"
	"testing"
)

func TestSettingsPluginToggle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	mustWriteJSON(t, path, map[string]any{
		"enabledPlugins": map[string]any{
			"foo@mkt": true,
			"bar@mkt": false,
		},
	})
	s, _ := LoadSettings(path)

	if en, known := s.PluginEnabled("foo@mkt"); !en || !known {
		t.Errorf("foo: want enabled=true known=true, got %v %v", en, known)
	}
	if en, known := s.PluginEnabled("bar@mkt"); en || !known {
		t.Errorf("bar: want enabled=false known=true, got %v %v", en, known)
	}
	if _, known := s.PluginEnabled("nope@mkt"); known {
		t.Error("nope: should report unknown")
	}

	s.SetPluginEnabled("bar@mkt", true)
	s.SetPluginEnabled("new@mkt", true)
	if en, _ := s.PluginEnabled("bar@mkt"); !en {
		t.Error("bar toggle failed")
	}
	if en, _ := s.PluginEnabled("new@mkt"); !en {
		t.Error("new entry add failed")
	}

	if !s.RemovePluginEntry("foo@mkt") {
		t.Error("removing foo should succeed")
	}
	if _, known := s.PluginEnabled("foo@mkt"); known {
		t.Error("foo should be unknown after remove")
	}
}

func TestSettingsMarketplaceCRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	mustWriteJSON(t, path, map[string]any{})
	s, _ := LoadSettings(path)

	// github
	if err := s.AddMarketplace(Marketplace{Name: "mkt1", SourceType: "github", Repo: "o/r"}); err != nil {
		t.Fatal(err)
	}
	// bad: github without repo
	if err := s.AddMarketplace(Marketplace{Name: "mkt2", SourceType: "github"}); err == nil {
		t.Error("should require --repo for github")
	}
	// local
	if err := s.AddMarketplace(Marketplace{Name: "mkt3", SourceType: "local", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	// bad: unknown type
	if err := s.AddMarketplace(Marketplace{Name: "mkt4", SourceType: "svn", Path: "/y"}); err == nil {
		t.Error("should reject unknown source")
	}

	extras := s.ExtraMarketplaces()
	if len(extras) != 2 {
		t.Fatalf("want 2 extras, got %d", len(extras))
	}

	if !s.RemoveMarketplace("mkt1") {
		t.Error("remove mkt1 should succeed")
	}
	if s.RemoveMarketplace("never-was") {
		t.Error("remove of missing should fail")
	}
}

func TestSettingsSkillOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	mustWriteJSON(t, path, map[string]any{})
	s, _ := LoadSettings(path)

	s.SetSkillOverride("my-skill", "off")
	if v, ok := s.SkillOverride("my-skill"); !ok || v != "off" {
		t.Errorf("want off, got (%q, %v)", v, ok)
	}
	if !s.RemoveSkillOverride("my-skill") {
		t.Error("remove should succeed")
	}
	if _, ok := s.SkillOverride("my-skill"); ok {
		t.Error("should be gone after remove")
	}
}
