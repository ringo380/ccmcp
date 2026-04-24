// Package skills discovers Claude Code skills across user, project, and plugin scopes.
package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/config"
)

// Scope identifies where a skill lives on disk.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
	ScopePlugin  Scope = "plugin"
)

// Skill describes one discovered skill. Plugin-sourced skills carry PluginID so
// callers can attribute them; user/project skills leave PluginID empty.
type Skill struct {
	Name        string // slug derived from dir name (or frontmatter `name:` if present)
	Description string // from frontmatter
	Scope       Scope
	PluginID    string // only set when Scope == ScopePlugin; qualified "name@marketplace"
	Dir         string // absolute path of the skill directory (contains SKILL.md)
	Enabled     bool   // derived from settings.skillOverrides (true when no "off" override)
}

// Discover returns every skill visible to Claude Code, sorted by name then scope.
// `projectDir` may be "" to skip project-scope discovery.
func Discover(claudeConfigDir, projectDir string, settings *config.Settings, installed *config.InstalledPlugins, pluginsDir string) []Skill {
	var out []Skill

	// User scope: ~/.claude/skills/<name>/SKILL.md
	if claudeConfigDir != "" {
		out = append(out, scanSkillDir(filepath.Join(claudeConfigDir, "skills"), ScopeUser, "")...)
	}
	// Project scope: <proj>/.claude/skills/<name>/SKILL.md
	if projectDir != "" {
		out = append(out, scanSkillDir(filepath.Join(projectDir, ".claude", "skills"), ScopeProject, "")...)
	}
	// Plugin scope: <installPath>/skills/<name>/SKILL.md for every REGISTERED plugin
	// (enabled or not — we surface disabled plugins' skills so users can see them).
	if settings != nil && installed != nil {
		paths := map[string]string{}
		for _, p := range installed.List() {
			paths[p.ID] = p.InstallPath
		}
		for _, e := range settings.PluginEntries() {
			path := paths[e.ID]
			if path == "" {
				name, mkt := config.ParsePluginID(e.ID)
				if mkt == "" {
					continue
				}
				cand := filepath.Join(pluginsDir, "cache", mkt, name, "unknown")
				if _, err := os.Stat(cand); err == nil {
					path = cand
				} else {
					continue
				}
			}
			out = append(out, scanSkillDir(filepath.Join(path, "skills"), ScopePlugin, e.ID)...)
		}
	}

	// Enablement: settings.skillOverrides[name]="off" disables a skill globally.
	if settings != nil {
		for i := range out {
			override, ok := settings.SkillOverride(out[i].Name)
			out[i].Enabled = !(ok && override == "off")
		}
	} else {
		for i := range out {
			out[i].Enabled = true
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Scope < out[j].Scope
	})
	return out
}

func scanSkillDir(root string, scope Scope, pluginID string) []Skill {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		skillDir := filepath.Join(root, e.Name())
		skillFile := filepath.Join(skillDir, "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			continue
		}
		fm, _ := assets.ReadFrontmatter(skillFile)
		name := fm.Name
		if name == "" {
			name = e.Name()
		}
		out = append(out, Skill{
			Name:        name,
			Description: fm.Description,
			Scope:       scope,
			PluginID:    pluginID,
			Dir:         skillDir,
		})
	}
	return out
}
