package paths

import (
	"os"
	"path/filepath"
)

// Paths resolves every config file ccmcp needs to read or write.
// Honors $CLAUDE_CONFIG_DIR and $CCMCP_HOME if set (useful for tests).
type Paths struct {
	Home             string
	ClaudeConfigDir  string // ~/.claude (or $CLAUDE_CONFIG_DIR)
	ClaudeJSON       string // ~/.claude.json
	SettingsJSON     string // ~/.claude/settings.json
	SettingsLocal    string // ~/.claude/settings.local.json
	PluginsDir       string // ~/.claude/plugins
	InstalledPlugins string // ~/.claude/plugins/installed_plugins.json
	KnownMarkets     string // ~/.claude/plugins/known_marketplaces.json
	Stash            string // ~/.claude-mcp-stash.json
	Profiles         string // ~/.claude-mcp-profiles.json
	BackupsDir       string // ~/.claude-mcp-backups
	Ignores          string // ~/.claude-ccmcp-ignores.json (ccmcp-owned conflict ignore list)
}

func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	cfgDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if cfgDir == "" {
		cfgDir = filepath.Join(home, ".claude")
	}
	p := Paths{
		Home:             home,
		ClaudeConfigDir:  cfgDir,
		ClaudeJSON:       filepath.Join(home, ".claude.json"),
		SettingsJSON:     filepath.Join(cfgDir, "settings.json"),
		SettingsLocal:    filepath.Join(cfgDir, "settings.local.json"),
		PluginsDir:       filepath.Join(cfgDir, "plugins"),
		InstalledPlugins: filepath.Join(cfgDir, "plugins", "installed_plugins.json"),
		KnownMarkets:     filepath.Join(cfgDir, "plugins", "known_marketplaces.json"),
		Stash:            filepath.Join(home, ".claude-mcp-stash.json"),
		Profiles:         filepath.Join(home, ".claude-mcp-profiles.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
	}
	return p, nil
}

// ProjectMCPJSON returns <projectDir>/.mcp.json.
func ProjectMCPJSON(projectDir string) string {
	return filepath.Join(projectDir, ".mcp.json")
}

// ProjectSettings returns <projectDir>/.claude/settings.json.
func ProjectSettings(projectDir string) string {
	return filepath.Join(projectDir, ".claude", "settings.json")
}

// ProjectSettingsLocal returns <projectDir>/.claude/settings.local.json.
func ProjectSettingsLocal(projectDir string) string {
	return filepath.Join(projectDir, ".claude", "settings.local.json")
}
