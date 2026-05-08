package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCLIDiscoverListEmbedded runs `ccmcp discover list --json` in offline
// mode, where DefaultSources is restricted to the embedded registry. Verifies
// the JSON shape and that the embedded seed entries surface.
func TestCLIDiscoverListEmbedded(t *testing.T) {
	t.Setenv("CCMCP_DISCOVERY_OFFLINE", "1")

	home := setupSandbox(t)
	out, err := runCLI(t, home, "discover", "list", "--json")
	if err != nil {
		t.Fatalf("discover list failed: %v\n%s", err, out)
	}

	var res struct {
		Marketplaces []map[string]any `json:"marketplaces"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not valid JSON: %v\noutput:\n%s", err, out)
	}
	if len(res.Marketplaces) == 0 {
		t.Fatal("expected at least one embedded marketplace")
	}
	for _, mp := range res.Marketplaces {
		if origin, _ := mp["origin"].(string); !strings.HasPrefix(origin, "embedded") {
			t.Errorf("expected origin embedded, got %q", origin)
		}
	}
}

// TestCLIDiscoverListNonJSON checks the human-readable output prints
// marketplace names and source info without crashing.
func TestCLIDiscoverListNonJSON(t *testing.T) {
	t.Setenv("CCMCP_DISCOVERY_OFFLINE", "1")
	home := setupSandbox(t)
	out, err := runCLI(t, home, "discover", "list")
	if err != nil {
		t.Fatalf("discover list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "github") {
		t.Errorf("expected at least one github source line; got:\n%s", out)
	}
}

func TestCLIDiscoverShowMissing(t *testing.T) {
	t.Setenv("CCMCP_DISCOVERY_OFFLINE", "1")
	home := setupSandbox(t)
	_, err := runCLI(t, home, "discover", "show", "no-such-marketplace-12345")
	if err == nil {
		t.Fatal("expected error for unknown marketplace")
	}
	if !strings.Contains(err.Error(), "no discovered marketplace") {
		t.Errorf("expected helpful error, got: %v", err)
	}
}
