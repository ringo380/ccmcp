package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/report"
)

func baseSnapshot() report.Snapshot {
	return report.Snapshot{
		GeneratedAt:   time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		ProjectPath:   "/home/user/myproject",
		UserMCPs:      []string{"dropbox", "stripe"},
		LocalMCPs:     []string{"local-db"},
		PluginSourced: []string{"context7"},
		ClaudeAi:      []string{"claude.ai Gmail"},
		Overrides:     []string{"stripe"},
		Stashed:       []string{"old-mcp"},
		PluginsActive: 2,
		PluginsTotal:  3,
		SkillsEnabled: 4,
		SkillsTotal:   5,
		AgentsTotal:   2,
		CommandsTotal: 10,
		Conflicts:     nil,
	}
}

func TestWriteSnapshotJSON(t *testing.T) {
	var buf bytes.Buffer
	snap := baseSnapshot()
	if err := report.WriteJSON(snap, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"dropbox", "context7", "generatedAt", "projectPath"} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON missing %q; got:\n%s", want, out)
		}
	}
}

func TestWriteSnapshotMD(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteSnapshotMD(baseSnapshot(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"# ccmcp Snapshot", "dropbox", "context7", "## Summary", "Plugins"} {
		if !strings.Contains(out, want) {
			t.Errorf("Markdown missing %q; got:\n%s", want, out)
		}
	}
}

func TestWriteSnapshotCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteSnapshotCSV(baseSnapshot(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"category,name,detail", "user_mcp,dropbox", "stash,old-mcp"} {
		if !strings.Contains(out, want) {
			t.Errorf("CSV missing %q; got:\n%s", want, out)
		}
	}
}

func TestWriteSweepMD(t *testing.T) {
	sr := report.SweepReport{
		GeneratedAt: time.Now(),
		Rows: []report.SweepRow{
			{ProjectPath: "/home/user/proj1", UserMCPs: 2, LocalMCPs: 1, PluginsActive: 3, PluginsTotal: 4, Conflicts: 0},
			{ProjectPath: "/home/user/proj2", UserMCPs: 2, LocalMCPs: 0, PluginsActive: 1, PluginsTotal: 4, Conflicts: 1},
		},
	}
	var buf bytes.Buffer
	if err := report.WriteSweepMD(sr, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"# ccmcp Sweep", "proj1", "proj2", "| Project |"} {
		if !strings.Contains(out, want) {
			t.Errorf("Sweep MD missing %q; got:\n%s", want, out)
		}
	}
}

func TestWriteSweepCSV(t *testing.T) {
	sr := report.SweepReport{
		GeneratedAt: time.Now(),
		Rows: []report.SweepRow{
			{ProjectPath: "/home/user/proj1", UserMCPs: 2, Conflicts: 0},
		},
	}
	var buf bytes.Buffer
	if err := report.WriteSweepCSV(sr, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "projectPath") || !strings.Contains(out, "/home/user/proj1") {
		t.Errorf("Sweep CSV missing expected content; got:\n%s", out)
	}
}

func TestWriteDriftMD_changes(t *testing.T) {
	baseline := baseSnapshot()
	d := report.DriftReport{
		GeneratedAt: time.Now(),
		ProjectPath: "/home/user/myproject",
		Before:      baseline,
		MCPsAdded:   []report.DriftItem{{Name: "new-mcp", Scope: "user"}},
		MCPsRemoved: []report.DriftItem{{Name: "old-mcp", Scope: "stash"}},
		OverridesAdded: []string{"new-override"},
	}
	var buf bytes.Buffer
	if err := report.WriteDriftMD(d, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"# ccmcp Drift", "new-mcp", "old-mcp", "Override changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("Drift MD missing %q; got:\n%s", want, out)
		}
	}
}

func TestWriteDriftMD_noChanges(t *testing.T) {
	d := report.DriftReport{
		GeneratedAt: time.Now(),
		Before:      baseSnapshot(),
	}
	var buf bytes.Buffer
	if err := report.WriteDriftMD(d, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No changes detected") {
		t.Errorf("empty drift should say 'No changes detected'; got:\n%s", buf.String())
	}
}

func TestWriteAuditMD_clean(t *testing.T) {
	a := report.AuditReport{
		GeneratedAt: time.Now(),
		ProjectPath: "/home/user/proj",
	}
	var buf bytes.Buffer
	if err := report.WriteAuditMD(a, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No issues found") {
		t.Errorf("clean audit should say 'No issues found'; got:\n%s", buf.String())
	}
}

func TestWriteAuditMD_withIssues(t *testing.T) {
	a := report.AuditReport{
		GeneratedAt: time.Now(),
		ProjectPath: "/home/user/proj",
		StaleOverrides: []report.AuditItem{
			{Key: "old-plugin:foo:bar", Kind: "OrphanPlugin", Reason: "plugin not installed"},
		},
		Conflicts: []commands.Conflict{
			{Kind: "plugin-vs-user", Effective: "build", Participants: []commands.Participant{
				{Kind: "command", Scope: "user", Name: "build"},
			}},
		},
		Redundancies: []report.RedundancyItem{
			{Name: "stripe", Scopes: []string{"user", "local"}},
		},
	}
	var buf bytes.Buffer
	if err := report.WriteAuditMD(a, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Stale overrides", "old-plugin:foo:bar", "Command conflicts", "build", "Redundant MCPs", "stripe"} {
		if !strings.Contains(out, want) {
			t.Errorf("Audit MD missing %q; got:\n%s", want, out)
		}
	}
}

func TestWriteAuditCSV(t *testing.T) {
	a := report.AuditReport{
		GeneratedAt: time.Now(),
		ProjectPath: "/home/user/proj",
		StaleOverrides: []report.AuditItem{
			{Key: "ghost-key", Kind: "OrphanStdio", Reason: "no matching source"},
		},
	}
	var buf bytes.Buffer
	if err := report.WriteAuditCSV(a, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "ghost-key") || !strings.Contains(out, "issue_type") {
		t.Errorf("Audit CSV missing expected content; got:\n%s", out)
	}
}

func TestDiffStrings(t *testing.T) {
	added, removed := report.DiffStrings(
		[]string{"a", "b", "c"},
		[]string{"b", "c", "d"},
	)
	if len(added) != 1 || added[0] != "d" {
		t.Errorf("added: want [d], got %v", added)
	}
	if len(removed) != 1 || removed[0] != "a" {
		t.Errorf("removed: want [a], got %v", removed)
	}
}

func TestDiffConflicts(t *testing.T) {
	old := []commands.Conflict{{Kind: "plugin-vs-user", Effective: "build"}}
	neu := []commands.Conflict{
		{Kind: "plugin-vs-user", Effective: "build"},
		{Kind: "duplicate-scope", Effective: "test"},
	}
	added, removed := report.DiffConflicts(old, neu)
	if len(added) != 1 || added[0].Effective != "test" {
		t.Errorf("conflictsAdded: want [test], got %v", added)
	}
	if len(removed) != 0 {
		t.Errorf("conflictsRemoved: want [], got %v", removed)
	}
}
