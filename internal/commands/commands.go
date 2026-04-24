// Package commands discovers Claude Code slash commands across user, project, and plugin scopes,
// and classifies duplicate-alias conflicts between sources.
package commands

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

// Command describes one discovered slash command.
//
// The effective slash-command name that Claude Code exposes depends on scope:
//   - user/project:   /<slug>
//   - plugin:          /<plugin-name>:<slug>
//
// Effective is the full form including any namespace prefix. Slug is the bare
// filename (without .md). Name is the frontmatter `name:` if present, else Slug.
type Command struct {
	Slug        string
	Name        string
	Effective   string
	Description string
	Scope       Scope
	PluginID    string
	File        string
}

func Discover(claudeConfigDir, projectDir string, settings *config.Settings, installed *config.InstalledPlugins, pluginsDir string) []Command {
	var out []Command

	if claudeConfigDir != "" {
		out = append(out, scanCmdDir(filepath.Join(claudeConfigDir, "commands"), ScopeUser, "", "")...)
	}
	if projectDir != "" {
		out = append(out, scanCmdDir(filepath.Join(projectDir, ".claude", "commands"), ScopeProject, "", "")...)
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
			pluginName, _ := config.ParsePluginID(e.ID)
			out = append(out, scanCmdDir(filepath.Join(path, "commands"), ScopePlugin, e.ID, pluginName)...)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Effective != out[j].Effective {
			return out[i].Effective < out[j].Effective
		}
		return out[i].Scope < out[j].Scope
	})
	return out
}

func scanCmdDir(root string, scope Scope, pluginID, pluginName string) []Command {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Command
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		file := filepath.Join(root, e.Name())
		fm, _ := assets.ReadFrontmatter(file)
		slug := strings.TrimSuffix(e.Name(), ".md")
		name := fm.Name
		if name == "" {
			name = slug
		}
		eff := slug
		if scope == ScopePlugin && pluginName != "" {
			eff = pluginName + ":" + slug
		}
		out = append(out, Command{
			Slug:        slug,
			Name:        name,
			Effective:   eff,
			Description: fm.Description,
			Scope:       scope,
			PluginID:    pluginID,
			File:        file,
		})
	}
	return out
}
