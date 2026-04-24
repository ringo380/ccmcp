package commands

import (
	"sort"

	"github.com/ringo380/ccmcp/internal/skills"
)

// ConflictKind identifies which axis of collision was detected.
type ConflictKind string

const (
	// PluginVsPlugin: two enabled plugins register the same effective slash command.
	// Example: /foo:bar from plugin A and plugin B.
	PluginVsPlugin ConflictKind = "plugin-vs-plugin"

	// PluginVsUser: a user- or project-scope command shares an effective name with a
	// plugin-scope command's slug (post-colon). Example: user `/build` vs plugin
	// `/foo:build` — the bare `/build` invocation usually resolves to user scope,
	// but tab-completion or partial matches become ambiguous.
	PluginVsUser ConflictKind = "plugin-vs-user"

	// SkillVsCommand: a skill's name matches a slash-command slug. Claude Code's
	// slash-command dispatcher and skill auto-invocation share lexical space; a
	// skill named `brainstorm` colliding with `/brainstorm` is a known foot-gun.
	SkillVsCommand ConflictKind = "skill-vs-command"

	// DuplicateScope: same effective name registered twice in the same scope
	// (e.g. two user commands with identical filenames across symlinks).
	DuplicateScope ConflictKind = "duplicate-scope"
)

// Conflict describes one detected collision. Participants lists every
// registering source in a stable, sorted order.
type Conflict struct {
	Kind      ConflictKind `json:"kind"`
	Effective string       `json:"effective"`
	Participants []Participant `json:"participants"`
}

type Participant struct {
	Kind     string `json:"kind"`     // "command" | "skill"
	Scope    string `json:"scope"`    // user|project|plugin
	Name     string `json:"name"`     // effective or skill name
	PluginID string `json:"pluginId,omitempty"`
	File     string `json:"file,omitempty"`
}

// FindConflicts walks every discovered command and skill and emits all detected
// collisions. Deterministic order: sorted by Effective, then Kind.
func FindConflicts(cmds []Command, skls []skills.Skill) []Conflict {
	var out []Conflict

	// 1) Duplicates at the effective-name level → PluginVsPlugin or DuplicateScope.
	byEff := map[string][]Command{}
	for _, c := range cmds {
		byEff[c.Effective] = append(byEff[c.Effective], c)
	}
	for eff, group := range byEff {
		if len(group) < 2 {
			continue
		}
		kind := DuplicateScope
		pluginCount := 0
		for _, c := range group {
			if c.Scope == ScopePlugin {
				pluginCount++
			}
		}
		if pluginCount == len(group) && pluginCount > 1 {
			kind = PluginVsPlugin
		}
		out = append(out, Conflict{
			Kind:         kind,
			Effective:    eff,
			Participants: cmdParticipants(group),
		})
	}

	// 2) PluginVsUser: plugin slug equal to a user/project bare command name.
	userSlugs := map[string][]Command{}
	for _, c := range cmds {
		if c.Scope == ScopeUser || c.Scope == ScopeProject {
			userSlugs[c.Slug] = append(userSlugs[c.Slug], c)
		}
	}
	for _, c := range cmds {
		if c.Scope != ScopePlugin {
			continue
		}
		if matches, ok := userSlugs[c.Slug]; ok {
			group := append([]Command{c}, matches...)
			out = append(out, Conflict{
				Kind:         PluginVsUser,
				Effective:    c.Slug,
				Participants: cmdParticipants(group),
			})
		}
	}

	// 3) SkillVsCommand: skill name equals a command slug.
	for _, s := range skls {
		if matches, ok := byEffBySlug(cmds)[s.Name]; ok {
			parts := []Participant{{
				Kind:     "skill",
				Scope:    string(s.Scope),
				Name:     s.Name,
				PluginID: s.PluginID,
				File:     s.Dir,
			}}
			parts = append(parts, cmdParticipants(matches)...)
			out = append(out, Conflict{
				Kind:         SkillVsCommand,
				Effective:    s.Name,
				Participants: parts,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Effective != out[j].Effective {
			return out[i].Effective < out[j].Effective
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func cmdParticipants(cmds []Command) []Participant {
	out := make([]Participant, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, Participant{
			Kind:     "command",
			Scope:    string(c.Scope),
			Name:     c.Effective,
			PluginID: c.PluginID,
			File:     c.File,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].PluginID < out[j].PluginID
	})
	return out
}

func byEffBySlug(cmds []Command) map[string][]Command {
	m := map[string][]Command{}
	for _, c := range cmds {
		m[c.Slug] = append(m[c.Slug], c)
	}
	return m
}
