package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStashRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stash.json")
	mustWriteJSON(t, path, map[string]any{})
	s, _ := LoadStash(path)

	if len(s.Names()) != 0 {
		t.Fatal("stash starts empty")
	}
	s.Put("a", map[string]any{"command": "foo"})
	s.Put("b", map[string]any{"command": "bar"})
	if got := s.Names(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("names: %v", got)
	}
	if v, ok := s.Get("a"); !ok || v.(map[string]any)["command"] != "foo" {
		t.Errorf("get a: %v %v", v, ok)
	}
	if !s.Delete("a") {
		t.Error("delete a should succeed")
	}
	if s.Delete("a") {
		t.Error("second delete should fail")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// Reload
	s2, _ := LoadStash(path)
	if got := s2.Names(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("after reload: %v", got)
	}
}

func TestProfilesRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	mustWriteJSON(t, path, map[string]any{})
	p, _ := LoadProfiles(path)

	p.Set("dev", []string{"mcp1", "mcp2"})
	p.Set("prod", []string{"mcp3"})
	if got := p.Names(); !reflect.DeepEqual(got, []string{"dev", "prod"}) {
		t.Errorf("names: %v", got)
	}
	mcps, ok := p.MCPs("dev")
	if !ok {
		t.Fatal("dev should exist")
	}
	if !reflect.DeepEqual(mcps, []string{"mcp1", "mcp2"}) {
		t.Errorf("dev mcps: %v", mcps)
	}
	if !p.Delete("dev") {
		t.Error("delete dev should succeed")
	}
	if _, ok := p.MCPs("dev"); ok {
		t.Error("dev should be gone")
	}
}
