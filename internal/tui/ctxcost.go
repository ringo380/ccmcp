package tui

import (
	"errors"

	"github.com/charmbracelet/lipgloss"
	"github.com/ringo380/ccmcp/internal/agents"
	"github.com/ringo380/ccmcp/internal/commands"
	"github.com/ringo380/ccmcp/internal/ctxcost"
	"github.com/ringo380/ccmcp/internal/skills"
)

// costIndex returns the cached per-turn context estimate, building it on first
// use. Each Discover walk is a disk scan, so this must be reached from
// rebuild() (or a render path), never recomputed per frame.
//
// A build error yields an empty index rather than nil, so callers never have to
// nil-check. An empty index is NOT self-describing: every figure in it is 0,
// which Human renders as "≈0" - a confident measurement of zero. Callers must
// therefore consult costUnavailable() before rendering any figure from it.
func (s *state) costIndex() *ctxcost.Index {
	if s.cost != nil && s.costDirtySettings == s.dirtySettings && s.costDirtyPlugins == s.dirtyPlugins {
		return s.cost
	}
	in := ctxcost.Input{
		Skills:   skills.Discover(s.paths.ClaudeConfigDir, s.project, s.settings, s.installed, s.paths.PluginsDir),
		Agents:   agents.Discover(s.paths.ClaudeConfigDir, s.project, s.settings, s.installed, s.paths.PluginsDir),
		Commands: commands.Discover(s.paths.ClaudeConfigDir, s.project, s.settings, s.installed, s.paths.PluginsDir),
		PluginEnabled: func(id string) bool {
			if s.settings == nil {
				return true
			}
			en, known := s.settings.PluginEnabled(id)
			return known && en
		},
	}
	idx, err := ctxcost.Build(in)
	s.costErr = err
	if err != nil || idx == nil {
		if err == nil {
			s.costErr = errors.New("no index built")
		}
		idx = &ctxcost.Index{
			ByPlugin: map[string]ctxcost.Breakdown{},
			ByMCP:    map[string]ctxcost.Breakdown{},
		}
	}
	s.cost = idx
	s.costDirtySettings = s.dirtySettings
	s.costDirtyPlugins = s.dirtyPlugins
	return s.cost
}

// costUnavailable returns a short reason when the last costIndex build failed,
// or "" when the index is trustworthy. Every figure in a failed build is 0, so
// a caller that skips this check renders "≈0" - a measurement - where there is
// no measurement at all.
//
// This is a reachable state, not a theoretical one: the token encoder downloads
// its BPE table on first use and caches it under $TMPDIR, which macOS purges,
// so "offline with a cold cache" fails the build.
func (s *state) costUnavailable() string {
	s.costIndex() // ensure the cached error reflects the current dirty state
	if s.costErr == nil {
		return ""
	}
	return truncateRunes(s.costErr.Error(), 60)
}

// invalidateCost drops the cached estimate so the next costIndex call rebuilds.
func (s *state) invalidateCost() { s.cost = nil }

// measured returns the session-start prompt prefix for this project's newest
// transcript, if any.
func (s *state) measured() (ctxcost.Measured, bool) {
	return ctxcost.Calibrate(s.paths.ClaudeConfigDir, s.project)
}

// truncateRunes shortens s to at most n runes, appending an ellipsis. Unlike
// the byte-based truncate in mcps.go this never splits a multi-byte rune, which
// matters because every context figure carries a multi-byte "≈".
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// fitWidth clamps s to at most w display columns.
//
// Header lines are budgeted in LOGICAL lines (strings.Count(header, "\n")), and
// model.View()'s overflow guard counts logical lines too, so neither notices a
// line that wraps. A wrapped header silently costs the body an extra physical
// row and scrolls itself off the top of a narrow terminal - which is the normal
// case under screen magnification. Clamping keeps physical rows == logical rows.
func fitWidth(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)+"…") > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
