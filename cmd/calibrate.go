package cmd

import (
	"fmt"

	"github.com/ringo380/ccmcp/internal/claudecode"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/doctor"
	"github.com/ringo380/ccmcp/internal/paths"
)

// calibrateClaudeVersion detects the installed Claude Code version, folds an
// optional model override (settings.json `ccmcpClaudeModel`) into the doctor
// package's resolved default model, and returns the detected version plus the
// derived capabilities. Best-effort: an undetected version yields the
// conservative baseline and the prior default model.
func calibrateClaudeVersion(p paths.Paths) (claudecode.Version, claudecode.Capabilities) {
	v := claudecode.Detect(p)
	caps := claudecode.CapabilitiesFor(v)
	model := caps.DefaultModel
	if s, err := config.LoadSettings(p.SettingsJSON); err == nil {
		if m, ok := s.ClaudeFixModel(); ok {
			model = m
		}
	}
	doctor.SetDefaultModel(model)
	return v, caps
}

// calibrationBanner is the one-line, user-facing note about which Claude Code
// version ccmcp calibrated against. Emitted to stderr so it never pollutes a
// command's stdout (or --json) payload.
func calibrationBanner(v claudecode.Version) string {
	if v.Known() {
		return fmt.Sprintf("Claude Code %s detected", v)
	}
	return "Claude Code version not detected — using baseline rules"
}
