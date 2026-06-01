package doctor

import (
	"testing"

	"github.com/ringo380/ccmcp/internal/commands"
)

func TestLintCommandShadows(t *testing.T) {
	conflicts := []commands.Conflict{
		{
			Kind:      commands.SkillVsCommand,
			Effective: "review",
			Participants: []commands.Participant{
				{Kind: "skill", Scope: "user", Name: "review", File: "/skills/review"},
				{Kind: "command", Scope: "user", Name: "review", File: "/cmds/review.md"},
			},
		},
		{
			// A non-shadow conflict must NOT produce CMD002.
			Kind:      commands.PluginVsPlugin,
			Effective: "foo:bar",
			Participants: []commands.Participant{
				{Kind: "command", Scope: "plugin", Name: "foo:bar", File: "/a"},
				{Kind: "command", Scope: "plugin", Name: "foo:bar", File: "/b"},
			},
		},
	}

	issues := LintCommandShadows(conflicts)
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 CMD002, got %d: %+v", len(issues), issues)
	}
	iss := issues[0]
	if iss.Code != "CMD002" || iss.Severity != SeverityWarning {
		t.Errorf("want CMD002 warning, got %s/%v", iss.Code, iss.Severity)
	}
	if iss.File != "/cmds/review.md" {
		t.Errorf("CMD002 should point at the command file, got %q", iss.File)
	}
}

func TestLintCommandShadowsEmpty(t *testing.T) {
	if got := LintCommandShadows(nil); len(got) != 0 {
		t.Errorf("no conflicts should yield no issues, got %+v", got)
	}
}
