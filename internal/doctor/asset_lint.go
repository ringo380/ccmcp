package doctor

import (
	"fmt"
	"regexp"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/skills"
)

// Claude Code 2.1.141 constraints (codified from the official docs):
//
//   - Skill `name` is restricted to lowercase letters, digits, and hyphens with a
//     hard 64-character cap (https://code.claude.com/docs/en/skills).
//   - Skill `description` + `when_to_use` combined is display-truncated at 1536
//     characters in skill listings — overrun is silent but practically broken
//     since the truncated portion never reaches the model.
//   - Agent `description` follows the same truncation pattern.
//   - Command frontmatter `description` surfaces in command palettes and is best
//     kept short (we warn at 500 chars — no hard cap, but the palette UX degrades).
//   - Plugin manifest `description` shows in the plugin manager listings.
//
// These rules drive the SKILL/AGENT/CMD/PLUGIN issue codes below. Severity is
// pragmatic: hard-cap violations (name >64 chars, invalid charset) are errors
// because they break Claude Code itself; soft display caps are warnings.
const (
	maxSkillNameChars     = 64
	maxSkillDescChars     = 1536
	warnSkillDescChars    = 1200
	maxAgentDescChars     = 1536
	warnAgentDescChars    = 1200
	warnCommandDescChars  = 500
	warnPluginDescChars   = 500
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// LintSkills validates every discovered skill against the CC skill constraints.
// Plugin-sourced skills are still scanned — users sometimes copy a plugin skill
// into their own scope and inherit the violation.
func LintSkills(sks []skills.Skill) []Issue {
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
			if !skillNameRe.MatchString(name) {
				out = append(out, Issue{
					File:     file,
					Severity: SeverityError,
					Code:     "SKILL001",
					Message:  fmt.Sprintf("skill name %q must match ^[a-z0-9-]+$ (Claude Code 2.1.141 hard requirement)", name),
				})
			}
			if len(name) > maxSkillNameChars {
				out = append(out, Issue{
					File:     file,
					Severity: SeverityError,
					Code:     "SKILL002",
					Message:  fmt.Sprintf("skill name length %d exceeds %d-character hard cap", len(name), maxSkillNameChars),
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
		case len(desc) > maxSkillDescChars:
			out = append(out, Issue{
				File:     file,
				Severity: SeverityError,
				Code:     "SKILL003",
				Message:  fmt.Sprintf("skill description+when_to_use length %d exceeds %d-character display limit — content past the cap is silently dropped", len(desc), maxSkillDescChars),
			})
		case len(desc) > warnSkillDescChars:
			out = append(out, Issue{
				File:     file,
				Severity: SeverityWarning,
				Code:     "SKILL003",
				Message:  fmt.Sprintf("skill description+when_to_use length %d approaches the %d-character display limit", len(desc), maxSkillDescChars),
			})
		}
	}
	return out
}

// LintAgents validates each agent's frontmatter description length.
func LintAgents(ags []agents.Agent) []Issue {
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
		case len(desc) > maxAgentDescChars:
			out = append(out, Issue{
				File:     a.File,
				Severity: SeverityError,
				Code:     "AGENT001",
				Message:  fmt.Sprintf("agent description length %d exceeds %d-character display limit", len(desc), maxAgentDescChars),
			})
		case len(desc) > warnAgentDescChars:
			out = append(out, Issue{
				File:     a.File,
				Severity: SeverityWarning,
				Code:     "AGENT001",
				Message:  fmt.Sprintf("agent description length %d approaches the %d-character display limit", len(desc), maxAgentDescChars),
			})
		}
	}
	return out
}

// LintCommands warns when a command frontmatter description is so long the palette
// UX degrades. No hard limit is documented, so this is warn-only.
func LintCommands(cmds []commands.Command) []Issue {
	var out []Issue
	for _, c := range cmds {
		if len(c.Description) > warnCommandDescChars {
			out = append(out, Issue{
				File:     c.File,
				Severity: SeverityWarning,
				Code:     "CMD001",
				Message:  fmt.Sprintf("command description length %d exceeds %d-character soft limit — shorten for palette readability", len(c.Description), warnCommandDescChars),
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
	var out []Issue
	if len(description) > warnPluginDescChars {
		out = append(out, Issue{
			File:     path,
			Severity: SeverityWarning,
			Code:     "PLUGIN001",
			Message:  fmt.Sprintf("plugin description length %d exceeds %d-character soft limit", len(description), warnPluginDescChars),
		})
	}
	return out
}
