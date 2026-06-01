package config

import "testing"

func TestSettingsClaudeFixModel(t *testing.T) {
	s := &Settings{Path: "<mem>", Raw: map[string]any{"ccmcpClaudeModel": "claude-sonnet-4-6"}}
	if m, ok := s.ClaudeFixModel(); !ok || m != "claude-sonnet-4-6" {
		t.Errorf("ClaudeFixModel()=%q,%v; want claude-sonnet-4-6,true", m, ok)
	}

	empty := &Settings{Path: "<mem>", Raw: map[string]any{}}
	if _, ok := empty.ClaudeFixModel(); ok {
		t.Error("absent ccmcpClaudeModel should report (\"\", false)")
	}

	blank := &Settings{Path: "<mem>", Raw: map[string]any{"ccmcpClaudeModel": ""}}
	if _, ok := blank.ClaudeFixModel(); ok {
		t.Error("empty ccmcpClaudeModel should report (\"\", false)")
	}
}

func TestSettingsSkillListingMaxDescChars(t *testing.T) {
	// JSON numbers decode as float64.
	s := &Settings{Path: "<mem>", Raw: map[string]any{"skillListingMaxDescChars": float64(2048)}}
	if n, ok := s.SkillListingMaxDescChars(); !ok || n != 2048 {
		t.Errorf("SkillListingMaxDescChars()=%d,%v; want 2048,true", n, ok)
	}

	absent := &Settings{Path: "<mem>", Raw: map[string]any{}}
	if n, ok := absent.SkillListingMaxDescChars(); ok || n != 0 {
		t.Errorf("absent key should report 0,false; got %d,%v", n, ok)
	}

	garbage := &Settings{Path: "<mem>", Raw: map[string]any{"skillListingMaxDescChars": "lots"}}
	if _, ok := garbage.SkillListingMaxDescChars(); ok {
		t.Error("non-numeric value should report (0, false)")
	}

	zero := &Settings{Path: "<mem>", Raw: map[string]any{"skillListingMaxDescChars": float64(0)}}
	if _, ok := zero.SkillListingMaxDescChars(); ok {
		t.Error("non-positive value should report (0, false)")
	}
}
