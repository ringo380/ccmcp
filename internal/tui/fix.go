package tui

import (
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// fixKind distinguishes how a proposal is applied.
//
//	fixInTUI       — write `proposed` bytes to `target` file (Doctor markdown fixes).
//	fixClaudeCLI   — hand off to `claude --print` to edit `target`.
//	fixInMemory    — run `applyFn` to mutate in-memory state (Summary config edits).
//	                  Marked dirty by applyFn; saved by the global 'w' flow. No
//	                  snapshot, no post-apply diff — the user keeps or discards
//	                  the whole session with w/Q like other in-memory edits.
type fixKind int

const (
	fixInTUI fixKind = iota
	fixClaudeCLI
	fixInMemory
)

// fixProposal carries everything needed to preview, apply, and revert a fix.
// Used by both the Doctor and Summary tabs.
type fixProposal struct {
	summary   string
	kind      fixKind
	target    string   // primary file being modified (empty for fixInMemory)
	proposed  []byte   // pre-computed post-state bytes (fixInTUI only); nil for CLI
	cliArgs   []string // args for exec.Command("claude", cliArgs...)
	cliPrompt string   // full prompt text (CLI only) — shown verbatim in confirm panel

	// cat is the Summary-tab issue category this proposal addresses, used by
	// the post-fix asset-cache invalidation logic to decide whether the fix
	// could have affected skill/agent/command discovery or lint output. Zero
	// (catNone) for Doctor proposals and Summary categories whose fix has no
	// asset-side effects (orphan prunes, stash drops, .claude.json mcpServer
	// edits) — those skip invalidation.
	cat summaryCat

	// applyFn is the in-memory mutator for fixInMemory proposals. Returns the
	// flash message on success (e.g. "pruned 'foo'") and any error. Receives the
	// shared state so it can flip dirty flags and read other config surfaces.
	applyFn func(*state) (string, error)

	// previewLines, when set, overrides the auto-generated diff preview shown
	// in the confirm panel. Used by fixInMemory proposals where there is no
	// file diff but we still want a clear "this is what will change" summary.
	previewLines []string

	// runtime-populated
	snapshotPath string // disk path of pre-fix snapshot, set by executeFix
	beforeBytes  []byte // in-memory copy of target file pre-fix (for CLI revert)
}

// fixDoneMsg is delivered when a fix completes. `origin` routes the message
// back to the view that started the fix so Doctor and Summary can share the
// machinery without crosstalk.
type fixDoneMsg struct {
	err      error
	proposal *fixProposal
	output   []byte // combined stdout+stderr from the claude CLI (empty for fixInTUI)
	origin   tabID
}

// execFixCmd runs the claude CLI in a goroutine and emits a fixDoneMsg when it
// exits. Replaces the prior tea.ExecProcess flow, which suspended the TUI and
// dumped the user to raw terminal output until the subprocess finished.
// Tests replace this variable to stub out the spawn.
var execFixCmd = func(cmd *exec.Cmd, p *fixProposal, origin tabID) tea.Cmd {
	return func() tea.Msg {
		out, err := cmd.CombinedOutput()
		return fixDoneMsg{err: err, proposal: p, output: out, origin: origin}
	}
}

// fixElapsed formats elapsed time since `started` to second precision (e.g.
// "12s") for the in-flight progress panel.
func fixElapsed(started time.Time) string {
	if started.IsZero() {
		return "0s"
	}
	return time.Since(started).Truncate(time.Second).String()
}

// runClaudePrint shells out to `claude --print` with the prompt on stdin,
// returning combined stdout (the model response). Used by Summary's `l` review
// path. Test stubs override claudeReviewCmd to bypass the subprocess.
var claudeReviewCmd = func(workdir, prompt string) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bin, "--print")
	cmd.Dir = workdir
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}
