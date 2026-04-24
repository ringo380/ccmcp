// Package agents discovers Claude Code subagents across user, project, and plugin scopes.
//
// Unlike skills (directory-per-skill with SKILL.md), agents are flat `.md` files where the
// slug = filename without extension.
package agents

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ringo380/ccmcp/internal/assets"
	"github.com/ringo380/ccmcp/internal/config"
)

type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
	ScopePlugin  Scope = "plugin"
)

// Agent describes one discovered subagent.
//
// Claude Code has no native per-agent disable mechanism today (unlike skills'
// `skillOverrides`). Enabled is reported true for every discovered agent; a future
// `agentOverrides` shim will flip this.
type Agent struct {
	Name        string
	Description string
	Model       string
	Scope       Scope
	PluginID    string
	File        string // absolute path to the .md
	Enabled     bool
}

func Discover(claudeConfigDir, projectDir string, settings *config.Settings, installed *config.InstalledPlugins, pluginsDir string) []Agent {
	var out []Agent

	if claudeConfigDir != "" {
		out = append(out, scanAgentDir(filepath.Join(claudeConfigDir, "agents"), ScopeUser, "")...)
	}
	if projectDir != "" {
		out = append(out, scanAgentDir(filepath.Join(projectDir, ".claude", "agents"), ScopeProject, "")...)
	}
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
			out = append(out, scanAgentDir(filepath.Join(path, "agents"), ScopePlugin, e.ID)...)
		}
	}

	// Enablement placeholder: no native agent override mechanism yet.
	for i := range out {
		out[i].Enabled = true
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Scope < out[j].Scope
	})
	return out
}

func scanAgentDir(root string, scope Scope, pluginID string) []Agent {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Agent
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		file := filepath.Join(root, e.Name())
		fm, _ := assets.ReadFrontmatter(file)
		name := fm.Name
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, Agent{
			Name:        name,
			Description: fm.Description,
			Model:       fm.Model,
			Scope:       scope,
			PluginID:    pluginID,
			File:        file,
		})
	}
	return out
}
