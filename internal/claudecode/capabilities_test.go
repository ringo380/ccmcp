package claudecode

import "testing"

func TestCapabilitiesForUnknownIsBaseline(t *testing.T) {
	got := CapabilitiesFor(Version{})
	if got != Baseline() {
		t.Errorf("unknown version must yield Baseline\n got: %+v\nwant: %+v", got, Baseline())
	}
	if got.SupportsFallbackModel {
		t.Error("baseline must not advertise fallback-model support")
	}
}

func TestCapabilitiesForFallbackModelBoundary(t *testing.T) {
	cases := []struct {
		ver      string
		fallback bool
	}{
		{"2.1.141", false},
		{"2.1.151", false},
		{"2.1.152", true},
		{"2.1.158", true},
		{"3.0.0", true},
	}
	for _, c := range cases {
		caps := CapabilitiesFor(ParseVersion(c.ver))
		if caps.SupportsFallbackModel != c.fallback {
			t.Errorf("CapabilitiesFor(%s).SupportsFallbackModel=%v, want %v", c.ver, caps.SupportsFallbackModel, c.fallback)
		}
	}
}

func TestBaselineLimitsMatchDocumentedConstraints(t *testing.T) {
	b := Baseline()
	if b.MaxSkillNameChars != 64 || b.MaxSkillDescChars != 1536 || b.MaxAgentBodyTokens != 15000 {
		t.Errorf("baseline limits drifted: %+v", b)
	}
	if b.SkillNamePattern != `^[a-z0-9-]+$` {
		t.Errorf("baseline skill-name pattern drifted: %q", b.SkillNamePattern)
	}
	if b.DefaultModel == "" || b.FallbackModel == "" {
		t.Errorf("baseline must define default + fallback models: %+v", b)
	}
}
