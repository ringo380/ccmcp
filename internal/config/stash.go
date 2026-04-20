package config

import "sort"

// Stash wraps ~/.claude-mcp-stash.json — a ccmcp-owned holding area for MCP server configs
// that the user wants "installed but turned off" without losing their env/command.
type Stash struct {
	Path string
	Raw  map[string]any
}

func LoadStash(path string) (*Stash, error) {
	raw, err := RawJSON(path)
	if err != nil {
		return nil, err
	}
	if _, ok := raw["userMcpServers"].(map[string]any); !ok {
		raw["userMcpServers"] = map[string]any{}
	}
	return &Stash{Path: path, Raw: raw}, nil
}

func (s *Stash) Save() error { return WriteJSON(s.Path, s.Raw) }

func (s *Stash) Entries() map[string]any {
	return objOrEmpty(s.Raw, "userMcpServers")
}

func (s *Stash) Names() []string { return sortedKeys(s.Entries()) }

func (s *Stash) Get(name string) (any, bool) {
	m := s.Entries()
	v, ok := m[name]
	return v, ok
}

func (s *Stash) Put(name string, cfg any) {
	m := objOrEmpty(s.Raw, "userMcpServers")
	m[name] = cfg
	s.Raw["userMcpServers"] = m
}

func (s *Stash) Delete(name string) bool {
	m, ok := s.Raw["userMcpServers"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := m[name]; !exists {
		return false
	}
	delete(m, name)
	s.Raw["userMcpServers"] = m
	return true
}

// Profiles wraps ~/.claude-mcp-profiles.json.
type Profiles struct {
	Path string
	Raw  map[string]any
}

func LoadProfiles(path string) (*Profiles, error) {
	raw, err := RawJSON(path)
	if err != nil {
		return nil, err
	}
	if _, ok := raw["profiles"].(map[string]any); !ok {
		raw["profiles"] = map[string]any{}
	}
	return &Profiles{Path: path, Raw: raw}, nil
}

func (p *Profiles) Save() error { return WriteJSON(p.Path, p.Raw) }

func (p *Profiles) Names() []string {
	m, _ := p.Raw["profiles"].(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (p *Profiles) MCPs(name string) ([]string, bool) {
	m, _ := p.Raw["profiles"].(map[string]any)
	v, ok := m[name]
	if !ok {
		return nil, false
	}
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

func (p *Profiles) Set(name string, mcps []string) {
	m, _ := p.Raw["profiles"].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	arr := make([]any, len(mcps))
	for i, s := range mcps {
		arr[i] = s
	}
	m[name] = arr
	p.Raw["profiles"] = m
}

func (p *Profiles) Delete(name string) bool {
	m, ok := p.Raw["profiles"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := m[name]; !exists {
		return false
	}
	delete(m, name)
	p.Raw["profiles"] = m
	return true
}
