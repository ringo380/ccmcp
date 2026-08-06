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
	// PrefixTokens is cache_creation + cache_read on the first usage record of
	// the newest transcript. That is the whole cached prefix - system prompt,
	// tool definitions, skill/agent listings, CLAUDE.md, memory - so it is an
	// upper bound on what ctxcost attributes, not a like-for-like comparison.
	PrefixTokens int       `json:"prefixTokens"`
	When         time.Time `json:"when"`
	SessionID    string    `json:"sessionId"`
}

// TranscriptSlug converts a project path into the directory name Claude Code
// uses under <claudeConfigDir>/projects: every "/" becomes "-".
func TranscriptSlug(projectDir string) string {
	return strings.ReplaceAll(projectDir, "/", "-")
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
