package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseAndQualifyPluginID(t *testing.T) {
	if n, m := ParsePluginID("foo@bar"); n != "foo" || m != "bar" {
		t.Errorf("ParsePluginID: got (%q,%q)", n, m)
	}
	if n, m := ParsePluginID("foo"); n != "foo" || m != "" {
		t.Errorf("ParsePluginID bare: got (%q,%q)", n, m)
	}
	// last-@ wins (handles names containing @)
	if n, m := ParsePluginID("a@b@c"); n != "a@b" || m != "c" {
		t.Errorf("ParsePluginID last-@: got (%q,%q)", n, m)
	}
	if got := QualifyPluginID("foo", "mkt"); got != "foo@mkt" {
		t.Errorf("qualify: %s", got)
	}
	if got := QualifyPluginID("already@there", "mkt"); got != "already@there" {
		t.Errorf("qualify already: %s", got)
	}
}

func TestInstalledPluginsRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "installed.json")
	mustWriteJSON(t, path, map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			"foo@mkt": []any{
				map[string]any{"scope": "user", "installPath": "/cache/foo", "version": "1.2"},
			},
			"bar@mkt": []any{},
		},
	})
	ip, _ := LoadInstalledPlugins(path)
	if !ip.Has("foo@mkt") {
		t.Error("Has(foo): want true")
	}
	path1, ok := ip.Remove("foo@mkt")
	if !ok {
		t.Fatal("remove should succeed")
	}
	if path1 != "/cache/foo" {
		t.Errorf("installPath: got %q", path1)
	}
	// Second remove: still returns empty path (entry had no installPath), ok=true.
	path2, ok := ip.Remove("bar@mkt")
	if !ok {
		t.Fatal("remove bar should succeed")
	}
	if path2 != "" {
		t.Errorf("empty entry should return empty path, got %q", path2)
	}
	if _, ok := ip.Remove("never"); ok {
		t.Error("remove missing should return false")
	}
}

func TestResolvePluginID(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	mustWriteJSON(t, settingsPath, map[string]any{
		"enabledPlugins": map[string]any{
			"foo@alpha": true,
			"foo@beta":  false,
			"unique@gamma": true,
		},
	})
	installedPath := filepath.Join(dir, "installed.json")
	mustWriteJSON(t, installedPath, map[string]any{
		"version": float64(2),
		"plugins": map[string]any{
			"foo@alpha":    []any{},
			"unique@gamma": []any{},
			"local-only@delta": []any{},
		},
	})
	s, _ := LoadSettings(settingsPath)
	ip, _ := LoadInstalledPlugins(installedPath)

	// unambiguous
	id, amb := ResolvePluginID("unique", s, ip)
	if id != "unique@gamma" || len(amb) != 0 {
		t.Errorf("unique: got (%q, %v)", id, amb)
	}
	// installed-only still resolves
	id, amb = ResolvePluginID("local-only", s, ip)
	if id != "local-only@delta" || len(amb) != 0 {
		t.Errorf("local-only: got (%q, %v)", id, amb)
	}
	// ambiguous returns empty id + all candidates
	id, amb = ResolvePluginID("foo", s, ip)
	if id != "" {
		t.Errorf("ambiguous should return empty id, got %q", id)
	}
	wantAmb := []string{"foo@alpha", "foo@beta"}
	if !reflect.DeepEqual(amb, wantAmb) {
		t.Errorf("ambiguities: got %v want %v", amb, wantAmb)
	}
	// already qualified
	id, _ = ResolvePluginID("x@y", s, ip)
	if id != "x@y" {
		t.Errorf("qualified passthrough: %s", id)
	}
}
