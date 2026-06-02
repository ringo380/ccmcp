package doctor

import "testing"

func TestResolvedModelPrecedence(t *testing.T) {
	orig := defaultModel
	t.Cleanup(func() { defaultModel = orig })

	// Default with nothing set is the compile-time baseline.
	defaultModel = DefaultAnthropicModel
	t.Setenv("CCMCP_CLAUDE_MODEL", "")
	if got := ResolvedModel(); got != DefaultAnthropicModel {
		t.Errorf("ResolvedModel()=%q, want baseline %q", got, DefaultAnthropicModel)
	}

	// SetDefaultModel installs the version-calibrated default.
	SetDefaultModel("claude-sonnet-4-6")
	if got := ResolvedModel(); got != "claude-sonnet-4-6" {
		t.Errorf("after SetDefaultModel, ResolvedModel()=%q, want claude-sonnet-4-6", got)
	}

	// Empty SetDefaultModel is ignored (doesn't blank out the model).
	SetDefaultModel("")
	if got := ResolvedModel(); got != "claude-sonnet-4-6" {
		t.Errorf("empty SetDefaultModel should be a no-op; got %q", got)
	}

	// Env var wins over the configured default.
	t.Setenv("CCMCP_CLAUDE_MODEL", "claude-opus-4-8")
	if got := ResolvedModel(); got != "claude-opus-4-8" {
		t.Errorf("env override should win; got %q", got)
	}
}
