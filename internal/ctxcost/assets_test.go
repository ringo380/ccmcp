package ctxcost

import (
	"testing"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/skills"
)

func TestBuildAttributesPluginAssetsToTheirPlugin(t *testing.T) {
	in := Input{
		Skills: []skills.Skill{
			{Name: "alpha", Description: "does the alpha thing", Scope: skills.ScopePlugin, PluginID: "p1@mkt", Enabled: true},
			{Name: "beta", Description: "does the beta thing", Scope: skills.ScopePlugin, PluginID: "p2@mkt", Enabled: true},
		},
		Agents: []agents.Agent{
			{Name: "helper", Description: "helps", Scope: agents.ScopePlugin, PluginID: "p1@mkt", Enabled: true},
		},
		Commands: []commands.Command{
			{Name: "deploy", Effective: "deploy", Description: "ships it", Scope: commands.ScopePlugin, PluginID: "p1@mkt"},
		},
	}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p1 := idx.ByPlugin["p1@mkt"]
	if p1.Skills.Loaded == 0 || p1.Agents.Loaded == 0 || p1.Commands.Loaded == 0 {
		t.Fatalf("p1 must have all three kinds counted, got %+v", p1)
	}
	if p1.Items != 3 {
		t.Fatalf("p1.Items = %d, want 3", p1.Items)
	}
	if idx.ByPlugin["p2@mkt"].Items != 1 {
		t.Fatalf("p2.Items = %d, want 1", idx.ByPlugin["p2@mkt"].Items)
	}

	// For assets, deferral does not apply: Loaded == Deferred.
	if p1.Total().Loaded != p1.Total().Deferred {
		t.Fatalf("asset cost must not differ loaded vs deferred: %+v", p1.Total())
	}

	// The project total must equal the sum of the parts.
	wantLoaded := idx.ByPlugin["p1@mkt"].Total().Loaded + idx.ByPlugin["p2@mkt"].Total().Loaded
	if idx.Project.Total().Loaded != wantLoaded {
		t.Fatalf("Project total = %d, want %d", idx.Project.Total().Loaded, wantLoaded)
	}
}

func TestBuildSkipsDisabledAssets(t *testing.T) {
	in := Input{
		Skills: []skills.Skill{
			{Name: "on", Description: "counted", Scope: skills.ScopePlugin, PluginID: "p@m", Enabled: true},
			{Name: "off", Description: "not counted", Scope: skills.ScopePlugin, PluginID: "p@m", Enabled: false},
		},
	}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := idx.ByPlugin["p@m"]
	if got.Items != 1 {
		t.Fatalf("Items = %d, want 1 (disabled skill must not count)", got.Items)
	}
	if got.Skills.Loaded <= 0 {
		t.Fatalf("the enabled skill must still contribute a positive cost, got %+v", got.Skills)
	}
}

// A user- or project-scope asset is real per-turn context but belongs to no
// plugin. It must land in the project total without inventing a plugin key.
func TestBuildCountsNonPluginAssetsInProjectOnly(t *testing.T) {
	in := Input{
		Skills: []skills.Skill{
			{Name: "mine", Description: "a personal skill", Scope: skills.ScopeUser, Enabled: true},
		},
	}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(idx.ByPlugin) != 0 {
		t.Fatalf("ByPlugin = %v, want empty", idx.ByPlugin)
	}
	if idx.Project.Skills.Loaded == 0 {
		t.Fatalf("user-scope skill must still count toward the project total")
	}
}

func TestBuildIgnoresAssetsWithNoDescription(t *testing.T) {
	in := Input{
		Skills: []skills.Skill{
			{Name: "nodesc", Description: "", Scope: skills.ScopePlugin, PluginID: "p@m", Enabled: true},
		},
	}
	idx, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Name still costs tokens even with no description, so this is counted -
	// but it must not panic or produce a negative/zero-item entry.
	if idx.ByPlugin["p@m"].Items != 1 {
		t.Fatalf("Items = %d, want 1", idx.ByPlugin["p@m"].Items)
	}
	if idx.ByPlugin["p@m"].Skills.Loaded <= 0 {
		t.Fatalf("a named skill costs > 0 tokens even with an empty description")
	}
}
