package ctxcost

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Measured is a real prompt-prefix size read out of a session transcript, used
// to sanity-check the estimate rather than asking the user to trust it.
type Measured struct {
	// PrefixTokens is cache_creation + cache_read on the FIRST usage record of
	// the newest transcript, i.e. the prefix as it stood at session start. Later
	// records fold in the whole conversation so far, so they no longer describe
	// the static per-turn prefix. That figure is the whole cached prefix -
	// system prompt, tool definitions, skill/agent listings, CLAUDE.md, memory -
	// so it is an upper bound on what ctxcost attributes, not a like-for-like
	// comparison.
	PrefixTokens int `json:"prefixTokens"`
	// When is the transcript file's mtime: when the session was last active, NOT
	// when PrefixTokens was measured. Callers must not label PrefixTokens "last
	// measured" - it is the session-start figure for the most recent session.
	When      time.Time `json:"when"`
	SessionID string    `json:"sessionId"`
}

// TranscriptSlug converts a project path into the directory name Claude Code
// uses under <claudeConfigDir>/projects: every character outside [A-Za-z0-9]
// becomes "-", with no run collapsing.
//
// Derived from the 124 live directories under ~/.claude/projects, not assumed:
// "/Users/ryanrobson/git/clubdeck.github.io" is stored as
// "-Users-ryanrobson-git-clubdeck-github-io" and "/Users/ryanrobson/.claude" as
// "-Users-ryanrobson--claude" (adjacent separators are kept, so "/." yields
// "--"). Only directories last written before ~2026-04 still preserve "." and
// "_"; replacing just "/" left calibration silently dead for every project path
// containing a dot, underscore, or space.
func TranscriptSlug(projectDir string) string {
	var b strings.Builder
	b.Grow(len(projectDir))
	for _, r := range projectDir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// usageRecord is the subset of a transcript line ctxcost reads.
type usageRecord struct {
	Message struct {
		Usage struct {
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Calibrate returns the measured prompt prefix from the newest transcript for
// projectDir. ok is false when there is no transcript, no readable usage
// record, or the total is zero - callers must omit the line rather than render
// a fabricated number.
func Calibrate(claudeConfigDir, projectDir string) (Measured, bool) {
	dir := filepath.Join(claudeConfigDir, "projects", TranscriptSlug(projectDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Measured{}, false
	}

	// Newest .jsonl by mtime.
	var best string
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestMod) {
			best, bestMod = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	if best == "" {
		return Measured{}, false
	}

	f, err := os.Open(best)
	if err != nil {
		return Measured{}, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Transcript lines carry whole tool results and routinely exceed the 64KB
	// default token size; without this the scanner stops at the first long line.
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"usage"`) {
			continue
		}
		var rec usageRecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		n := rec.Message.Usage.CacheCreationInputTokens + rec.Message.Usage.CacheReadInputTokens
		if n <= 0 {
			continue
		}
		return Measured{
			PrefixTokens: n,
			When:         bestMod,
			SessionID:    strings.TrimSuffix(filepath.Base(best), ".jsonl"),
		}, true
	}
	return Measured{}, false
}
