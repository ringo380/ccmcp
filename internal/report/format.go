package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ringo380/ccmcp/internal/commands"
)

func participantNames(parts []commands.Participant) string {
	ss := make([]string, len(parts))
	for i, p := range parts {
		if p.PluginID != "" {
			ss[i] = p.PluginID
		} else {
			ss[i] = p.Scope + ":" + p.Name
		}
	}
	return strings.Join(ss, ", ")
}

// WriteJSON renders v as indented JSON.
func WriteJSON(v any, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// WriteSnapshotMD renders a Snapshot as Markdown.
func WriteSnapshotMD(s Snapshot, w io.Writer) error {
	fmt.Fprintf(w, "# ccmcp Snapshot — %s\n\n", s.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "**Project:** `%s`\n\n", s.ProjectPath)

	table := func(title string, items []string, emptyMsg string) {
		fmt.Fprintf(w, "## %s\n\n", title)
		if len(items) == 0 {
			fmt.Fprintf(w, "*%s*\n\n", emptyMsg)
			return
		}
		fmt.Fprintln(w, "| Name |")
		fmt.Fprintln(w, "|------|")
		for _, n := range items {
			fmt.Fprintf(w, "| `%s` |\n", n)
		}
		fmt.Fprintln(w)
	}

	table("User MCPs", s.UserMCPs, "none")
	table("Local MCPs (this project)", s.LocalMCPs, "none")
	table("Plugin-sourced MCPs", s.PluginSourced, "none")
	table("Shared (.mcp.json)", s.McpjsonShared, "none")
	table("claude.ai integrations", s.ClaudeAi, "none")
	table("Per-project disabled (overrides)", s.Overrides, "none")
	table("Stashed MCPs", s.Stashed, "none")

	fmt.Fprintf(w, "## Summary\n\n")
	fmt.Fprintf(w, "| Metric | Value |\n")
	fmt.Fprintf(w, "|--------|-------|\n")
	fmt.Fprintf(w, "| Plugins | %d / %d enabled |\n", s.PluginsActive, s.PluginsTotal)
	fmt.Fprintf(w, "| Skills | %d / %d enabled |\n", s.SkillsEnabled, s.SkillsTotal)
	fmt.Fprintf(w, "| Agents | %d |\n", s.AgentsTotal)
	fmt.Fprintf(w, "| Commands | %d |\n", s.CommandsTotal)
	fmt.Fprintf(w, "| Command conflicts | %d |\n", len(s.Conflicts))
	fmt.Fprintln(w)

	if len(s.Conflicts) > 0 {
		fmt.Fprintf(w, "## Command Conflicts\n\n")
		fmt.Fprintf(w, "| Effective | Kind | Participants |\n")
		fmt.Fprintf(w, "|-----------|------|-------------|\n")
		for _, c := range s.Conflicts {
			fmt.Fprintf(w, "| `/%s` | %s | %s |\n", c.Effective, c.Kind, participantNames(c.Participants))
		}
		fmt.Fprintln(w)
	}
	return nil
}

// WriteSnapshotCSV renders a Snapshot as CSV (flat key=value rows).
func WriteSnapshotCSV(s Snapshot, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"category", "name", "detail"})

	emit := func(cat string, items []string) {
		for _, n := range items {
			_ = cw.Write([]string{cat, n, ""})
		}
	}
	emit("user_mcp", s.UserMCPs)
	emit("local_mcp", s.LocalMCPs)
	emit("plugin_mcp", s.PluginSourced)
	emit("mcpjson", s.McpjsonShared)
	emit("claudeai", s.ClaudeAi)
	emit("override", s.Overrides)
	emit("stash", s.Stashed)

	for _, c := range s.Conflicts {
		_ = cw.Write([]string{"conflict", c.Effective, fmt.Sprintf("%s|%s", c.Kind, participantNames(c.Participants))})
	}
	return cw.Error()
}

// WriteSweepMD renders a SweepReport as a Markdown table.
func WriteSweepMD(sr SweepReport, w io.Writer) error {
	fmt.Fprintf(w, "# ccmcp Sweep — %s\n\n", sr.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintln(w, "| Project | User MCPs | Local MCPs | Overrides | Plugins | Skills | Agents | Cmds | Conflicts |")
	fmt.Fprintln(w, "|---------|-----------|------------|-----------|---------|--------|--------|------|-----------|")
	for _, r := range sr.Rows {
		fmt.Fprintf(w, "| `%s` | %d | %d | %d | %d/%d | %d/%d | %d | %d | %d |\n",
			r.ProjectPath,
			r.UserMCPs, r.LocalMCPs, r.Overrides,
			r.PluginsActive, r.PluginsTotal,
			r.SkillsEnabled, r.SkillsTotal,
			r.AgentsTotal, r.CommandsTotal, r.Conflicts)
	}
	fmt.Fprintln(w)
	return nil
}

// WriteSweepCSV renders a SweepReport as CSV.
func WriteSweepCSV(sr SweepReport, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"projectPath", "userMcps", "localMcps", "overrides", "pluginsActive", "pluginsTotal", "skillsEnabled", "skillsTotal", "agentsTotal", "commandsTotal", "conflicts"})
	for _, r := range sr.Rows {
		_ = cw.Write([]string{
			r.ProjectPath,
			fmt.Sprint(r.UserMCPs), fmt.Sprint(r.LocalMCPs), fmt.Sprint(r.Overrides),
			fmt.Sprint(r.PluginsActive), fmt.Sprint(r.PluginsTotal),
			fmt.Sprint(r.SkillsEnabled), fmt.Sprint(r.SkillsTotal),
			fmt.Sprint(r.AgentsTotal), fmt.Sprint(r.CommandsTotal),
			fmt.Sprint(r.Conflicts),
		})
	}
	return cw.Error()
}

// WriteDriftMD renders a DriftReport as Markdown.
func WriteDriftMD(d DriftReport, w io.Writer) error {
	fmt.Fprintf(w, "# ccmcp Drift — %s\n\n", d.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "**Project:** `%s`  \n", d.ProjectPath)
	fmt.Fprintf(w, "**Baseline snapshot:** %s\n\n", d.Before.GeneratedAt.Format(time.RFC3339))

	diffSection := func(title string, added, removed []string) {
		if len(added) == 0 && len(removed) == 0 {
			return
		}
		fmt.Fprintf(w, "## %s\n\n", title)
		for _, n := range added {
			fmt.Fprintf(w, "+ `%s`\n", n)
		}
		for _, n := range removed {
			fmt.Fprintf(w, "- `%s`\n", n)
		}
		fmt.Fprintln(w)
	}

	addedMCPs := make([]string, 0, len(d.MCPsAdded))
	removedMCPs := make([]string, 0, len(d.MCPsRemoved))
	for _, di := range d.MCPsAdded {
		addedMCPs = append(addedMCPs, fmt.Sprintf("%s [%s]", di.Name, di.Scope))
	}
	for _, di := range d.MCPsRemoved {
		removedMCPs = append(removedMCPs, fmt.Sprintf("%s [%s]", di.Name, di.Scope))
	}
	diffSection("MCPs changed", addedMCPs, removedMCPs)
	diffSection("Override changes", d.OverridesAdded, d.OverridesRemoved)

	if len(d.ConflictsAdded) > 0 {
		fmt.Fprintf(w, "## New command conflicts\n\n")
		for _, c := range d.ConflictsAdded {
			fmt.Fprintf(w, "- `/%s` (%s)\n", c.Effective, c.Kind)
		}
		fmt.Fprintln(w)
	}
	if len(d.ConflictsRemoved) > 0 {
		fmt.Fprintf(w, "## Resolved command conflicts\n\n")
		for _, c := range d.ConflictsRemoved {
			fmt.Fprintf(w, "- `/%s` (%s)\n", c.Effective, c.Kind)
		}
		fmt.Fprintln(w)
	}

	noChanges := len(d.MCPsAdded)+len(d.MCPsRemoved)+
		len(d.OverridesAdded)+len(d.OverridesRemoved)+
		len(d.ConflictsAdded)+len(d.ConflictsRemoved) == 0
	if noChanges {
		fmt.Fprintln(w, "*No changes detected since baseline.*")
	}
	return nil
}

// WriteAuditMD renders an AuditReport as Markdown.
func WriteAuditMD(a AuditReport, w io.Writer) error {
	fmt.Fprintf(w, "# ccmcp Audit — %s\n\n", a.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "**Project:** `%s`\n\n", a.ProjectPath)

	if len(a.StaleOverrides) == 0 && len(a.Conflicts) == 0 && len(a.Redundancies) == 0 {
		fmt.Fprintln(w, "*No issues found.*")
		return nil
	}

	if len(a.StaleOverrides) > 0 {
		fmt.Fprintf(w, "## Stale overrides (%d)\n\n", len(a.StaleOverrides))
		fmt.Fprintln(w, "Entries in `disabledMcpServers` that no longer match any live source:")
		fmt.Fprintln(w, "| Key | Kind | Reason |")
		fmt.Fprintln(w, "|-----|------|--------|")
		for _, it := range a.StaleOverrides {
			fmt.Fprintf(w, "| `%s` | %s | %s |\n", it.Key, it.Kind, it.Reason)
		}
		fmt.Fprintln(w)
	}

	if len(a.Conflicts) > 0 {
		fmt.Fprintf(w, "## Command conflicts (%d)\n\n", len(a.Conflicts))
		fmt.Fprintln(w, "| Effective | Kind | Participants |")
		fmt.Fprintln(w, "|-----------|------|-------------|")
		for _, c := range a.Conflicts {
			fmt.Fprintf(w, "| `/%s` | %s | %s |\n", c.Effective, c.Kind, participantNames(c.Participants))
		}
		fmt.Fprintln(w)
	}

	if len(a.Redundancies) > 0 {
		fmt.Fprintf(w, "## Redundant MCPs (%d)\n\n", len(a.Redundancies))
		fmt.Fprintln(w, "MCPs appearing in multiple scopes at once:")
		fmt.Fprintln(w, "| Name | Scopes |")
		fmt.Fprintln(w, "|------|--------|")
		for _, r := range a.Redundancies {
			fmt.Fprintf(w, "| `%s` | %s |\n", r.Name, strings.Join(r.Scopes, ", "))
		}
		fmt.Fprintln(w)
	}
	return nil
}

// WriteAuditCSV renders an AuditReport as CSV.
func WriteAuditCSV(a AuditReport, w io.Writer) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"issue_type", "name", "kind", "detail"})
	for _, it := range a.StaleOverrides {
		_ = cw.Write([]string{"stale_override", it.Key, it.Kind, it.Reason})
	}
	for _, c := range a.Conflicts {
		_ = cw.Write([]string{"command_conflict", c.Effective, string(c.Kind), participantNames(c.Participants)})
	}
	for _, r := range a.Redundancies {
		_ = cw.Write([]string{"redundancy", r.Name, "", strings.Join(r.Scopes, "|")})
	}
	return cw.Error()
}

// diffStrings returns (added, removed) between old and new string slices.
func DiffStrings(old, neu []string) (added, removed []string) {
	oldSet := make(map[string]bool, len(old))
	newSet := make(map[string]bool, len(neu))
	for _, s := range old {
		oldSet[s] = true
	}
	for _, s := range neu {
		newSet[s] = true
	}
	for _, s := range neu {
		if !oldSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range old {
		if !newSet[s] {
			removed = append(removed, s)
		}
	}
	return
}

// DiffConflicts returns (added, removed) between two conflict lists.
func DiffConflicts(old, neu []commands.Conflict) (added, removed []commands.Conflict) {
	key := func(c commands.Conflict) string { return string(c.Kind) + ":" + c.Effective }
	oldSet := map[string]bool{}
	newSet := map[string]bool{}
	for _, c := range old {
		oldSet[key(c)] = true
	}
	for _, c := range neu {
		newSet[key(c)] = true
	}
	for _, c := range neu {
		if !oldSet[key(c)] {
			added = append(added, c)
		}
	}
	for _, c := range old {
		if !newSet[key(c)] {
			removed = append(removed, c)
		}
	}
	return
}
