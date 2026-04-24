package skills

import (
	"fmt"
	"os"
	"path/filepath"
)

// rootForScope returns the skills/ directory for a given scope.
// claudeConfigDir is typically ~/.claude; projectDir is the project root or "".
func rootForScope(scope Scope, claudeConfigDir, projectDir string) (string, error) {
	switch scope {
	case ScopeUser:
		if claudeConfigDir == "" {
			return "", fmt.Errorf("user scope requires ~/.claude path")
		}
		return filepath.Join(claudeConfigDir, "skills"), nil
	case ScopeProject:
		if projectDir == "" {
			return "", fmt.Errorf("project scope requires a project path")
		}
		return filepath.Join(projectDir, ".claude", "skills"), nil
	default:
		return "", fmt.Errorf("cannot write to %s scope", scope)
	}
}

// Scaffold writes a new SKILL.md under <scope>/skills/<name>/SKILL.md.
// Returns the absolute path of the created file. Errors if the skill dir already exists.
func Scaffold(name, description string, scope Scope, claudeConfigDir, projectDir string) (string, error) {
	root, err := rootForScope(scope, claudeConfigDir, projectDir)
	if err != nil {
		return "", err
	}
	skillDir := filepath.Join(root, name)
	if _, err := os.Stat(skillDir); err == nil {
		return "", fmt.Errorf("skill %q already exists at %s", name, skillDir)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(skillDir, "SKILL.md")
	body := fmt.Sprintf(`---
name: %s
description: %q
---

# %s

Describe what this skill does and when it should trigger.
`, name, description, name)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		return "", err
	}
	return file, nil
}

// Remove deletes a user- or project-scope skill directory. Refuses plugin-scope.
// Returns the path that was removed and whether something existed.
func Remove(name string, scope Scope, claudeConfigDir, projectDir string) (string, bool, error) {
	root, err := rootForScope(scope, claudeConfigDir, projectDir)
	if err != nil {
		return "", false, err
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err != nil {
		return dir, false, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return dir, false, err
	}
	return dir, true, nil
}

// Move relocates a skill between user and project scopes. Refuses overwriting an existing dest.
func Move(name string, from, to Scope, claudeConfigDir, projectDir string) (src, dst string, err error) {
	srcRoot, err := rootForScope(from, claudeConfigDir, projectDir)
	if err != nil {
		return "", "", err
	}
	dstRoot, err := rootForScope(to, claudeConfigDir, projectDir)
	if err != nil {
		return "", "", err
	}
	src = filepath.Join(srcRoot, name)
	dst = filepath.Join(dstRoot, name)
	if _, err := os.Stat(src); err != nil {
		return src, dst, fmt.Errorf("source skill not found: %s", src)
	}
	if _, err := os.Stat(dst); err == nil {
		return src, dst, fmt.Errorf("destination already exists: %s", dst)
	}
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return src, dst, err
	}
	return src, dst, os.Rename(src, dst)
}
