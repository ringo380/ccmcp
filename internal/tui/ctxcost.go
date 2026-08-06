package tui

import (
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
// nil-check; an empty index renders as unmeasured, not as zero.
func (s *state) costIndex() *ctxcost.Index {
	if s.cost != nil {
		return s.cost
	}
	in := ctxcost.Input{
		Skills:   skills.Discover(s.paths.ClaudeConfigDir, s.project, s.settings, s.installed, s.paths.PluginsDir),
		Agents:   agents.Discover(s.paths.ClaudeConfigDir, s.project, s.settings, s.installed, s.paths.PluginsDir),
		Commands: commands.Discover(s.paths.ClaudeConfigDir, s.project, s.settings, s.installed, s.paths.PluginsDir),
	}
	idx, err := ctxcost.Build(in)
	if err != nil || idx == nil {
		idx = &ctxcost.Index{
			ByPlugin: map[string]ctxcost.Breakdown{},
			ByMCP:    map[string]ctxcost.Breakdown{},
		}
	}
	s.cost = idx
	return s.cost
}

// invalidateCost drops the cached estimate so the next costIndex call rebuilds.
func (s *state) invalidateCost() { s.cost = nil }

// measured returns the last real prompt prefix for this project, if any.
func (s *state) measured() (ctxcost.Measured, bool) {
	return ctxcost.Calibrate(s.paths.ClaudeConfigDir, s.project)
}
