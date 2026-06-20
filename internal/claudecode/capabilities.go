package claudecode

// Capabilities is the version-derived behavior bundle. ccmcp resolves it once
// from the detected Version; every consumer reads these fields rather than
// re-deriving from the version, so version logic stays confined to
// CapabilitiesFor.
type Capabilities struct {
	// Asset-lint limits - Claude Code skill/agent/command/plugin constraints.
	MaxSkillNameChars    int
	MaxSkillDescChars    int
	WarnSkillDescChars   int
	MaxAgentDescChars    int
	WarnAgentDescChars   int
	MaxAgentBodyTokens   int
	WarnAgentBodyTokens  int
	WarnCommandDescChars int
	WarnPluginDescChars  int
	SkillNamePattern     string

	// Headless `claude --print` fix/review model selection.
	DefaultModel          string // full model ID (also used on the HTTP path)
	FallbackModel         string // passed as --fallback-model when supported
	SupportsFallbackModel bool   // CC >= 2.1.152 auto-switches on model-not-found
}

// Baseline is the conservative, known-good capability set, calibrated to Claude
// Code 2.1.141 (the oldest layout ccmcp targets). Used verbatim when the
// installed version is undetectable, and as the starting point for every
// newer-version delta in CapabilitiesFor.
//
// These limits are codified from the official docs:
//   - Skill `name` is lowercase letters/digits/hyphens with a hard 64-char cap.
//   - Skill `description` + `when_to_use` combined is display-truncated at 1536
//     characters in skill listings (overrun is silently dropped). As of CC
//     2.1.152 this cap is configurable via `skillListingMaxDescChars` - callers
//     that read it should override MaxSkillDescChars accordingly.
//   - Agent `description` follows the same truncation; agent bodies are budgeted
//     at ~15k tokens.
//   - Command / plugin manifest descriptions have no hard cap; long values
//     degrade the palette/listing UX, so they warn-only.
func Baseline() Capabilities {
	return Capabilities{
		MaxSkillNameChars:    64,
		MaxSkillDescChars:    1536,
		WarnSkillDescChars:   1200,
		MaxAgentDescChars:    1536,
		WarnAgentDescChars:   1200,
		MaxAgentBodyTokens:   15000,
		WarnAgentBodyTokens:  13000,
		WarnCommandDescChars: 500,
		WarnPluginDescChars:  500,
		SkillNamePattern:     `^[a-z0-9-]+$`,
		// Haiku is plenty for the mechanical edits the fix/review tasks describe;
		// Sonnet/Opus burned tokens on prose responses. Kept a full model ID (not
		// a CLI alias) because it's also used on the Anthropic HTTP path.
		DefaultModel: "claude-haiku-4-5",
		// FallbackModel is used only on the CLI path (--fallback-model), so CC
		// recovers if DefaultModel is retired. A current Sonnet, very unlikely to
		// be retired in the same window as the Haiku above.
		FallbackModel: "claude-sonnet-4-6",
	}
}

// CapabilitiesFor resolves the capability set for a detected version. An unknown
// version yields the Baseline.
//
// THIS IS THE SINGLE PLACE TO EDIT when a new Claude Code version changes a
// limit, model, or feature - add one `if v.AtLeast("X.Y.Z") { ... }` branch.
func CapabilitiesFor(v Version) Capabilities {
	c := Baseline()
	if !v.Known() {
		return c
	}
	// CC >= 2.1.152 switches to --fallback-model automatically when the primary
	// model is not found, so it's safe to pass one on headless invocations.
	if v.AtLeast("2.1.152") {
		c.SupportsFallbackModel = true
	}
	return c
}
