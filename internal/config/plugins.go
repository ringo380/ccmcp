package config

import (
	"sort"
	"strings"
)

// InstalledPlugins wraps ~/.claude/plugins/installed_plugins.json.
// Shape: { "version": 2, "plugins": { "<id>@<mkt>": [ {scope, installPath, version, ...}, ... ] } }
type InstalledPlugins struct {
	Path string
	Raw  map[string]any
}

func LoadInstalledPlugins(path string) (*InstalledPlugins, error) {
	raw, err := RawJSON(path)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		raw = map[string]any{}
	}
	if _, ok := raw["plugins"].(map[string]any); !ok {
		raw["plugins"] = map[string]any{}
	}
	if _, ok := raw["version"]; !ok {
		raw["version"] = 2
	}
	return &InstalledPlugins{Path: path, Raw: raw}, nil
}

func (p *InstalledPlugins) Save() error {
	return WriteJSON(p.Path, p.Raw)
}

type InstalledPlugin struct {
	ID          string
	Scope       string
	InstallPath string
	Version     string
}

func (p *InstalledPlugins) List() []InstalledPlugin {
	plugins, _ := p.Raw["plugins"].(map[string]any)
	out := make([]InstalledPlugin, 0, len(plugins))
	for id, v := range plugins {
		arr, _ := v.([]any)
		if len(arr) == 0 {
			out = append(out, InstalledPlugin{ID: id})
			continue
		}
		entry, _ := arr[0].(map[string]any)
		inst := InstalledPlugin{ID: id}
		if entry != nil {
			inst.Scope, _ = entry["scope"].(string)
			inst.InstallPath, _ = entry["installPath"].(string)
			inst.Version, _ = entry["version"].(string)
		}
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (p *InstalledPlugins) Has(id string) bool {
	plugins, _ := p.Raw["plugins"].(map[string]any)
	_, ok := plugins[id]
	return ok
}

// ByName returns every installed entry whose bare plugin name (without @marketplace)
// equals `name`. Used to attribute `plugin:<name>:<server>` override keys back to a
// concrete on-disk plugin — those keys don't include the marketplace, so a name-only
// lookup is the only option. May return 0, 1, or multiple hits (same plugin name in
// two marketplaces is allowed).
func (p *InstalledPlugins) ByName(name string) []InstalledPlugin {
	var out []InstalledPlugin
	for _, ip := range p.List() {
		n, _ := ParsePluginID(ip.ID)
		if n == name {
			out = append(out, ip)
		}
	}
	return out
}

// Remove deletes the entry. Returns the installPath of the first entry if present, so the
// caller can optionally delete the on-disk cache.
func (p *InstalledPlugins) Remove(id string) (installPath string, removed bool) {
	plugins, ok := p.Raw["plugins"].(map[string]any)
	if !ok {
		return "", false
	}
	v, exists := plugins[id]
	if !exists {
		return "", false
	}
	if arr, ok := v.([]any); ok && len(arr) > 0 {
		if entry, ok := arr[0].(map[string]any); ok {
			installPath, _ = entry["installPath"].(string)
		}
	}
	delete(plugins, id)
	p.Raw["plugins"] = plugins
	return installPath, true
}

// ParsePluginID splits "name@marketplace" into (name, marketplace). Missing @ -> ("name", "").
func ParsePluginID(id string) (name, marketplace string) {
	if i := strings.LastIndex(id, "@"); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

// QualifyPluginID returns id as-is if it already contains @marketplace; otherwise appends @mkt.
func QualifyPluginID(id, marketplace string) string {
	if strings.Contains(id, "@") {
		return id
	}
	if marketplace == "" {
		return id
	}
	return id + "@" + marketplace
}

// ResolvePluginID searches installed + enabled entries for an unqualified `id`.
// Returns the qualified form, or "" if not found / ambiguous.
func ResolvePluginID(id string, settings *Settings, installed *InstalledPlugins) (string, []string) {
	if strings.Contains(id, "@") {
		return id, nil
	}
	matches := map[string]struct{}{}
	if installed != nil {
		for _, p := range installed.List() {
			n, _ := ParsePluginID(p.ID)
			if n == id {
				matches[p.ID] = struct{}{}
			}
		}
	}
	if settings != nil {
		for _, e := range settings.PluginEntries() {
			n, _ := ParsePluginID(e.ID)
			if n == id {
				matches[e.ID] = struct{}{}
			}
		}
	}
	var found []string
	for m := range matches {
		found = append(found, m)
	}
	sort.Strings(found)
	if len(found) == 1 {
		return found[0], nil
	}
	return "", found
}

// --- known_marketplaces.json -----------------------------------------------

type KnownMarketplaces struct {
	Path string
	Raw  map[string]any
}

func LoadKnownMarketplaces(path string) (*KnownMarketplaces, error) {
	raw, err := RawJSON(path)
	if err != nil {
		return nil, err
	}
	return &KnownMarketplaces{Path: path, Raw: raw}, nil
}

func (k *KnownMarketplaces) Names() []string {
	return sortedKeys(k.Raw)
}
