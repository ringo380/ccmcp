package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppConfigMissingFileIsEmpty(t *testing.T) {
	c := LoadAppConfig(filepath.Join(t.TempDir(), "nope.json"))
	if c == nil {
		t.Fatal("LoadAppConfig returned nil")
	}
	if v, src := c.DefaultScope(); v != "user" || src != SrcDefault {
		t.Fatalf("missing file: got (%q,%q), want (user,default)", v, src)
	}
}

func TestAppConfigRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	c := LoadAppConfig(p)
	c.SetBool(KeyOfflineDiscovery, true)
	c.SetString(KeyDefaultScope, "project")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2 := LoadAppConfig(p)
	if v, src := c2.OfflineDiscovery(); !v || src != SrcConfig {
		t.Fatalf("offline after reload: got (%v,%q), want (true,config)", v, src)
	}
	if v, _ := c2.DefaultScope(); v != "project" {
		t.Fatalf("scope after reload: %q", v)
	}
}

func TestAppConfigCorruptFileFallsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := LoadAppConfig(p)
	if v, src := c.ConfirmBeforeApply(); !v || src != SrcDefault {
		t.Fatalf("corrupt file: got (%v,%q), want (true,default)", v, src)
	}
}

func TestAppConfigEnvOverridesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	c := LoadAppConfig(p)
	c.SetBool(KeyOfflineDiscovery, false) // file says online
	t.Setenv("CCMCP_DISCOVERY_OFFLINE", "1")
	if v, src := c.OfflineDiscovery(); !v || src != SrcEnv {
		t.Fatalf("env override: got (%v,%q), want (true,env)", v, src)
	}
}
