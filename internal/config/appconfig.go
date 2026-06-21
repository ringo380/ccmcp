package config

import (
	"encoding/json"
	"os"
	"strings"
)

// AppConfig wraps ~/.claude-mcp-config.json - a ccmcp-owned prefs file for the
// tool's own behavior (LLM model, update-check, offline discovery, scope, prune
// defaults). Precedence for env-backed fields is env > file > built-in default.
type AppConfig struct {
	Path string
	Raw  map[string]any
}

// Source tags where an effective value came from.
type Source string

const (
	SrcEnv     Source = "env"
	SrcConfig  Source = "config"
	SrcDefault Source = "default"
)

// JSON keys persisted in the file.
const (
	KeyClaudeModel      = "claudeModel"
	KeyOfflineDiscovery = "offlineDiscovery"
	KeyUpdateCheck      = "updateCheck"
	KeyDefaultScope     = "defaultScope"
	KeyPruneGhosts      = "pruneIncludeStashGhosts"
	KeyConfirmApply     = "confirmBeforeApply"
)

// LoadAppConfig reads the prefs file. A missing or corrupt file yields an empty
// (all-default) config; it never errors and never returns nil, so a bad file can
// never block the TUI.
func LoadAppConfig(path string) *AppConfig {
	c := &AppConfig{Path: path, Raw: map[string]any{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil || raw == nil {
		return c
	}
	c.Raw = raw
	return c
}

func (c *AppConfig) Save() error { return WriteJSON(c.Path, c.Raw) }

func (c *AppConfig) SetString(key, val string) { c.Raw[key] = val }
func (c *AppConfig) SetBool(key string, val bool) { c.Raw[key] = val }
func (c *AppConfig) Unset(key string) { delete(c.Raw, key) }

func (c *AppConfig) fileString(key string) (string, bool) {
	v, ok := c.Raw[key].(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func (c *AppConfig) fileBool(key string) (bool, bool) {
	v, ok := c.Raw[key].(bool)
	return v, ok
}

func envTruthy(name string) bool {
	v := strings.TrimSpace(os.Getenv(name))
	return v != "" && v != "0" && v != "false"
}

// --- env > file > default resolvers ---

func (c *AppConfig) ClaudeModel() (string, Source) {
	if v := strings.TrimSpace(os.Getenv("CCMCP_CLAUDE_MODEL")); v != "" {
		return v, SrcEnv
	}
	if v, ok := c.fileString(KeyClaudeModel); ok {
		return v, SrcConfig
	}
	return "", SrcDefault
}

func (c *AppConfig) OfflineDiscovery() (bool, Source) {
	if os.Getenv("CCMCP_DISCOVERY_OFFLINE") != "" {
		return true, SrcEnv
	}
	if v, ok := c.fileBool(KeyOfflineDiscovery); ok {
		return v, SrcConfig
	}
	return false, SrcDefault
}

// UpdateCheckEnabled is true when the launch-time update check should run.
// The env var CCMCP_NO_UPDATE_CHECK disables it (env wins).
func (c *AppConfig) UpdateCheckEnabled() (bool, Source) {
	if envTruthy("CCMCP_NO_UPDATE_CHECK") {
		return false, SrcEnv
	}
	if v, ok := c.fileBool(KeyUpdateCheck); ok {
		return v, SrcConfig
	}
	return true, SrcDefault
}

// --- file > default (no env tier) ---

func (c *AppConfig) DefaultScope() (string, Source) {
	if v, ok := c.fileString(KeyDefaultScope); ok {
		return v, SrcConfig
	}
	return "user", SrcDefault
}

func (c *AppConfig) PruneIncludeStashGhosts() (bool, Source) {
	if v, ok := c.fileBool(KeyPruneGhosts); ok {
		return v, SrcConfig
	}
	return false, SrcDefault
}

func (c *AppConfig) ConfirmBeforeApply() (bool, Source) {
	if v, ok := c.fileBool(KeyConfirmApply); ok {
		return v, SrcConfig
	}
	return true, SrcDefault
}
