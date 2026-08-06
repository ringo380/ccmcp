package cmd

import (
	"strings"
	"testing"
)

func TestCLIContextListsHeaviestPluginsFirst(t *testing.T) {
	// setupSandbox + runCLI are the existing helpers in cmd/cli_test.go:15.
	// runCLI resets cobra's package-global flag vars between Execute() calls.
	home := setupSandbox(t)
	out, err := runCLI(t, home, "context")
	if err != nil {
		t.Fatalf("context: %v\n%s", err, out)
	}
	if !strings.Contains(out, "per-turn context") {
		t.Fatalf("missing total line:\n%s", out)
	}
	if !strings.Contains(out, "≈") {
		t.Fatalf("figures must be estimates:\n%s", out)
	}
}

func TestCLIContextJSONHasProjectTotal(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "context", "--json")
	if err != nil {
		t.Fatalf("context --json: %v\n%s", err, out)
	}
	for _, key := range []string{`"project"`, `"byPlugin"`, `"loaded"`, `"deferred"`} {
		if !strings.Contains(out, key) {
			t.Fatalf("JSON missing %s:\n%s", key, out)
		}
	}
}
