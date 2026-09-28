package paths

import (
	"os"
	"path/filepath"
	"strings"
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
	ProbeCache       string // ~/.claude-mcp-probe-cache.json
	BackupsDir       string // ~/.claude-mcp-backups
	Ignores          string // ~/.claude-ccmcp-ignores.json (ccmcp-owned conflict ignore list)
	AppConfig        string // ~/.claude-mcp-config.json
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
		ProbeCache:       filepath.Join(home, ".claude-mcp-probe-cache.json"),
		BackupsDir:       filepath.Join(home, ".claude-mcp-backups"),
		Ignores:          filepath.Join(home, ".claude-ccmcp-ignores.json"),
		AppConfig:        filepath.Join(home, ".claude-mcp-config.json"),
	}
	return p, nil
}

// TranscriptSlug converts a project path into the directory name Claude Code
// uses under <claudeConfigDir>/projects: every character outside [A-Za-z0-9]
// becomes "-", with no run collapsing.
//
// Derived from the live directories under ~/.claude/projects, not assumed:
// "/Users/ryanrobson/git/clubdeck.github.io" is stored as
// "-Users-ryanrobson-git-clubdeck-github-io" and "/Users/ryanrobson/.claude" as
// "-Users-ryanrobson--claude" (adjacent separators are kept, so "/." yields
// "--"). Replacing just "/" left calibration silently dead for every project
// path containing a dot, underscore, or space.
func TranscriptSlug(projectDir string) string {
	return slug(projectDir, false)
}

// LegacyTranscriptSlug is the older encoding Claude Code used before ~2026-04,
// which preserved "." and "_" verbatim. Directories written back then still
// carry it, so a lookup that only tries TranscriptSlug reports real, populated
// state as missing.
func LegacyTranscriptSlug(projectDir string) string {
	return slug(projectDir, true)
}

func slug(projectDir string, keepDotUnderscore bool) string {
	var b strings.Builder
	b.Grow(len(projectDir))
	for _, r := range projectDir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case keepDotUnderscore && (r == '.' || r == '_'):
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ProjectStateDir returns <claudeConfigDir>/projects/<slug> for projectDir.
//
// It prefers the current slug, but when that directory does not exist and the
// legacy-slug one does, it returns the legacy path. Without the fallback a
// project whose path contains "." or "_" and was first opened before ~2026-04
// resolves to a nonexistent directory, so its transcripts look absent and
// doctor reports MEM001 against a populated memory dir.
func ProjectStateDir(claudeConfigDir, projectDir string) string {
	cur := filepath.Join(claudeConfigDir, "projects", TranscriptSlug(projectDir))
	legacy := LegacyTranscriptSlug(projectDir)
	if legacy == TranscriptSlug(projectDir) {
		return cur
	}
	if _, err := os.Stat(cur); err == nil {
		return cur
	}
	alt := filepath.Join(claudeConfigDir, "projects", legacy)
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	return cur
}

// ProjectMemoryDir returns the per-project memory directory, honoring the
// legacy-slug fallback in ProjectStateDir.
func ProjectMemoryDir(claudeConfigDir, projectDir string) string {
	return filepath.Join(ProjectStateDir(claudeConfigDir, projectDir), "memory")
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
