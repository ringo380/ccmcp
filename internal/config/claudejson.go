package config

import (
	"fmt"
	"sort"
)

// ClaudeJSON is a read/write wrapper around ~/.claude.json that preserves unknown keys.
// The file mixes app telemetry with MCP config, so we operate on a generic map and only
// mutate the fields we care about.
type ClaudeJSON struct {
	Path string
	Raw  map[string]any
}

func LoadClaudeJSON(path string) (*ClaudeJSON, error) {
	raw, err := RawJSON(path)
	if err != nil {
		return nil, err
	}
	return &ClaudeJSON{Path: path, Raw: raw}, nil
}

func (c *ClaudeJSON) Save() error {
	return WriteJSON(c.Path, c.Raw)
}

// --- user-scope MCP servers -------------------------------------------------

// UserMCPs returns a copy of .mcpServers (never nil).
func (c *ClaudeJSON) UserMCPs() map[string]any {
	return objOrEmpty(c.Raw, "mcpServers")
}

func (c *ClaudeJSON) UserMCPNames() []string {
	return sortedKeys(c.UserMCPs())
}

func (c *ClaudeJSON) SetUserMCP(name string, cfg any) {
	m := objOrEmpty(c.Raw, "mcpServers")
	m[name] = cfg
	c.Raw["mcpServers"] = m
}

func (c *ClaudeJSON) DeleteUserMCP(name string) (any, bool) {
	m, ok := c.Raw["mcpServers"].(map[string]any)
	if !ok {
		return nil, false
	}
	cfg, exists := m[name]
	if !exists {
		return nil, false
	}
	delete(m, name)
	if len(m) == 0 {
		delete(c.Raw, "mcpServers")
	} else {
		c.Raw["mcpServers"] = m
	}
	return cfg, true
}

// ClearUserMCPs returns the entries that were removed.
func (c *ClaudeJSON) ClearUserMCPs() map[string]any {
	m := objOrEmpty(c.Raw, "mcpServers")
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	delete(c.Raw, "mcpServers")
	return out
}

// --- project-scope MCP servers ----------------------------------------------

// projectNode returns the raw per-project map, creating it if createMissing is true.
func (c *ClaudeJSON) projectNode(path string, createMissing bool) map[string]any {
	projects := objOrEmpty(c.Raw, "projects")
	node, ok := projects[path].(map[string]any)
	if !ok {
		if !createMissing {
			return nil
		}
		node = map[string]any{}
		projects[path] = node
		c.Raw["projects"] = projects
	}
	return node
}

func (c *ClaudeJSON) ProjectPaths() []string {
	projects := objOrEmpty(c.Raw, "projects")
	return sortedKeys(projects)
}

func (c *ClaudeJSON) ProjectMCPs(path string) map[string]any {
	node := c.projectNode(path, false)
	if node == nil {
		return map[string]any{}
	}
	return objOrEmpty(node, "mcpServers")
}

func (c *ClaudeJSON) ProjectMCPNames(path string) []string {
	return sortedKeys(c.ProjectMCPs(path))
}

func (c *ClaudeJSON) SetProjectMCP(path, name string, cfg any) {
	node := c.projectNode(path, true)
	m := objOrEmpty(node, "mcpServers")
	m[name] = cfg
	node["mcpServers"] = m
}

func (c *ClaudeJSON) DeleteProjectMCP(path, name string) bool {
	node := c.projectNode(path, false)
	if node == nil {
		return false
	}
	m, ok := node["mcpServers"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := m[name]; !exists {
		return false
	}
	delete(m, name)
	if len(m) == 0 {
		delete(node, "mcpServers")
	}
	return true
}

func (c *ClaudeJSON) ClearProjectMCPs(path string) int {
	node := c.projectNode(path, false)
	if node == nil {
		return 0
	}
	m, ok := node["mcpServers"].(map[string]any)
	if !ok {
		return 0
	}
	n := len(m)
	delete(node, "mcpServers")
	return n
}

// --- .mcp.json allow/deny lists (per-project) -------------------------------

func (c *ClaudeJSON) ProjectMcpjsonEnabled(path string) []string {
	return strSlice(c.projectNode(path, false), "enabledMcpjsonServers")
}

func (c *ClaudeJSON) ProjectMcpjsonDisabled(path string) []string {
	return strSlice(c.projectNode(path, false), "disabledMcpjsonServers")
}

func (c *ClaudeJSON) SetProjectMcpjsonEnabled(path string, names []string) {
	node := c.projectNode(path, true)
	node["enabledMcpjsonServers"] = toAny(names)
}

func (c *ClaudeJSON) SetProjectMcpjsonDisabled(path string, names []string) {
	node := c.projectNode(path, true)
	node["disabledMcpjsonServers"] = toAny(names)
}

// --- per-project MCP override list (disabledMcpServers) --------------------
//
// This field is what Claude Code's /mcp dialog writes when you toggle an MCP off
// for the current project. Entries use a prefix scheme:
//
//   "plain-name"              - stdio MCP (user/local scope or historical)
//   "claude.ai <IntegName>"   - claude.ai remote integration
//   "plugin:<plugin>:<name>"  - plugin-sourced MCP
//
// ccmcp treats all three uniformly via override_keys.go.

func (c *ClaudeJSON) ProjectDisabledMcpServers(path string) []string {
	return strSlice(c.projectNode(path, false), "disabledMcpServers")
}

func (c *ClaudeJSON) SetProjectDisabledMcpServers(path string, keys []string) {
	node := c.projectNode(path, true)
	if len(keys) == 0 {
		delete(node, "disabledMcpServers")
		return
	}
	node["disabledMcpServers"] = toAny(keys)
}

// AddProjectDisabledMcpServer appends key if not already present. Returns true if added.
func (c *ClaudeJSON) AddProjectDisabledMcpServer(path, key string) bool {
	cur := c.ProjectDisabledMcpServers(path)
	for _, k := range cur {
		if k == key {
			return false
		}
	}
	cur = append(cur, key)
	c.SetProjectDisabledMcpServers(path, cur)
	return true
}

// RemoveProjectDisabledMcpServer drops key from the list. Returns true if removed.
func (c *ClaudeJSON) RemoveProjectDisabledMcpServer(path, key string) bool {
	cur := c.ProjectDisabledMcpServers(path)
	out := cur[:0]
	found := false
	for _, k := range cur {
		if k == key {
			found = true
			continue
		}
		out = append(out, k)
	}
	if !found {
		return false
	}
	c.SetProjectDisabledMcpServers(path, out)
	return true
}

// --- claude.ai integrations history ----------------------------------------

// ClaudeAiEverConnected returns the top-level .claudeAiMcpEverConnected array —
// a history of every claude.ai remote MCP the user has ever authorized. It's our
// best local signal for enumerating remote integrations (we can't enumerate them
// from claude.ai at runtime without OAuth).
func (c *ClaudeJSON) ClaudeAiEverConnected() []string {
	arr, ok := c.Raw["claudeAiMcpEverConnected"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// --- helpers ---------------------------------------------------------------

func objOrEmpty(root map[string]any, key string) map[string]any {
	if v, ok := root[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func strSlice(node map[string]any, key string) []string {
	if node == nil {
		return nil
	}
	arr, ok := node[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// DescribeMCP returns a short type/command summary for display.
func DescribeMCP(cfg any) string {
	m, ok := cfg.(map[string]any)
	if !ok {
		return "(unknown)"
	}
	t, _ := m["type"].(string)
	cmd, _ := m["command"].(string)
	url, _ := m["url"].(string)
	switch {
	case t == "http" || t == "sse":
		if url != "" {
			return fmt.Sprintf("%s %s", t, url)
		}
		return t
	case cmd != "":
		return cmd
	default:
		return "(no command)"
	}
}
