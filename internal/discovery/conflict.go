package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/skills"
)

// ConflictState is the snapshot of installed Claude Code state that conflict
// detection compares the incoming preview against. Constructing it from
// existing scanner outputs keeps the detector a pure function — easy to test.
type ConflictState struct {
	// SkillNames maps a skill name to a one-line "where it comes from"
	// description (e.g. "user", "project", or "plugin:foo@official").
	SkillNames    map[string]string
	AgentNames    map[string]string
	CommandNames  map[string]string
	MCPServerKeys map[string]string
	HookEvents    map[string]string

	// MarketplaceNames is the set of marketplace names already known to
	// settings.extraKnownMarketplaces (or cloned on disk).
	MarketplaceNames map[string]struct{}
	// InstalledPluginIDs is the set of "<name>@<marketplace>" IDs already in
	// installed_plugins.json.
	InstalledPluginIDs map[string]struct{}
}

// BuildState assembles a ConflictState from the existing scanners' output.
// Callers in TUI/CLI fan out the same Discover() that fills the other tabs.
func BuildState(
	skillRows []skills.Skill,
	agentRows []agents.Agent,
	commandRows []commands.Command,
	mcpServerKeys []string,
	hookEvents map[string]string,
	marketplaceNames []string,
	installedPluginIDs []string,
) ConflictState {
	st := ConflictState{
		SkillNames:         map[string]string{},
		AgentNames:         map[string]string{},
		CommandNames:       map[string]string{},
		MCPServerKeys:      map[string]string{},
		HookEvents:         map[string]string{},
		MarketplaceNames:   map[string]struct{}{},
		InstalledPluginIDs: map[string]struct{}{},
	}
	for _, s := range skillRows {
		st.SkillNames[s.Name] = describeScopePlugin(string(s.Scope), s.PluginID)
	}
	for _, a := range agentRows {
		st.AgentNames[a.Name] = describeScopePlugin(string(a.Scope), a.PluginID)
	}
	for _, c := range commandRows {
		st.CommandNames[c.Name] = describeScopePlugin(string(c.Scope), c.PluginID)
	}
	for _, k := range mcpServerKeys {
		st.MCPServerKeys[k] = "user/local"
	}
	for k, v := range hookEvents {
		st.HookEvents[k] = v
	}
	for _, n := range marketplaceNames {
		st.MarketplaceNames[n] = struct{}{}
	}
	for _, id := range installedPluginIDs {
		st.InstalledPluginIDs[id] = struct{}{}
	}
	return st
}

func describeScopePlugin(scope, pluginID string) string {
	if pluginID != "" {
		return "plugin:" + pluginID
	}
	if scope == "" {
		return "(unknown scope)"
	}
	return scope
}

// DetectConflicts walks a freshly cloned preview directory and reports every
// name collision against the installed state. The previewDir is scanned for:
//
//   - skills/<name>/SKILL.md
//   - agents/<name>.md
//   - commands/<name>.md
//   - .mcp.json (top-level "mcpServers" map keys)
//   - hooks/*.json (hook event names)
//
// Plus the marketplace-name and plugin-ID identity checks.
func DetectConflicts(previewDir string, mp RemoteMarketplace, plugin RemotePlugin, st ConflictState) ConflictReport {
	var rep ConflictReport

	if mp.Name != "" {
		if _, ok := st.MarketplaceNames[mp.Name]; ok {
			rep.MarketplaceNameClash = true
		}
	}
	if mp.Name != "" && plugin.Name != "" {
		id := plugin.Name + "@" + mp.Name
		if _, ok := st.InstalledPluginIDs[id]; ok {
			rep.PluginIDClash = true
		}
	}

	rep.Skills = scanSkillsDir(filepath.Join(previewDir, "skills"), st.SkillNames)
	rep.Agents = scanFlatNamedDir(filepath.Join(previewDir, "agents"), st.AgentNames, ".md")
	rep.Commands = scanFlatNamedDir(filepath.Join(previewDir, "commands"), st.CommandNames, ".md")
	rep.MCPServers = scanMCPJSON(filepath.Join(previewDir, ".mcp.json"), st.MCPServerKeys)
	rep.Hooks = scanHooksDir(filepath.Join(previewDir, "hooks"), st.HookEvents)

	return rep
}

// scanSkillsDir walks <root>/<name>/SKILL.md and reports every skill whose
// name (frontmatter or directory) collides with an existing one.
func scanSkillsDir(root string, existing map[string]string) []Conflict {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Conflict
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		file := filepath.Join(dir, "SKILL.md")
		if _, err := os.Stat(file); err != nil {
			continue
		}
		name := e.Name()
		if src, ok := existing[name]; ok {
			out = append(out, Conflict{Name: name, ExistingSource: src, IncomingPath: file})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scanFlatNamedDir walks <root>/<name><ext> and reports every collision.
func scanFlatNamedDir(root string, existing map[string]string, ext string) []Conflict {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Conflict
	for _, e := range entries {
		if e.IsDir() {
			// agents/<sub>/agent.md style nested layout.
			nested := filepath.Join(root, e.Name(), "agent.md")
			if _, err := os.Stat(nested); err == nil {
				name := e.Name()
				if src, ok := existing[name]; ok {
					out = append(out, Conflict{Name: name, ExistingSource: src, IncomingPath: nested})
				}
			}
			continue
		}
		if !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ext)
		if src, ok := existing[name]; ok {
			out = append(out, Conflict{
				Name:           name,
				ExistingSource: src,
				IncomingPath:   filepath.Join(root, e.Name()),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scanMCPJSON parses the plugin's .mcp.json (when present) and reports every
// `mcpServers.<key>` collision against an existing user/local-scope server.
func scanMCPJSON(path string, existing map[string]string) []Conflict {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil
	}
	var out []Conflict
	for k := range doc.MCPServers {
		if src, ok := existing[k]; ok {
			out = append(out, Conflict{Name: k, ExistingSource: src, IncomingPath: path})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scanHooksDir walks <root>/<event>.json and reports every collision against
// an event the user already has a hook configured for.
func scanHooksDir(root string, existing map[string]string) []Conflict {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Conflict
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if src, ok := existing[name]; ok {
			out = append(out, Conflict{Name: name, ExistingSource: src, IncomingPath: filepath.Join(root, e.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
