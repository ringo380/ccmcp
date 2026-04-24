package agents

import (
	"fmt"
	"os"
	"path/filepath"
)

func rootForScope(scope Scope, claudeConfigDir, projectDir string) (string, error) {
	switch scope {
	case ScopeUser:
		if claudeConfigDir == "" {
			return "", fmt.Errorf("user scope requires ~/.claude path")
		}
		return filepath.Join(claudeConfigDir, "agents"), nil
	case ScopeProject:
		if projectDir == "" {
			return "", fmt.Errorf("project scope requires a project path")
		}
		return filepath.Join(projectDir, ".claude", "agents"), nil
	default:
		return "", fmt.Errorf("cannot write to %s scope", scope)
	}
}

// Scaffold writes a new agent .md file under <scope>/agents/<name>.md.
func Scaffold(name, description, model string, scope Scope, claudeConfigDir, projectDir string) (string, error) {
	root, err := rootForScope(scope, claudeConfigDir, projectDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(root, name+".md")
	if _, err := os.Stat(file); err == nil {
		return "", fmt.Errorf("agent %q already exists at %s", name, file)
	}
	if model == "" {
		model = "sonnet"
	}
	body := fmt.Sprintf(`---
name: %s
description: %q
model: %s
---

# %s

You are %s. Describe the agent's responsibility, tone, and behavior here.
`, name, description, model, name, name)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		return "", err
	}
	return file, nil
}

func Remove(name string, scope Scope, claudeConfigDir, projectDir string) (string, bool, error) {
	root, err := rootForScope(scope, claudeConfigDir, projectDir)
	if err != nil {
		return "", false, err
	}
	file := filepath.Join(root, name+".md")
	if _, err := os.Stat(file); err != nil {
		return file, false, nil
	}
	return file, true, os.Remove(file)
}

func Move(name string, from, to Scope, claudeConfigDir, projectDir string) (src, dst string, err error) {
	srcRoot, err := rootForScope(from, claudeConfigDir, projectDir)
	if err != nil {
		return "", "", err
	}
	dstRoot, err := rootForScope(to, claudeConfigDir, projectDir)
	if err != nil {
		return "", "", err
	}
	src = filepath.Join(srcRoot, name+".md")
	dst = filepath.Join(dstRoot, name+".md")
	if _, err := os.Stat(src); err != nil {
		return src, dst, fmt.Errorf("source agent not found: %s", src)
	}
	if _, err := os.Stat(dst); err == nil {
		return src, dst, fmt.Errorf("destination already exists: %s", dst)
	}
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return src, dst, err
	}
	return src, dst, os.Rename(src, dst)
}
