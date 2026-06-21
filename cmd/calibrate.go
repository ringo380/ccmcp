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
	// Best-effort settings load: an unreadable/malformed settings.json simply
	// drops the model override here (vs. fatal in `doctor assets`, which loads +
	// validates settings itself and passes it to calibrateClaudeVersionWith).
	s, _ := config.LoadSettings(p.SettingsJSON)
	return calibrateClaudeVersionWith(p, s)
}

// calibrateClaudeVersionWith is calibrateClaudeVersion with the caller's
// already-loaded settings, so commands that have parsed settings.json don't
// re-read it. A nil s means "no override" (capability default model).
func calibrateClaudeVersionWith(p paths.Paths, s *config.Settings) (claudecode.Version, claudecode.Capabilities) {
	v := claudecode.Detect(p)
	caps := claudecode.CapabilitiesFor(v)
	model := caps.DefaultModel
	// Fold only the file (SrcConfig) tier into the default here; the env var
	// CCMCP_CLAUDE_MODEL is applied later by doctor.ResolvedModel, so it would
	// win regardless and must not be baked in as the default.
	if p.AppConfig != "" {
		if m, src := config.LoadAppConfig(p.AppConfig).ClaudeModel(); m != "" && src == config.SrcConfig {
			model = m
		}
	}
	if s != nil {
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
	return "Claude Code version not detected - using baseline rules"
}
