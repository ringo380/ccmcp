package cmd

import (
	"strings"
	"testing"
)

// resetCobraCompletion forces cobra to re-add the auto-generated `completion`
// subcommand (and its bash/zsh/fish/powershell children) on the next Execute.
// Without this, subcommands captured os.Stdout via closure during their first
// init, so a second runCLI in the same test process would write to a pipe the
// previous run already closed.
func resetCobraCompletion(t *testing.T) {
	t.Helper()
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "completion" {
			rootCmd.RemoveCommand(sub)
			return
		}
	}
}

// TestCLICompletionGeneratesShellScripts exercises `completion <shell>` for
// every supported shell to confirm cobra's built-in subcommand is wired and
// each produced script references the ccmcp binary name.
func TestCLICompletionGeneratesShellScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			resetCobraCompletion(t)
			home := setupSandbox(t)
			out, err := runCLI(t, home, "completion", shell)
			if err != nil {
				t.Fatalf("completion %s err: %v output: %s", shell, err, out)
			}
			if len(out) < 200 {
				t.Errorf("completion %s output too short (%d bytes), likely empty", shell, len(out))
			}
			if !strings.Contains(out, "ccmcp") {
				t.Errorf("completion %s output should reference 'ccmcp':\n%s", shell, out)
			}
		})
	}
}

// TestCLICompletionScopeFlag asserts that `--scope` exposes the fixed value
// set via cobra's __complete protocol.
func TestCLICompletionScopeFlag(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "__complete", "mcp", "list", "--scope", "")
	if err != nil {
		t.Fatalf("__complete scope err: %v output: %s", err, out)
	}
	for _, want := range []string{"user", "local", "project", "stash", "all"} {
		if !strings.Contains(out, want) {
			t.Errorf("--scope completion should offer %q:\n%s", want, out)
		}
	}
}

// TestCLICompletionStashName asserts that `mcp stash <name>` dynamically
// completes from the user-scope MCP names in the active config.
func TestCLICompletionStashName(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "__complete", "mcp", "stash", "")
	if err != nil {
		t.Fatalf("__complete stash err: %v output: %s", err, out)
	}
	if !strings.Contains(out, "keep-me") {
		t.Errorf("`mcp stash` should complete with user MCP 'keep-me':\n%s", out)
	}
}

// TestCLICompletionRestoreName asserts that `mcp restore <name>` dynamically
// completes from the stash.
func TestCLICompletionRestoreName(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "__complete", "mcp", "restore", "")
	if err != nil {
		t.Fatalf("__complete restore err: %v output: %s", err, out)
	}
	if !strings.Contains(out, "parked") {
		t.Errorf("`mcp restore` should complete with stashed entry 'parked':\n%s", out)
	}
}
