package config

import (
	"fmt"
	"sort"
)

// Settings wraps ~/.claude/settings.json.
// enabledPlugins has a quirky shape — it's an object like {"id@marketplace": bool}, where
// `false` means installed-but-disabled. We preserve that semantic.
type Settings struct {
	Path string
	Raw  map[string]any
}

func LoadSettings(path string) (*Settings, error) {
	raw, err := RawJSON(path)
	if err != nil {
		return nil, err
	}
	return &Settings{Path: path, Raw: raw}, nil
}

func (s *Settings) Save() error {
	return WriteJSON(s.Path, s.Raw)
}

// --- enabledPlugins ---------------------------------------------------------

type PluginEntry struct {
	ID      string // e.g. "feature-dev@claude-plugins-official"
	Enabled bool
}

func (s *Settings) PluginEntries() []PluginEntry {
	m, _ := s.Raw["enabledPlugins"].(map[string]any)
	out := make([]PluginEntry, 0, len(m))
	for id, v := range m {
		b, _ := v.(bool)
		out = append(out, PluginEntry{ID: id, Enabled: b})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Settings) PluginEnabled(id string) (enabled, known bool) {
	m, _ := s.Raw["enabledPlugins"].(map[string]any)
	if m == nil {
		return false, false
	}
	v, ok := m[id]
	if !ok {
		return false, false
	}
	b, _ := v.(bool)
	return b, true
}

func (s *Settings) SetPluginEnabled(id string, enabled bool) {
	m, _ := s.Raw["enabledPlugins"].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	m[id] = enabled
	s.Raw["enabledPlugins"] = m
}

// RemovePluginEntry removes the id from enabledPlugins entirely (used on uninstall).
func (s *Settings) RemovePluginEntry(id string) bool {
	m, ok := s.Raw["enabledPlugins"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := m[id]; !exists {
		return false
	}
	delete(m, id)
	s.Raw["enabledPlugins"] = m
	return true
}

// --- marketplaces -----------------------------------------------------------

type Marketplace struct {
	Name       string
	SourceType string // github | git | local
	Repo       string // for github/git
	Path       string // for local
	AutoUpdate bool
}

func (s *Settings) ExtraMarketplaces() []Marketplace {
	m, _ := s.Raw["extraKnownMarketplaces"].(map[string]any)
	out := make([]Marketplace, 0, len(m))
	for name, v := range m {
		entry, _ := v.(map[string]any)
		src, _ := entry["source"].(map[string]any)
		mp := Marketplace{Name: name}
		if b, ok := entry["autoUpdate"].(bool); ok {
			mp.AutoUpdate = b
		}
		if src != nil {
			mp.SourceType, _ = src["source"].(string)
			mp.Repo, _ = src["repo"].(string)
			mp.Path, _ = src["path"].(string)
		}
		out = append(out, mp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Settings) AddMarketplace(mp Marketplace) error {
	if mp.Name == "" {
		return fmt.Errorf("marketplace name required")
	}
	m, _ := s.Raw["extraKnownMarketplaces"].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	src := map[string]any{"source": mp.SourceType}
	switch mp.SourceType {
	case "github", "git":
		if mp.Repo == "" {
			return fmt.Errorf("--repo required for %s source", mp.SourceType)
		}
		src["repo"] = mp.Repo
	case "local":
		if mp.Path == "" {
			return fmt.Errorf("--path required for local source")
		}
		src["path"] = mp.Path
	default:
		return fmt.Errorf("unknown source type %q (use github|git|local)", mp.SourceType)
	}
	m[mp.Name] = map[string]any{
		"source":     src,
		"autoUpdate": mp.AutoUpdate,
	}
	s.Raw["extraKnownMarketplaces"] = m
	return nil
}

func (s *Settings) RemoveMarketplace(name string) bool {
	m, ok := s.Raw["extraKnownMarketplaces"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := m[name]; !exists {
		return false
	}
	delete(m, name)
	s.Raw["extraKnownMarketplaces"] = m
	return true
}

// --- skill overrides --------------------------------------------------------

func (s *Settings) SkillOverride(skill string) (string, bool) {
	m, _ := s.Raw["skillOverrides"].(map[string]any)
	v, ok := m[skill]
	if !ok {
		return "", false
	}
	str, _ := v.(string)
	return str, true
}

func (s *Settings) SetSkillOverride(skill, value string) {
	m, _ := s.Raw["skillOverrides"].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	m[skill] = value
	s.Raw["skillOverrides"] = m
}

func (s *Settings) RemoveSkillOverride(skill string) bool {
	m, ok := s.Raw["skillOverrides"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := m[skill]; !exists {
		return false
	}
	delete(m, skill)
	if len(m) == 0 {
		delete(s.Raw, "skillOverrides")
	} else {
		s.Raw["skillOverrides"] = m
	}
	return true
}
