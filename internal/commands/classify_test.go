package commands

import (
	"testing"

	"github.com/ringo380/ccmcp/internal/skills"
)

func TestFindConflictsPluginVsPlugin(t *testing.T) {
	cmds := []Command{
		{Slug: "brainstorm", Effective: "a:brainstorm", Scope: ScopePlugin, PluginID: "a@mkt"},
		{Slug: "brainstorm", Effective: "a:brainstorm", Scope: ScopePlugin, PluginID: "b@mkt"},
	}
	got := FindConflicts(cmds, nil)
	if len(got) != 1 || got[0].Kind != PluginVsPlugin {
		t.Fatalf("want 1 plugin-vs-plugin, got %+v", got)
	}
	if len(got[0].Participants) != 2 {
		t.Errorf("want 2 participants, got %d", len(got[0].Participants))
	}
}

func TestFindConflictsPluginVsUser(t *testing.T) {
	cmds := []Command{
		{Slug: "build", Effective: "build", Scope: ScopeUser},
		{Slug: "build", Effective: "foo:build", Scope: ScopePlugin, PluginID: "foo@mkt"},
	}
	got := FindConflicts(cmds, nil)
	// plugin-vs-user fires (slug collision with user command)
	var saw bool
	for _, c := range got {
		if c.Kind == PluginVsUser && c.Effective == "build" {
			saw = true
		}
	}
	if !saw {
		t.Errorf("expected plugin-vs-user on 'build'; got %+v", got)
	}
}

func TestFindConflictsSkillVsCommand(t *testing.T) {
	cmds := []Command{
		{Slug: "plans", Effective: "plans", Scope: ScopeUser},
	}
	skls := []skills.Skill{
		{Name: "plans", Scope: skills.ScopeUser},
	}
	got := FindConflicts(cmds, skls)
	if len(got) != 1 || got[0].Kind != SkillVsCommand {
		t.Fatalf("want skill-vs-command, got %+v", got)
	}
}

func TestFindConflictsNoFalsePositives(t *testing.T) {
	cmds := []Command{
		{Slug: "a", Effective: "a", Scope: ScopeUser},
		{Slug: "b", Effective: "foo:b", Scope: ScopePlugin, PluginID: "foo@mkt"},
	}
	if got := FindConflicts(cmds, nil); len(got) != 0 {
		t.Errorf("expected no conflicts; got %+v", got)
	}
}
