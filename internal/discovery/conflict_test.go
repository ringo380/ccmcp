package discovery_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ringo380/ccmcp/internal/discovery"
)

// makePreview builds a synthetic preview directory with skills, agents,
// commands, an .mcp.json, and one hook file.
func makePreview(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	skill := filepath.Join(dir, "skills", "shared-skill")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: shared-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "shared-agent.md"), []byte("---\nname: shared-agent\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmdsDir := filepath.Join(dir, "commands")
	if err := os.MkdirAll(cmdsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdsDir, "shared-cmd.md"), []byte("# cmd\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{"mcpServers":{"shared-mcp":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	hooksDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "PreToolUse.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectConflictsAcrossCategories(t *testing.T) {
	dir := makePreview(t)
	st := discovery.ConflictState{
		SkillNames:    map[string]string{"shared-skill": "user"},
		AgentNames:    map[string]string{"shared-agent": "project"},
		CommandNames:  map[string]string{"shared-cmd": "plugin:foo@bar"},
		MCPServerKeys: map[string]string{"shared-mcp": "user/local"},
		HookEvents:    map[string]string{"PreToolUse": "user"},
		MarketplaceNames: map[string]struct{}{
			"my-mkt": {},
		},
		InstalledPluginIDs: map[string]struct{}{
			"my-plugin@my-mkt": {},
		},
	}
	mp := discovery.RemoteMarketplace{Name: "my-mkt"}
	plugin := discovery.RemotePlugin{Name: "my-plugin"}

	rep := discovery.DetectConflicts(dir, mp, plugin, st)
	if !rep.MarketplaceNameClash {
		t.Error("marketplace name clash not detected")
	}
	if !rep.PluginIDClash {
		t.Error("plugin id clash not detected")
	}
	if len(rep.Skills) != 1 || rep.Skills[0].Name != "shared-skill" {
		t.Errorf("skills clash missing: %+v", rep.Skills)
	}
	if len(rep.Agents) != 1 || rep.Agents[0].Name != "shared-agent" {
		t.Errorf("agents clash missing: %+v", rep.Agents)
	}
	if len(rep.Commands) != 1 || rep.Commands[0].Name != "shared-cmd" {
		t.Errorf("commands clash missing: %+v", rep.Commands)
	}
	if len(rep.MCPServers) != 1 || rep.MCPServers[0].Name != "shared-mcp" {
		t.Errorf("mcp clash missing: %+v", rep.MCPServers)
	}
	if len(rep.Hooks) != 1 || rep.Hooks[0].Name != "PreToolUse" {
		t.Errorf("hook clash missing: %+v", rep.Hooks)
	}
	if rep.Empty() {
		t.Error("Empty() should be false when conflicts exist")
	}
	if rep.Total() < 7 {
		t.Errorf("Total() = %d, want ≥ 7", rep.Total())
	}
}

func TestDetectConflictsNoneClean(t *testing.T) {
	dir := makePreview(t)
	st := discovery.ConflictState{}
	rep := discovery.DetectConflicts(dir, discovery.RemoteMarketplace{}, discovery.RemotePlugin{}, st)
	if !rep.Empty() {
		t.Errorf("expected empty report, got %+v", rep)
	}
}
