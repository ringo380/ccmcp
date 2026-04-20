package config

// MCPJson wraps a project's .mcp.json (the file that ships in the repo).
type MCPJson struct {
	Path string
	Raw  map[string]any
}

func LoadMCPJson(path string) (*MCPJson, error) {
	raw, err := RawJSON(path)
	if err != nil {
		return nil, err
	}
	return &MCPJson{Path: path, Raw: raw}, nil
}

func (m *MCPJson) Save() error { return WriteJSON(m.Path, m.Raw) }

// Servers returns the live mcpServers map from .mcp.json (never nil). Read-only.
func (m *MCPJson) Servers() map[string]any {
	return objOrEmpty(m.Raw, "mcpServers")
}

func (m *MCPJson) Names() []string { return sortedKeys(m.Servers()) }
