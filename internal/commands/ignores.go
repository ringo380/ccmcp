package commands

import (
	"sort"

	"github.com/ringo380/ccmcp/internal/config"
)

// Ignores is ccmcp-owned state that records conflict keys the user has
// explicitly decided not to see in reports. Stored at ~/.claude-ccmcp-ignores.json.
// Shape: { "ignoredConflicts": ["effective-name", ...] }
type Ignores struct {
	Path string
	Raw  map[string]any
}

func LoadIgnores(path string) (*Ignores, error) {
	raw, err := config.RawJSON(path)
	if err != nil {
		return nil, err
	}
	return &Ignores{Path: path, Raw: raw}, nil
}

func (i *Ignores) Save() error {
	return config.WriteJSON(i.Path, i.Raw)
}

func (i *Ignores) Has(effective string) bool {
	arr, _ := i.Raw["ignoredConflicts"].([]any)
	for _, v := range arr {
		if s, _ := v.(string); s == effective {
			return true
		}
	}
	return false
}

func (i *Ignores) Add(effective string) bool {
	if i.Has(effective) {
		return false
	}
	arr, _ := i.Raw["ignoredConflicts"].([]any)
	arr = append(arr, effective)
	i.Raw["ignoredConflicts"] = arr
	return true
}

func (i *Ignores) List() []string {
	arr, _ := i.Raw["ignoredConflicts"].([]any)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Filter removes conflicts whose Effective appears in the ignore list.
func (i *Ignores) Filter(in []Conflict) []Conflict {
	if i == nil {
		return in
	}
	out := make([]Conflict, 0, len(in))
	for _, c := range in {
		if i.Has(c.Effective) {
			continue
		}
		out = append(out, c)
	}
	return out
}
