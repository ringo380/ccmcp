package mcpprobe

import "testing"

func TestTargetFromConfigStdio(t *testing.T) {
	cfg := map[string]any{
		"command": "npx",
		"args":    []any{"-y", "some-server"},
		"env":     map[string]any{"TOKEN": "abc"},
	}
	got, ok, reason := TargetFromConfig("srv", cfg)
	if !ok {
		t.Fatalf("want probeable, got reason %q", reason)
	}
	if got.Command != "npx" || len(got.Args) != 2 || got.Args[1] != "some-server" {
		t.Fatalf("bad target: %+v", got)
	}
	if got.Env["TOKEN"] != "abc" {
		t.Fatalf("env not carried: %+v", got.Env)
	}
}

func TestTargetFromConfigRejectsRemote(t *testing.T) {
	for name, cfg := range map[string]any{
		"url-based":  map[string]any{"type": "sse", "url": "https://example.com/mcp"},
		"no-command": map[string]any{"args": []any{"x"}},
		"not-a-map":  "just a string",
	} {
		if _, ok, reason := TargetFromConfig(name, cfg); ok {
			t.Errorf("%s: want unprobeable", name)
		} else if reason == "" {
			t.Errorf("%s: unprobeable target must carry a reason", name)
		}
	}
}

// The cache is keyed on the config, so editing any part of it must invalidate.
func TestKeyChangesWithEveryConfigField(t *testing.T) {
	base := Target{Name: "s", Command: "a", Args: []string{"x"}, Env: map[string]string{"K": "1"}}
	variants := map[string]Target{
		"command": {Name: "s", Command: "b", Args: []string{"x"}, Env: map[string]string{"K": "1"}},
		"args":    {Name: "s", Command: "a", Args: []string{"y"}, Env: map[string]string{"K": "1"}},
		"env":     {Name: "s", Command: "a", Args: []string{"x"}, Env: map[string]string{"K": "2"}},
	}
	for field, v := range variants {
		if v.Key() == base.Key() {
			t.Errorf("changing %s must change the key", field)
		}
	}
	// Name is display-only and must NOT affect the key: the same server under a
	// different display name is the same probe.
	renamed := base
	renamed.Name = "other"
	if renamed.Key() != base.Key() {
		t.Error("display name must not affect the key")
	}
}

// Map iteration order is random; the key must not be.
func TestKeyIsStableAcrossRuns(t *testing.T) {
	tgt := Target{Command: "a", Env: map[string]string{"A": "1", "B": "2", "C": "3"}}
	first := tgt.Key()
	for i := 0; i < 50; i++ {
		if tgt.Key() != first {
			t.Fatal("key is not stable across calls - env map ordering leaked in")
		}
	}
}
