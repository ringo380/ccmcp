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
