package report

import (
	"time"

	"github.com/ringo380/ccmcp/internal/commands"
)

// Snapshot is a point-in-time capture of one project's full ccmcp state.
type Snapshot struct {
	GeneratedAt   time.Time          `json:"generatedAt"`
	ProjectPath   string             `json:"projectPath"`
	UserMCPs      []string           `json:"userMcps"`
	LocalMCPs     []string           `json:"localMcps"`
	McpjsonShared []string           `json:"mcpjsonShared"`
	PluginSourced []string           `json:"pluginSourced"`
	ClaudeAi      []string           `json:"claudeAi"`
	Overrides     []string           `json:"disabledHere"`
	Stashed       []string           `json:"stashed"`
	PluginsActive int                `json:"pluginsActive"`
	PluginsTotal  int                `json:"pluginsTotal"`
	SkillsEnabled int                `json:"skillsEnabled"`
	SkillsTotal   int                `json:"skillsTotal"`
	AgentsTotal   int                `json:"agentsTotal"`
	CommandsTotal int                `json:"commandsTotal"`
	Conflicts     []commands.Conflict `json:"commandConflicts"`
}

// SweepRow summarises one project directory.
type SweepRow struct {
	ProjectPath   string `json:"projectPath"`
	UserMCPs      int    `json:"userMcps"`
	LocalMCPs     int    `json:"localMcps"`
	Overrides     int    `json:"overrides"`
	PluginsActive int    `json:"pluginsActive"`
	PluginsTotal  int    `json:"pluginsTotal"`
	SkillsEnabled int    `json:"skillsEnabled"`
	SkillsTotal   int    `json:"skillsTotal"`
	AgentsTotal   int    `json:"agentsTotal"`
	CommandsTotal int    `json:"commandsTotal"`
	Conflicts     int    `json:"conflicts"`
}

// SweepReport is the full set of rows from a sweep.
type SweepReport struct {
	GeneratedAt time.Time  `json:"generatedAt"`
	Rows        []SweepRow `json:"projects"`
}

// DriftItem describes a single added or removed item.
type DriftItem struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// DriftReport compares two snapshots.
type DriftReport struct {
	GeneratedAt time.Time   `json:"generatedAt"`
	ProjectPath string      `json:"projectPath"`
	Before      Snapshot    `json:"before"`
	MCPsAdded   []DriftItem `json:"mcpsAdded"`
	MCPsRemoved []DriftItem `json:"mcpsRemoved"`
	OverridesAdded   []string `json:"overridesAdded"`
	OverridesRemoved []string `json:"overridesRemoved"`
	ConflictsAdded   []commands.Conflict `json:"conflictsAdded"`
	ConflictsRemoved []commands.Conflict `json:"conflictsRemoved"`
}

// AuditItem is one entry in a list of issues found during audit.
type AuditItem struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// RedundancyItem describes an MCP present in multiple scopes simultaneously.
type RedundancyItem struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

// AuditReport is a comprehensive audit of one project's state.
type AuditReport struct {
	GeneratedAt    time.Time          `json:"generatedAt"`
	ProjectPath    string             `json:"projectPath"`
	StaleOverrides []AuditItem        `json:"staleOverrides"`
	Conflicts      []commands.Conflict `json:"commandConflicts"`
	Redundancies   []RedundancyItem   `json:"redundancies"`
}
