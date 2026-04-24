// Package assets contains shared primitives for parsing Claude Code asset files
// (skills, agents, commands) that use YAML-ish frontmatter.
package assets

import (
	"bufio"
	"os"
	"strings"
)

// Frontmatter holds the subset of YAML frontmatter fields ccmcp needs.
// We parse by hand to avoid pulling in a YAML dep for such a narrow surface —
// only top-level scalar keys are read; nested structures (triggers:, etc.)
// are ignored.
type Frontmatter struct {
	Name        string
	Description string
	Model       string
	// Raw holds every top-level scalar key verbatim for callers that need more.
	Raw map[string]string
}

// ReadFrontmatter opens path, scans for an opening "---" line, collects lines
// up to the closing "---", and extracts top-level scalar keys. Returns a
// zero-value Frontmatter (with Raw=nil) if the file has no frontmatter block.
func ReadFrontmatter(path string) (Frontmatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return Frontmatter{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)

	// First non-empty line must be "---" for a frontmatter block to exist.
	var inBlock bool
	fm := Frontmatter{Raw: map[string]string{}}
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if !inBlock {
			if trim == "" {
				continue
			}
			if trim != "---" {
				return Frontmatter{}, nil
			}
			inBlock = true
			continue
		}
		if trim == "---" {
			break
		}
		// Skip list continuations and nested mappings (leading whitespace).
		if line != "" && (line[0] == ' ' || line[0] == '\t' || line[0] == '-') {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.TrimPrefix(val, "\"")
		val = strings.TrimSuffix(val, "\"")
		val = strings.TrimPrefix(val, "'")
		val = strings.TrimSuffix(val, "'")
		if val == "" {
			// Block scalar or nested map starts on next line; we don't follow.
			continue
		}
		fm.Raw[key] = val
		switch key {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		case "model":
			fm.Model = val
		}
	}
	if err := sc.Err(); err != nil {
		return Frontmatter{}, err
	}
	return fm, nil
}

// Truncate returns s shortened to n runes with a trailing ellipsis if it was cut.
// Useful for rendering description snippets in list output.
func Truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
