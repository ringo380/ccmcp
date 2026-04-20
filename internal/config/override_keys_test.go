package config

import "testing"

func TestOverrideKeyRoundtrip(t *testing.T) {
	cases := []struct {
		source MCPSource
		name   string
		plugin string
		wire   string
	}{
		{SourceUser, "dropbox", "", "dropbox"},
		{SourceLocal, "xcodebuildmcp", "", "xcodebuildmcp"},
		{SourceProject, "foo", "", "foo"},
		{SourceClaude, "Gmail", "", "claude.ai Gmail"},
		{SourceClaude, "Hugging Face (2)", "", "claude.ai Hugging Face (2)"}, // names with spaces
		{SourcePlugin, "context7", "context7", "plugin:context7:context7"},
		{SourcePlugin, "cloudflare-api", "cloudflare", "plugin:cloudflare:cloudflare-api"},
		{SourcePlugin, "Prisma-Local", "prisma", "plugin:prisma:Prisma-Local"},
		{SourceUnknown, "base-mcp", "", "base-mcp"},
	}
	for _, tc := range cases {
		t.Run(tc.wire, func(t *testing.T) {
			if got := OverrideKey(tc.source, tc.name, tc.plugin); got != tc.wire {
				t.Errorf("OverrideKey: got %q want %q", got, tc.wire)
			}
			src, name, plugin := ParseOverrideKey(tc.wire)
			// ParseOverrideKey reports Unknown for plain stdio keys (caller must resolve)
			wantSrc := tc.source
			if tc.source == SourceUser || tc.source == SourceLocal || tc.source == SourceProject {
				wantSrc = SourceUnknown
			}
			if src != wantSrc {
				t.Errorf("ParseOverrideKey source: got %q want %q (wire=%q)", src, wantSrc, tc.wire)
			}
			if name != tc.name {
				t.Errorf("ParseOverrideKey name: got %q want %q (wire=%q)", name, tc.name, tc.wire)
			}
			if plugin != tc.plugin {
				t.Errorf("ParseOverrideKey plugin: got %q want %q (wire=%q)", plugin, tc.plugin, tc.wire)
			}
		})
	}
}

func TestOverrideKeyStashIsEmpty(t *testing.T) {
	// Stash isn't reflected in disabledMcpServers — it's a ccmcp-local concept.
	if got := OverrideKey(SourceStash, "foo", ""); got != "" {
		t.Errorf("stash should have empty override key, got %q", got)
	}
}

func TestParseMalformedPluginKey(t *testing.T) {
	src, name, plugin := ParseOverrideKey("plugin:broken")
	if src != SourceUnknown || name != "plugin:broken" || plugin != "" {
		t.Errorf("malformed plugin key: got (%q, %q, %q)", src, name, plugin)
	}
}
