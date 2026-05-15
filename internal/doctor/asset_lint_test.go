package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/skills"
)

func writeSkill(t *testing.T, dir, name, desc string, extraFrontmatter ...string) skills.Skill {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n"
	for _, line := range extraFrontmatter {
		body += line + "\n"
	}
	body += "---\n# body\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return skills.Skill{Name: name, Description: desc, Dir: skillDir}
}

func TestLintSkillsNameValid(t *testing.T) {
	tmp := t.TempDir()
	s := writeSkill(t, tmp, "valid-skill-123", "short description")
	issues := LintSkills([]skills.Skill{s})
	for _, iss := range issues {
		if iss.Code == "SKILL001" || iss.Code == "SKILL002" {
			t.Errorf("valid skill name unexpectedly flagged: %s", iss)
		}
	}
}

func TestLintSkillsNameInvalidChars(t *testing.T) {
	tmp := t.TempDir()
	s := writeSkill(t, tmp, "Invalid_Skill", "short")
	issues := LintSkills([]skills.Skill{s})
	var found bool
	for _, iss := range issues {
		if iss.Code == "SKILL001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SKILL001 for invalid skill name; got %v", issues)
	}
}

func TestLintSkillsNameTooLong(t *testing.T) {
	tmp := t.TempDir()
	long := strings.Repeat("a", 65)
	s := writeSkill(t, tmp, long, "short")
	issues := LintSkills([]skills.Skill{s})
	var found bool
	for _, iss := range issues {
		if iss.Code == "SKILL002" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SKILL002 for 65-char skill name; got %v", issues)
	}
}

func TestLintSkillsDescriptionTooLong(t *testing.T) {
	tmp := t.TempDir()
	long := strings.Repeat("x", 1600)
	s := writeSkill(t, tmp, "good-name", long)
	issues := LintSkills([]skills.Skill{s})
	var errFound bool
	for _, iss := range issues {
		if iss.Code == "SKILL003" && iss.Severity == SeverityError {
			errFound = true
		}
	}
	if !errFound {
		t.Errorf("expected SKILL003 error for 1600-char description; got %v", issues)
	}
}

func TestLintSkillsDescriptionWarn(t *testing.T) {
	tmp := t.TempDir()
	medium := strings.Repeat("x", 1300)
	s := writeSkill(t, tmp, "good-name", medium)
	issues := LintSkills([]skills.Skill{s})
	var warnFound bool
	for _, iss := range issues {
		if iss.Code == "SKILL003" && iss.Severity == SeverityWarning {
			warnFound = true
		}
	}
	if !warnFound {
		t.Errorf("expected SKILL003 warning for 1300-char description; got %v", issues)
	}
}

func TestLintAgentsDescriptionTooLong(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "agent.md")
	long := strings.Repeat("y", 1700)
	body := "---\nname: agent\ndescription: " + long + "\n---\n# body\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ag := agents.Agent{Name: "agent", Description: long, File: file}
	issues := LintAgents([]agents.Agent{ag})
	var found bool
	for _, iss := range issues {
		if iss.Code == "AGENT001" && iss.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AGENT001 error; got %v", issues)
	}
}

func TestLintAgentsBodyOverBudget(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "fat-agent.md")
	// Each "lorem ipsum " is 2 tokens in cl100k_base, so 8000 repeats
	// ≈ 16k tokens — comfortably over the 15k error threshold.
	body := "---\nname: fat\ndescription: short\n---\n" + strings.Repeat("lorem ipsum ", 8000)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ag := agents.Agent{Name: "fat", Description: "short", File: file}
	issues := LintAgents([]agents.Agent{ag})
	var errFound bool
	for _, iss := range issues {
		if iss.Code == "AGENT002" && iss.Severity == SeverityError {
			errFound = true
		}
	}
	if !errFound {
		t.Errorf("expected AGENT002 error for overbudget body; got %v", issues)
	}
}

func TestLintAgentsBodyUnderBudget(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "thin-agent.md")
	body := "---\nname: thin\ndescription: short\n---\n# body\nstays well under the 13k warn threshold.\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ag := agents.Agent{Name: "thin", Description: "short", File: file}
	issues := LintAgents([]agents.Agent{ag})
	for _, iss := range issues {
		if iss.Code == "AGENT002" {
			t.Errorf("did not expect AGENT002 for thin body; got %v", iss)
		}
	}
}

func TestLintCommandsDescription(t *testing.T) {
	long := strings.Repeat("z", 600)
	cmd := commands.Command{Slug: "x", Description: long, File: "/tmp/cmd.md"}
	issues := LintCommands([]commands.Command{cmd})
	var found bool
	for _, iss := range issues {
		if iss.Code == "CMD001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CMD001 for 600-char command description; got %v", issues)
	}
}

func TestLintPluginManifest(t *testing.T) {
	long := strings.Repeat("p", 600)
	issues := LintPluginManifest("/tmp/plugin.json", long)
	if len(issues) != 1 || issues[0].Code != "PLUGIN001" {
		t.Errorf("expected one PLUGIN001 issue; got %v", issues)
	}
}
