package tui

import "testing"

func TestCostIndexIsCachedAndInvalidated(t *testing.T) {
	st, _ := buildState(t) // existing helper, internal/tui/tui_test.go:20

	first := st.costIndex()
	if first == nil {
		t.Fatal("costIndex returned nil")
	}
	if second := st.costIndex(); second != first {
		t.Fatal("costIndex must return the cached pointer on the second call")
	}

	st.invalidateCost()
	if third := st.costIndex(); third == first {
		t.Fatal("costIndex must rebuild after invalidateCost")
	}
}

// TestCostIndexStaleOnSettingsMutation covers the gap the explicit
// invalidateCost() call site does not: a skill/agent override mutation changes
// ctxcost's Enabled filter (internal/ctxcost/assets.go) without ever touching
// pluginMCPs. Any of the 14 dirtySettings=true sites in internal/tui/ can
// trigger this, so the cache must detect the flag change itself rather than
// rely on being invalidated at every mutation site.
func TestCostIndexStaleOnSettingsMutation(t *testing.T) {
	st, _ := buildState(t)

	first := st.costIndex()
	if first == nil {
		t.Fatal("costIndex returned nil")
	}

	// Mutate skill-override state the way skillView does, without calling
	// invalidateCost() directly - only flipping dirtySettings, as the real
	// mutation sites do.
	st.settings.SetSkillOverride("some-skill", "off")
	st.dirtySettings = true

	if second := st.costIndex(); second == first {
		t.Fatal("costIndex must rebuild after a settings mutation (dirtySettings flip), even without an explicit invalidateCost() call")
	}
}
