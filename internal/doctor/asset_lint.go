package doctor

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/claudecode"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/skills"
)

// LintConfig holds the version-calibrated limits the asset linters enforce.
// Build it from a claudecode.Capabilities via LintConfigFromCapabilities (or use
// DefaultLintConfig for the baseline). The zero value is NOT valid.
//
// These rules drive the SKILL/AGENT/CMD/PLUGIN issue codes below. Severity is
// pragmatic: hard-cap violations (name over the cap, invalid charset) are errors
// because they break Claude Code itself; soft display caps are warnings.
type LintConfig struct {
	MaxSkillNameChars    int
	MaxSkillDescChars    int
	WarnSkillDescChars   int
	MaxAgentDescChars    int
	WarnAgentDescChars   int
	MaxAgentBodyTokens   int
	WarnAgentBodyTokens  int
	WarnCommandDescChars int
	WarnPluginDescChars  int
	SkillNameRe          *regexp.Regexp
}

// LintConfigFromCapabilities maps a resolved capability set into a LintConfig,
// compiling the skill-name pattern. An empty/invalid pattern falls back to the
// canonical ^[a-z0-9-]+$.
func LintConfigFromCapabilities(c claudecode.Capabilities) LintConfig {
	re, err := regexp.Compile(c.SkillNamePattern)
	if c.SkillNamePattern == "" || err != nil {
		re = regexp.MustCompile(`^[a-z0-9-]+$`)
	}
	return LintConfig{
		MaxSkillNameChars:    c.MaxSkillNameChars,
		MaxSkillDescChars:    c.MaxSkillDescChars,
		WarnSkillDescChars:   c.WarnSkillDescChars,
		MaxAgentDescChars:    c.MaxAgentDescChars,
		WarnAgentDescChars:   c.WarnAgentDescChars,
		MaxAgentBodyTokens:   c.MaxAgentBodyTokens,
		WarnAgentBodyTokens:  c.WarnAgentBodyTokens,
		WarnCommandDescChars: c.WarnCommandDescChars,
		WarnPluginDescChars:  c.WarnPluginDescChars,
		SkillNameRe:          re,
	}
}

// WithSkillDescCap returns cfg with its skill-description caps overridden by an
// explicit `skillListingMaxDescChars` value (configurable in Claude Code
// 2.1.152+). The warning threshold scales to 3/4 of the cap. A non-positive cap
// is ignored (cfg returned unchanged).
func (c LintConfig) WithSkillDescCap(cap int) LintConfig {
	if cap <= 0 {
		return c
	}
	c.MaxSkillDescChars = cap
	c.WarnSkillDescChars = cap * 3 / 4
	return c
}

// DefaultLintConfig is the baseline limit set, derived from claudecode.Baseline()
// so the lint defaults and the version-capability baseline never drift. The
// no-config Lint* wrappers use it, preserving behavior when a caller hasn't
// detected a version.
var DefaultLintConfig = LintConfigFromCapabilities(claudecode.Baseline())

// LintSkills validates every discovered skill against the baseline CC skill
// constraints. See LintSkillsWithConfig for the version-calibrated variant.
func LintSkills(sks []skills.Skill) []Issue {
	return LintSkillsWithConfig(sks, DefaultLintConfig)
}

// LintSkillsWithConfig validates every discovered skill against cfg's limits.
// Plugin-sourced skills are still scanned — users sometimes copy a plugin skill
// into their own scope and inherit the violation.
func LintSkillsWithConfig(sks []skills.Skill, cfg LintConfig) []Issue {
	var out []Issue
	for _, s := range sks {
		file := s.Dir + "/SKILL.md"
		fm, _ := assets.ReadFrontmatter(file)
		// Prefer the frontmatter `name:`; fall back to the slug derived from the
		// directory name, which is what CC ends up using when name: is absent.
		name := fm.Name
		if name == "" {
			name = s.Name
		}
		if name != "" {
			if !cfg.SkillNameRe.MatchString(name) {
				out = append(out, Issue{
					File:     file,
					Severity: SeverityError,
					Code:     "SKILL001",
					Message:  fmt.Sprintf("skill name %q must match %s (Claude Code hard requirement)", name, cfg.SkillNameRe.String()),
				})
			}
			if len(name) > cfg.MaxSkillNameChars {
				out = append(out, Issue{
					File:     file,
					Severity: SeverityError,
					Code:     "SKILL002",
					Message:  fmt.Sprintf("skill name length %d exceeds %d-character hard cap", len(name), cfg.MaxSkillNameChars),
				})
			}
		}
		// Combined description + when_to_use — the doc-documented display truncation
		// applies to their concatenation, not each field individually.
		desc := fm.Description
		if wtu, ok := fm.Raw["when_to_use"]; ok {
			if desc == "" {
				desc = wtu
			} else {
				desc = desc + " " + wtu
			}
		}
		switch {
		case len(desc) > cfg.MaxSkillDescChars:
			out = append(out, Issue{
				File:     file,
				Severity: SeverityError,
				Code:     "SKILL003",
				Message:  fmt.Sprintf("skill description+when_to_use length %d exceeds %d-character display limit — content past the cap is silently dropped", len(desc), cfg.MaxSkillDescChars),
			})
		case len(desc) > cfg.WarnSkillDescChars:
			out = append(out, Issue{
				File:     file,
				Severity: SeverityWarning,
				Code:     "SKILL003",
				Message:  fmt.Sprintf("skill description+when_to_use length %d approaches the %d-character display limit", len(desc), cfg.MaxSkillDescChars),
			})
		}
	}
	return out
}

// LintAgents validates each agent against the baseline limits. See
// LintAgentsWithConfig for the version-calibrated variant.
func LintAgents(ags []agents.Agent) []Issue {
	return LintAgentsWithConfig(ags, DefaultLintConfig)
}

// LintAgentsWithConfig validates each agent's frontmatter description length AND
// its body token count against cfg's limits.
func LintAgentsWithConfig(ags []agents.Agent, cfg LintConfig) []Issue {
	var out []Issue
	for _, a := range ags {
		desc := a.Description
		if desc == "" {
			// Fallback: re-read the file in case Discover skipped the frontmatter.
			if fm, err := assets.ReadFrontmatter(a.File); err == nil {
				desc = fm.Description
			}
		}
		switch {
		case len(desc) > cfg.MaxAgentDescChars:
			out = append(out, Issue{
				File:     a.File,
				Severity: SeverityError,
				Code:     "AGENT001",
				Message:  fmt.Sprintf("agent description length %d exceeds %d-character display limit", len(desc), cfg.MaxAgentDescChars),
			})
		case len(desc) > cfg.WarnAgentDescChars:
			out = append(out, Issue{
				File:     a.File,
				Severity: SeverityWarning,
				Code:     "AGENT001",
				Message:  fmt.Sprintf("agent description length %d approaches the %d-character display limit", len(desc), cfg.MaxAgentDescChars),
			})
		}
		if bodyTokens, err := agentBodyTokenCount(a.File); err == nil {
			switch {
			case bodyTokens > cfg.MaxAgentBodyTokens:
				out = append(out, Issue{
					File:     a.File,
					Severity: SeverityError,
					Code:     "AGENT002",
					Message:  fmt.Sprintf("agent body is %d tokens, over the %d-token Claude Code budget", bodyTokens, cfg.MaxAgentBodyTokens),
				})
			case bodyTokens > cfg.WarnAgentBodyTokens:
				out = append(out, Issue{
					File:     a.File,
					Severity: SeverityWarning,
					Code:     "AGENT002",
					Message:  fmt.Sprintf("agent body is %d tokens, approaching the %d-token Claude Code budget", bodyTokens, cfg.WarnAgentBodyTokens),
				})
			}
		}
	}
	return out
}

// tokenEncoderOnce memoises the cl100k_base BPE encoder. Anthropic doesn't
// publish its tokenizer publicly; OpenAI's cl100k_base is the standard close-
// enough analog (within ~5% on prose, much closer than the 4-chars-per-token
// rule of thumb).
var (
	tokenEncoderOnce sync.Once
	tokenEncoder     *tiktoken.Tiktoken
	tokenEncoderErr  error
)

func getTokenEncoder() (*tiktoken.Tiktoken, error) {
	tokenEncoderOnce.Do(func() {
		tokenEncoder, tokenEncoderErr = tiktoken.GetEncoding("cl100k_base")
	})
	return tokenEncoder, tokenEncoderErr
}

// agentBodyTokenCount reads `path`, strips the leading YAML frontmatter
// block (if any), and returns the BPE token count of the remainder. Returns
// an error only on read or encoder-init failure — empty bodies are 0 tokens.
func agentBodyTokenCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	body := stripFrontmatter(string(data))
	enc, err := getTokenEncoder()
	if err != nil {
		return 0, err
	}
	return len(enc.Encode(body, nil, nil)), nil
}

// stripFrontmatter removes a leading `---\n...\n---\n` YAML block if present.
// Falls back to returning the input unchanged when no frontmatter is detected.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return s
	}
	// Find the closing --- on its own line.
	rest := s[4:]
	for {
		idx := strings.Index(rest, "\n---")
		if idx < 0 {
			return s
		}
		// Make sure the match is a complete line: followed by \n or EOF.
		after := idx + 4
		if after >= len(rest) || rest[after] == '\n' || rest[after] == '\r' {
			body := rest[after:]
			body = strings.TrimLeft(body, "\r\n")
			return body
		}
		rest = rest[after:]
	}
}

// LintCommands warns about over-long command descriptions against the baseline
// limits. See LintCommandsWithConfig for the version-calibrated variant.
func LintCommands(cmds []commands.Command) []Issue {
	return LintCommandsWithConfig(cmds, DefaultLintConfig)
}

// LintCommandsWithConfig warns when a command frontmatter description is so long
// the palette UX degrades. No hard limit is documented, so this is warn-only.
func LintCommandsWithConfig(cmds []commands.Command, cfg LintConfig) []Issue {
	var out []Issue
	for _, c := range cmds {
		if len(c.Description) > cfg.WarnCommandDescChars {
			out = append(out, Issue{
				File:     c.File,
				Severity: SeverityWarning,
				Code:     "CMD001",
				Message:  fmt.Sprintf("command description length %d exceeds %d-character soft limit — shorten for palette readability", len(c.Description), cfg.WarnCommandDescChars),
			})
		}
	}
	return out
}

// LintCommandShadows emits CMD002 for every command that a same-named skill
// silently shadows. Claude Code resolves `/name` to the skill when both a
// command and a skill register it, so the command never runs — a quiet
// foot-gun. `conflicts` comes from commands.FindConflicts; only the
// SkillVsCommand kind is relevant here. This is the CI/`doctor assets` surface
// for the same collisions the TUI Commands tab shows interactively.
func LintCommandShadows(conflicts []commands.Conflict) []Issue {
	var out []Issue
	for _, c := range conflicts {
		if c.Kind != commands.SkillVsCommand {
			continue
		}
		for _, p := range c.Participants {
			if p.Kind != "command" {
				continue
			}
			out = append(out, Issue{
				File:     p.File,
				Severity: SeverityWarning,
				Code:     "CMD002",
				Message:  fmt.Sprintf("command /%s is shadowed by a skill of the same name — Claude Code runs the skill, so this command never executes", c.Effective),
			})
		}
	}
	return out
}

// LintPluginManifest validates a single plugin.json against documented constraints.
// `path` is the manifest file path; `description` is the manifest's description
// field (caller pulls it from the JSON before calling, since plugin.json isn't
// frontmatter and has its own parsing).
func LintPluginManifest(path, description string) []Issue {
	return LintPluginManifestWithConfig(path, description, DefaultLintConfig)
}

// LintPluginManifestWithConfig is the version-calibrated variant of
// LintPluginManifest.
func LintPluginManifestWithConfig(path, description string, cfg LintConfig) []Issue {
	var out []Issue
	if len(description) > cfg.WarnPluginDescChars {
		out = append(out, Issue{
			File:     path,
			Severity: SeverityWarning,
			Code:     "PLUGIN001",
			Message:  fmt.Sprintf("plugin description length %d exceeds %d-character soft limit", len(description), cfg.WarnPluginDescChars),
		})
	}
	return out
}
