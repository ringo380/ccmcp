// Package doctor provides structural linting for CLAUDE.md and MEMORY.md files.
package doctor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Severity indicates how serious a lint finding is.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Issue is one finding from the linter.
type Issue struct {
	File     string
	Line     int // 0 = file-level, not line-specific
	Severity Severity
	Code     string
	Message  string
}

func (i Issue) String() string {
	loc := i.File
	if i.Line > 0 {
		loc = fmt.Sprintf("%s:%d", i.File, i.Line)
	}
	return fmt.Sprintf("[%s] %s  %s  %s", i.Severity, i.Code, loc, i.Message)
}

// LintClaudeMD runs structural checks on a CLAUDE.md file.
// dir is the directory containing the file (used to resolve relative links).
func LintClaudeMD(path string) []Issue {
	var issues []Issue

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return []Issue{{File: path, Severity: SeverityError, Code: "MD001", Message: "file not found"}}
	}
	if err != nil {
		return []Issue{{File: path, Severity: SeverityError, Code: "MD001", Message: err.Error()}}
	}
	if info.Size() == 0 {
		return []Issue{{File: path, Severity: SeverityError, Code: "MD002", Message: "file is empty"}}
	}

	dir := filepath.Dir(path)
	lines, err := readLines(path)
	if err != nil {
		return []Issue{{File: path, Severity: SeverityError, Code: "MD001", Message: "cannot read: " + err.Error()}}
	}

	if len(lines) > 500 {
		issues = append(issues, Issue{
			File:     path,
			Severity: SeverityInfo,
			Code:     "MD005",
			Message:  fmt.Sprintf("file is %d lines — consider trimming to reduce context load", len(lines)),
		})
	}

	linkRe := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

	for i, line := range lines {
		lineNum := i + 1

		if len(line) > 200 {
			issues = append(issues, Issue{
				File:     path,
				Line:     lineNum,
				Severity: SeverityWarning,
				Code:     "MD003",
				Message:  fmt.Sprintf("line length %d exceeds 200 characters", len(line)),
			})
		}

		// Check markdown links that look like local paths (not URLs, not anchors)
		for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
			target := m[2]
			if strings.HasPrefix(target, "http") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			// Strip fragment
			if idx := strings.Index(target, "#"); idx >= 0 {
				target = target[:idx]
			}
			if target == "" {
				continue
			}
			abs := filepath.Join(dir, target)
			if _, err := os.Stat(abs); os.IsNotExist(err) {
				issues = append(issues, Issue{
					File:     path,
					Line:     lineNum,
					Severity: SeverityWarning,
					Code:     "MD004",
					Message:  fmt.Sprintf("linked file not found: %s", target),
				})
			}
		}
	}

	return issues
}

// LintMemoryIndex checks a MEMORY.md index file and the individual memory files it points to.
// memDir is the directory that contains both MEMORY.md and the individual *.md memory files.
func LintMemoryIndex(memDir string) []Issue {
	indexPath := filepath.Join(memDir, "MEMORY.md")
	var issues []Issue

	info, err := os.Stat(indexPath)
	if os.IsNotExist(err) {
		return []Issue{{File: indexPath, Severity: SeverityInfo, Code: "MEM001", Message: "MEMORY.md not found (memory not yet initialised for this project)"}}
	}
	if err != nil || info.Size() == 0 {
		return []Issue{{File: indexPath, Severity: SeverityWarning, Code: "MEM001", Message: "MEMORY.md is empty or unreadable"}}
	}

	lines, _ := readLines(indexPath)
	// Pattern: - [Title](file.md) — description
	entryRe := regexp.MustCompile(`^\s*-\s+\[([^\]]+)\]\(([^)]+)\)`)

	for i, line := range lines {
		lineNum := i + 1

		if len(line) > 150 {
			issues = append(issues, Issue{
				File:     indexPath,
				Line:     lineNum,
				Severity: SeverityWarning,
				Code:     "MEM003",
				Message:  fmt.Sprintf("index entry is %d chars (recommended ≤150)", len(line)),
			})
		}

		m := entryRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		target := m[2]
		abs := filepath.Join(memDir, target)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			issues = append(issues, Issue{
				File:     indexPath,
				Line:     lineNum,
				Severity: SeverityWarning,
				Code:     "MEM002",
				Message:  fmt.Sprintf("memory file not found: %s", target),
			})
			continue
		}
		// Lint the individual file too
		issues = append(issues, lintMemoryFile(abs)...)
	}

	return issues
}

// lintMemoryFile checks frontmatter of a single memory file.
func lintMemoryFile(path string) []Issue {
	var issues []Issue
	lines, err := readLines(path)
	if err != nil {
		return []Issue{{File: path, Severity: SeverityError, Code: "MEM004", Message: "cannot read: " + err.Error()}}
	}

	// Expect --- frontmatter block at top
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return []Issue{{File: path, Severity: SeverityError, Code: "MEM004", Message: "missing frontmatter (expected --- at line 1)"}}
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return []Issue{{File: path, Severity: SeverityError, Code: "MEM004", Message: "frontmatter block not closed (missing closing ---)"}}
	}

	fm := map[string]string{}
	for _, l := range lines[1:end] {
		if idx := strings.Index(l, ":"); idx > 0 {
			k := strings.TrimSpace(l[:idx])
			v := strings.TrimSpace(l[idx+1:])
			fm[k] = v
		}
	}

	validTypes := map[string]bool{"user": true, "feedback": true, "project": true, "reference": true}
	required := []string{"name", "description", "type"}
	for _, field := range required {
		if fm[field] == "" {
			issues = append(issues, Issue{
				File:     path,
				Severity: SeverityError,
				Code:     "MEM005",
				Message:  fmt.Sprintf("frontmatter missing required field: %q", field),
			})
		}
	}
	if t := fm["type"]; t != "" && !validTypes[t] {
		issues = append(issues, Issue{
			File:     path,
			Severity: SeverityError,
			Code:     "MEM006",
			Message:  fmt.Sprintf("invalid type %q — must be one of: user, feedback, project, reference", t),
		})
	}

	return issues
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}
