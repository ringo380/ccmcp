package commands

import (
	"path/filepath"
	"testing"
)

func TestIgnoresRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ignores.json")
	ig, err := LoadIgnores(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ig.Add("foo") {
		t.Error("Add should report new")
	}
	if ig.Add("foo") {
		t.Error("duplicate Add should be false")
	}
	if err := ig.Save(); err != nil {
		t.Fatal(err)
	}
	ig2, err := LoadIgnores(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ig2.Has("foo") {
		t.Error("foo should persist")
	}
	in := []Conflict{{Effective: "foo"}, {Effective: "bar"}}
	out := ig2.Filter(in)
	if len(out) != 1 || out[0].Effective != "bar" {
		t.Errorf("filter result wrong: %+v", out)
	}
}
