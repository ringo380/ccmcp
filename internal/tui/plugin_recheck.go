package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/discovery"
)

// pluginRecheckMsg carries the result of a live marketplace-membership recheck.
type pluginRecheckMsg struct {
	removed map[string]bool
	err     error
}

// recheckRemovedCmd fetches live marketplace manifests for the marketplaces backing
// installed plugins and returns the set of ids no longer present. Membership-only -
// no disk writes. Marketplaces whose remote source can't be resolved or fetched are
// skipped (their ids stay un-flagged) so a transient network/registration gap never
// produces a false "removed" warning.
func (v *pluginView) recheckRemovedCmd() tea.Cmd {
	var ids []string
	for _, ip := range v.st.installed.List() {
		ids = append(ids, ip.ID)
	}
	remotes := resolveRemoteMarketplaces(v.st)
	return func() tea.Msg {
		removed, err := recheckRemovedLive(ids, remotes)
		return pluginRecheckMsg{removed: removed, err: err}
	}
}

// resolveRemoteMarketplaces builds a name -> RemoteMarketplace map from the user's
// extra marketplaces and the system-known marketplaces, keeping only github/git
// sources (local marketplaces are covered by the offline cache path).
func resolveRemoteMarketplaces(st *state) map[string]discovery.RemoteMarketplace {
	out := map[string]discovery.RemoteMarketplace{}
	for _, mp := range st.settings.ExtraMarketplaces() {
		if (mp.SourceType == "github" || mp.SourceType == "git") && mp.Repo != "" {
			out[mp.Name] = discovery.RemoteMarketplace{Name: mp.Name, Source: mp.SourceType, Repo: mp.Repo}
		}
	}
	if known, err := config.LoadKnownMarketplaces(st.paths.KnownMarkets); err == nil && known != nil {
		for name, v := range known.Raw {
			entry, _ := v.(map[string]any)
			src, _ := entry["source"].(map[string]any)
			if src == nil {
				continue
			}
			source, _ := src["source"].(string)
			repo, _ := src["repo"].(string)
			if (source == "github" || source == "git") && repo != "" {
				if _, exists := out[name]; !exists { // extras win
					out[name] = discovery.RemoteMarketplace{Name: name, Source: source, Repo: repo}
				}
			}
		}
	}
	return out
}

// recheckRemovedLive groups ids by marketplace, fetches each resolvable marketplace's
// live manifest once, and flags ids whose plugin name is absent. Ids whose marketplace
// has no resolvable remote, or whose fetch fails, are left un-flagged.
func recheckRemovedLive(ids []string, remotes map[string]discovery.RemoteMarketplace) (map[string]bool, error) {
	byMkt := map[string][]string{} // marketplace -> plugin names
	idOf := map[string]string{}    // marketplace+"\x00"+name -> full id
	for _, id := range ids {
		name, mkt := config.ParsePluginID(id)
		if mkt == "" {
			continue
		}
		byMkt[mkt] = append(byMkt[mkt], name)
		idOf[mkt+"\x00"+name] = id
	}
	if len(byMkt) == 0 {
		return map[string]bool{}, nil
	}

	client := discovery.NewHTTPClient(15 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	removed := map[string]bool{}
	resolved := 0
	var lastErr error
	for mkt, names := range byMkt {
		rm, ok := remotes[mkt]
		if !ok {
			continue
		}
		resolved++
		m, err := discovery.FetchManifest(ctx, client, rm)
		if err != nil {
			lastErr = err
			continue
		}
		present := map[string]bool{}
		for _, p := range m.Plugins {
			present[p.Name] = true
		}
		for _, name := range names {
			if !present[name] {
				removed[idOf[mkt+"\x00"+name]] = true
			}
		}
	}
	if resolved == 0 {
		return removed, fmt.Errorf("no marketplaces with a resolvable remote source to recheck")
	}
	return removed, lastErr
}
