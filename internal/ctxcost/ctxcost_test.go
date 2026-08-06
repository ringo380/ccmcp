package ctxcost

import "testing"

func TestCostAddSumsBothFigures(t *testing.T) {
	got := Cost{Loaded: 100, Deferred: 10}.Add(Cost{Loaded: 5, Deferred: 2})
	if got.Loaded != 105 || got.Deferred != 12 {
		t.Fatalf("Add = %+v, want {105 12}", got)
	}
}

func TestBreakdownTotalSumsEveryKind(t *testing.T) {
	b := Breakdown{
		Skills:   Cost{Loaded: 10, Deferred: 10},
		Agents:   Cost{Loaded: 20, Deferred: 20},
		Commands: Cost{Loaded: 30, Deferred: 30},
		MCP:      Cost{Loaded: 40, Deferred: 4},
	}
	got := b.Total()
	if got.Loaded != 100 {
		t.Fatalf("Total().Loaded = %d, want 100", got.Loaded)
	}
	if got.Deferred != 64 {
		t.Fatalf("Total().Deferred = %d, want 64", got.Deferred)
	}
}

// An unmeasured source must never be counted as zero tokens - it must be
// counted as an unknown, so the UI can render "-" and say how many are missing.
func TestUnmeasuredIsTrackedSeparatelyFromZero(t *testing.T) {
	var b Breakdown
	b.AddSource(Source{Tier: TierUnknown, Reason: "never probed"})
	b.AddSource(Source{Cost: Cost{Loaded: 7, Deferred: 7}, Tier: TierExact})

	if b.Unmeasured != 1 {
		t.Fatalf("Unmeasured = %d, want 1", b.Unmeasured)
	}
	if b.Total().Loaded != 7 {
		t.Fatalf("Total().Loaded = %d, want 7 (unknown contributes nothing)", b.Total().Loaded)
	}
	if b.Items != 1 {
		t.Fatalf("Items = %d, want 1 (only measured sources count as items)", b.Items)
	}
}
