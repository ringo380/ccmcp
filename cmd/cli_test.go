package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI executes the root command with args in a sandboxed $HOME/$CLAUDE_CONFIG_DIR so
// real user state is never touched. It captures both os.Stdout (commands use fmt.Println
// directly) and the cobra buffer so anything the tool prints is visible to the test.
func runCLI(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	// Reset package-global flag state between invocations (cobra keeps values)
	flagPath = ""
	flagDryRun = false
	flagJSON = false
	flagNoColor = false
	mcpScope = ""
	mcpFromStash = false
	mcpToStash = false
	mcpAll = false
	pluginFilterEnabled = false
	pluginFilterDisabled = false
	pluginPurge = false
	pluginMarketplace = ""
	pluginRegisterOnly = false
	mktSource = "github"
	mktRepo = ""
	mktLocalPath = ""
	mktAutoUpdate = true
	mcpMoveTo = ""
	overrideUndo = false
	overrideSource = ""
	overridePluginOf = ""
	pruneIncludeStashGhosts = false
	pruneYes = false

	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	// Capture os.Stdout
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	rootCmd.SetOut(w)
	rootCmd.SetErr(w)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	w.Close()
	out := <-done
	return out, err
}

func setupSandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	must := func(path string, v any) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(home, ".claude.json"), map[string]any{
		"anonymousId": "sandbox",
		"mcpServers": map[string]any{
			"keep-me": map[string]any{"command": "echo"},
		},
	})
	must(filepath.Join(home, ".claude-mcp-stash.json"), map[string]any{
		"userMcpServers": map[string]any{
			"parked": map[string]any{"command": "parked-cmd"},
		},
	})
	must(filepath.Join(home, ".claude-mcp-profiles.json"), map[string]any{})
	must(filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"a@mkt": true,
			"b@mkt": false,
		},
	})
	must(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), map[string]any{
		"version": float64(2),
		"plugins": map[string]any{},
	})
	return home
}

func TestCLIStatusReadsAllScopes(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "status")
	if err != nil {
		t.Fatalf("status err: %v output: %s", err, out)
	}
	if !strings.Contains(out, "keep-me") {
		t.Error("status should show user MCP 'keep-me'")
	}
	if !strings.Contains(out, "parked") {
		t.Error("status should show stashed MCP 'parked'")
	}
	if !strings.Contains(out, "1 enabled / 2 known") {
		t.Errorf("plugin counts wrong:\n%s", out)
	}
}

func TestCLIStashAndRestore(t *testing.T) {
	home := setupSandbox(t)

	// stash keep-me
	if _, err := runCLI(t, home, "mcp", "stash", "keep-me"); err != nil {
		t.Fatal(err)
	}
	// Verify it landed in stash and left user scope
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	if _, ok := claude["mcpServers"]; ok {
		t.Error("mcpServers should be deleted (was down to 0)")
	}
	stash := readJSON(t, filepath.Join(home, ".claude-mcp-stash.json"))
	servers, _ := stash["userMcpServers"].(map[string]any)
	if _, ok := servers["keep-me"]; !ok {
		t.Error("keep-me should be in stash")
	}
	if _, ok := servers["parked"]; !ok {
		t.Error("parked should still be in stash")
	}

	// restore both
	if _, err := runCLI(t, home, "mcp", "restore"); err != nil {
		t.Fatal(err)
	}
	claude = readJSON(t, filepath.Join(home, ".claude.json"))
	user, _ := claude["mcpServers"].(map[string]any)
	if len(user) != 2 {
		t.Errorf("want 2 restored user MCPs, got %d", len(user))
	}
}

func TestCLIProjectEnableDisableRoundtrip(t *testing.T) {
	home := setupSandbox(t)
	before := readJSON(t, filepath.Join(home, ".claude.json"))

	proj := "/tmp/test-proj"
	if _, err := runCLI(t, home, "mcp", "enable", "parked", "--scope", "project", "--path", proj); err != nil {
		t.Fatal(err)
	}
	after := readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ := after["projects"].(map[string]any)
	if _, ok := projects[proj].(map[string]any)["mcpServers"].(map[string]any)["parked"]; !ok {
		t.Fatalf("parked not enabled in project: %v", projects)
	}

	if _, err := runCLI(t, home, "mcp", "disable", "parked", "--scope", "project", "--path", proj); err != nil {
		t.Fatal(err)
	}
	// After disable, project entry should be cleaned up OR not contain parked anymore.
	after = readJSON(t, filepath.Join(home, ".claude.json"))
	if projects, ok := after["projects"].(map[string]any); ok {
		if p, ok := projects[proj].(map[string]any); ok {
			if mcps, ok := p["mcpServers"].(map[string]any); ok {
				if _, still := mcps["parked"]; still {
					t.Error("parked should be removed after disable")
				}
			}
		}
	}

	// Unknown fields preserved
	if after["anonymousId"] != before["anonymousId"] {
		t.Error("anonymousId should be preserved across mutations")
	}
}

func TestCLIPluginToggle(t *testing.T) {
	home := setupSandbox(t)
	if _, err := runCLI(t, home, "plugin", "disable", "a@mkt"); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	plugins, _ := settings["enabledPlugins"].(map[string]any)
	if plugins["a@mkt"] != false {
		t.Errorf("a@mkt should be false, got %#v", plugins["a@mkt"])
	}

	if _, err := runCLI(t, home, "plugin", "enable", "a@mkt"); err != nil {
		t.Fatal(err)
	}
	settings = readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	plugins, _ = settings["enabledPlugins"].(map[string]any)
	if plugins["a@mkt"] != true {
		t.Error("a@mkt should be true after re-enable")
	}
}

func TestCLIDryRunDoesNotWrite(t *testing.T) {
	home := setupSandbox(t)
	before, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if _, err := runCLI(t, home, "mcp", "enable", "parked", "--scope", "project", "--path", "/tmp/p", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if string(before) != string(after) {
		t.Error("--dry-run must not write")
	}
}

func TestCLIProfileSaveAndUse(t *testing.T) {
	home := setupSandbox(t)
	if _, err := runCLI(t, home, "profile", "save", "dev", "keep-me", "parked"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, home, "profile", "use", "dev", "--path", "/tmp/newproj"); err != nil {
		t.Fatal(err)
	}
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ := claude["projects"].(map[string]any)
	proj, _ := projects["/tmp/newproj"].(map[string]any)
	mcps, _ := proj["mcpServers"].(map[string]any)
	if _, ok := mcps["keep-me"]; !ok {
		t.Error("keep-me should be applied from user-scope source")
	}
	if _, ok := mcps["parked"]; !ok {
		t.Error("parked should be applied from stash")
	}
}

func TestCLIMoveUserToLocalRemovesFromUser(t *testing.T) {
	home := setupSandbox(t)
	// Sanity: keep-me starts in user scope
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	if _, ok := claude["mcpServers"].(map[string]any)["keep-me"]; !ok {
		t.Fatal("precondition: keep-me should be in user scope")
	}
	proj := "/tmp/cli-move-test"
	if _, err := runCLI(t, home, "mcp", "move", "keep-me", "--to", "local", "--path", proj); err != nil {
		t.Fatalf("move: %v", err)
	}
	claude = readJSON(t, filepath.Join(home, ".claude.json"))
	// Should be GONE from user scope
	if user, _ := claude["mcpServers"].(map[string]any); user != nil {
		if _, still := user["keep-me"]; still {
			t.Error("keep-me should be removed from user scope after move")
		}
	}
	// Should now be in local scope for the project
	projects, _ := claude["projects"].(map[string]any)
	node, _ := projects[proj].(map[string]any)
	mcps, _ := node["mcpServers"].(map[string]any)
	if _, ok := mcps["keep-me"]; !ok {
		t.Errorf("keep-me should be in local scope of %s; got %v", proj, mcps)
	}
}

func TestCLIMoveAcceptsProjectAlias(t *testing.T) {
	home := setupSandbox(t)
	proj := "/tmp/cli-move-alias"
	// 'project' is accepted as legacy alias for 'local'
	if _, err := runCLI(t, home, "mcp", "move", "keep-me", "--to", "project", "--path", proj); err != nil {
		t.Fatal(err)
	}
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ := claude["projects"].(map[string]any)
	node, _ := projects[proj].(map[string]any)
	mcps, _ := node["mcpServers"].(map[string]any)
	if _, ok := mcps["keep-me"]; !ok {
		t.Error("project alias should route to local scope")
	}
}

func TestCLIScopeLocalAliasInEnable(t *testing.T) {
	home := setupSandbox(t)
	proj := "/tmp/cli-local-alias"
	// --scope local should behave exactly like --scope project.
	if _, err := runCLI(t, home, "mcp", "enable", "parked", "--scope", "local", "--path", proj); err != nil {
		t.Fatal(err)
	}
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ := claude["projects"].(map[string]any)
	node, _ := projects[proj].(map[string]any)
	mcps, _ := node["mcpServers"].(map[string]any)
	if _, ok := mcps["parked"]; !ok {
		t.Error("--scope local should put the MCP in project-node mcpServers")
	}
}

func TestCLIOverridePluginRoundtrip(t *testing.T) {
	home := setupSandbox(t)
	proj := "/tmp/cli-override-proj"

	// Disable a plugin-sourced MCP per-project.
	if _, err := runCLI(t, home, "mcp", "override", "plugin:context7:context7", "--path", proj); err != nil {
		t.Fatalf("override: %v", err)
	}
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ := claude["projects"].(map[string]any)
	node, _ := projects[proj].(map[string]any)
	arr, _ := node["disabledMcpServers"].([]any)
	found := false
	for _, v := range arr {
		if s, _ := v.(string); s == "plugin:context7:context7" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected plugin:context7:context7 in disabledMcpServers; got %v", arr)
	}

	// Idempotent: second override emits "no change".
	if _, err := runCLI(t, home, "mcp", "override", "plugin:context7:context7", "--path", proj); err != nil {
		t.Fatal(err)
	}

	// Undo.
	if _, err := runCLI(t, home, "mcp", "override", "plugin:context7:context7", "--undo", "--path", proj); err != nil {
		t.Fatal(err)
	}
	claude = readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ = claude["projects"].(map[string]any)
	node, _ = projects[proj].(map[string]any)
	arr, _ = node["disabledMcpServers"].([]any)
	for _, v := range arr {
		if s, _ := v.(string); s == "plugin:context7:context7" {
			t.Error("override --undo should have removed the entry")
		}
	}
}

func TestCLIOverrideClaudeAi(t *testing.T) {
	home := setupSandbox(t)
	proj := "/tmp/cli-override-ai"

	if _, err := runCLI(t, home, "mcp", "override", "claude.ai Gmail", "--path", proj); err != nil {
		t.Fatal(err)
	}
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	node, _ := claude["projects"].(map[string]any)[proj].(map[string]any)
	arr, _ := node["disabledMcpServers"].([]any)
	if len(arr) == 0 || arr[0] != "claude.ai Gmail" {
		t.Errorf("expected 'claude.ai Gmail' in disabledMcpServers; got %v", arr)
	}
}

func TestCLIOverrideUnqualifiedFallbackToStdio(t *testing.T) {
	home := setupSandbox(t)
	proj := "/tmp/cli-override-stdio"

	// "keep-me" is a user-scope stdio MCP in the sandbox
	if _, err := runCLI(t, home, "mcp", "override", "keep-me", "--path", proj); err != nil {
		t.Fatal(err)
	}
	claude := readJSON(t, filepath.Join(home, ".claude.json"))
	node, _ := claude["projects"].(map[string]any)[proj].(map[string]any)
	arr, _ := node["disabledMcpServers"].([]any)
	if len(arr) == 0 || arr[0] != "keep-me" {
		t.Errorf("expected 'keep-me' in disabledMcpServers; got %v", arr)
	}
}

func TestCLIPruneSkipsDisabledPluginAndStashGhosts(t *testing.T) {
	home := setupSandbox(t)
	proj := "/tmp/cli-prune-proj"

	// Seed disabledMcpServers with one of each bucket. Sandbox doesn't have any
	// plugin infrastructure so plugin:* entries will classify as orphan-plugin — which
	// is fine for this test (we're checking that the command prunes orphans but keeps
	// stash ghosts and live-stdio by default).
	cj := readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ := cj["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	projects[proj] = map[string]any{
		"disabledMcpServers": []any{
			"keep-me",             // live in user scope → stdioLive (NOT pruned)
			"parked",              // in stash → stashGhost (kept unless --include-stash-ghosts)
			"plugin:fake:fake",    // not installed → orphanPlugin (pruned)
			"totally-gone",        // no source → orphanStdio (pruned)
		},
	}
	cj["projects"] = projects
	b, _ := json.Marshal(cj)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	// 1) prune --dry-run: list but don't write
	before, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if _, err := runCLI(t, home, "mcp", "prune", "--path", proj, "--dry-run"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if string(before) != string(after) {
		t.Error("--dry-run should not modify disk")
	}

	// 2) prune --yes: default behavior prunes orphan-plugin + orphan-stdio
	if _, err := runCLI(t, home, "mcp", "prune", "--path", proj, "--yes"); err != nil {
		t.Fatal(err)
	}
	cj = readJSON(t, filepath.Join(home, ".claude.json"))
	remaining := asStringList(cj["projects"].(map[string]any)[proj].(map[string]any)["disabledMcpServers"])
	// Should keep: keep-me (live), parked (stash ghost)
	// Should remove: plugin:fake:fake, totally-gone
	got := map[string]bool{}
	for _, k := range remaining {
		got[k] = true
	}
	if !got["keep-me"] {
		t.Error("stdioLive (keep-me) should be preserved")
	}
	if !got["parked"] {
		t.Error("stashGhost (parked) should be preserved by default")
	}
	if got["plugin:fake:fake"] || got["totally-gone"] {
		t.Errorf("orphans should be removed, still present: %v", remaining)
	}
}

func TestCLIPruneWithIncludeStashGhosts(t *testing.T) {
	home := setupSandbox(t)
	proj := "/tmp/cli-prune-stash"

	cj := readJSON(t, filepath.Join(home, ".claude.json"))
	projects, _ := cj["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	projects[proj] = map[string]any{
		"disabledMcpServers": []any{"parked", "totally-gone"},
	}
	cj["projects"] = projects
	b, _ := json.Marshal(cj)
	os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600)

	if _, err := runCLI(t, home, "mcp", "prune", "--path", proj, "--yes", "--include-stash-ghosts"); err != nil {
		t.Fatal(err)
	}
	cj = readJSON(t, filepath.Join(home, ".claude.json"))
	node, _ := cj["projects"].(map[string]any)[proj].(map[string]any)
	remaining := asStringList(node["disabledMcpServers"])
	if len(remaining) != 0 {
		t.Errorf("both should be pruned with --include-stash-ghosts, got: %v", remaining)
	}
}

func asStringList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
