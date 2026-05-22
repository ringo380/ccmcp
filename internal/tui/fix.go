package tui

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

// cliStreamLineMsg carries one line of stdout/stderr from a live `claude
// --print` invocation. Views append to a ring buffer so the user can toggle a
// log panel showing the most recent activity. `next` is the Cmd that returns
// the *next* line (or the final fixDoneMsg when the process exits) — the
// model handler must include it in tea.Batch so the drain doesn't stall.
type cliStreamLineMsg struct {
	line     string
	stderr   bool
	origin   tabID
	proposal *fixProposal
	next     tea.Cmd
}

// streamRingMax bounds the log buffer kept on each view. The on-screen panel
// renders the last 10; 50 gives us headroom for "what happened just before"
// when the user reveals the panel after the fact.
const streamRingMax = 50

// claudeFixModelArgs returns the model + max-turns flags appended to every
// `claude --print` invocation that we expect to actually edit a file. Cheap
// model (Haiku) + a turn cap prevent the prior behaviour where Sonnet/Opus
// burned tokens producing prose responses that never invoked Edit.
func claudeFixModelArgs() []string {
	return []string{"--model", doctor.DefaultAnthropicModel, "--max-turns", "4"}
}

// wrapImperativeFixPrompt prepends a strict instruction that forces Claude to
// reach for the Edit/Write tool against the named target, rather than narrate
// the change in prose. The historical failure mode was a clean exit 0 with no
// file changes — costly and worthless. `target` may be empty for multi-target
// bundles; when empty, the envelope speaks to each path listed inside the body.
func wrapImperativeFixPrompt(target, body string) string {
	if target == "" {
		return fmt.Sprintf(
			"You MUST use the Edit (or Write, for new files) tool to apply every change described below. Do not respond in prose. Do not summarise. Make the edits and exit.\n\nIf a specific change is ambiguous, make the smallest reasonable edit rather than refusing — refusing without an edit is the worst outcome.\n\nTask:\n\n%s",
			body,
		)
	}
	return fmt.Sprintf(
		"You MUST use the Edit (or Write, for new files) tool to modify the file at: %s. Do not respond in prose. Do not print the proposed change. Make the edit and exit.\n\nIf the exact change is ambiguous, make the smallest reasonable edit rather than refusing — refusing without an edit is the worst outcome.\n\nTask:\n\n%s",
		target, body,
	)
}

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
	cliArgs      []string // args for exec.Command("claude", cliArgs...)
	cliPrompt    string   // full envelope-wrapped prompt text (CLI only) — shown verbatim in confirm panel and passed via cliArgs
	cliPromptRaw string   // un-wrapped task body, used by bulk builders to concatenate multiple fixes under a single outer envelope without double-wrapping

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

	// bulkTargets, when set, names additional files this proposal touches beyond
	// `target`. Used by Doctor's category-bulk CLI fixes so snapshot/revert
	// covers every file the bundled prompt might edit, not just the primary.
	bulkTargets []string

	// bulkApplyFn, when set, replaces the default single-file write for
	// fixInTUI proposals. Used by Doctor's programmatic category-bulk to apply
	// every per-issue proposal in one keystroke. Takes the snapshot dir,
	// returns count-applied + a path→snapshotPath map (so the post-apply
	// revert/keep flow can restore each file), plus any fatal error.
	bulkApplyFn func(snapDir string) (int, map[string]string, error)

	// runtime-populated
	snapshotPath  string            // disk path of pre-fix snapshot for `target`
	bulkSnapshots map[string]string // path → snapshot path for files in bulkTargets
	beforeBytes   []byte            // in-memory copy of target file pre-fix (for CLI revert)
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

// execFixCmd runs the claude CLI with stdout+stderr piped so the TUI can
// stream lines into the togglable log panel. Each line is delivered as a
// cliStreamLineMsg; when the process exits the channel closes and the
// drainer emits a final fixDoneMsg with the stitched output preserved so the
// existing "no edits" tail display still works. Tests replace this variable
// to stub out the spawn (the stub typically synthesises a single fixDoneMsg
// directly, bypassing the stream entirely).
var execFixCmd = func(cmd *exec.Cmd, p *fixProposal, origin tabID) tea.Cmd {
	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		return func() tea.Msg { return fixDoneMsg{err: err, proposal: p, origin: origin} }
	}
	stderrR, err := cmd.StderrPipe()
	if err != nil {
		return func() tea.Msg { return fixDoneMsg{err: err, proposal: p, origin: origin} }
	}
	if err := cmd.Start(); err != nil {
		return func() tea.Msg { return fixDoneMsg{err: err, proposal: p, origin: origin} }
	}

	// Per-invocation state shared between the pump goroutines and the drain
	// Cmd. The mutex guards the captured-output buffer that fixDoneMsg.output
	// needs at the end (the "no edits" tail display reads it).
	type streamState struct {
		mu       sync.Mutex
		captured []byte
		lines    chan cliStreamLineMsg
	}
	s := &streamState{lines: make(chan cliStreamLineMsg, 256)}

	pump := func(r io.Reader, isStderr bool, wg *sync.WaitGroup) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024) // tolerate long stdout lines
		for scanner.Scan() {
			line := scanner.Text()
			s.mu.Lock()
			s.captured = append(s.captured, []byte(line)...)
			s.captured = append(s.captured, '\n')
			s.mu.Unlock()
			s.lines <- cliStreamLineMsg{
				line:     line,
				stderr:   isStderr,
				origin:   origin,
				proposal: p,
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go pump(stdoutR, false, &wg)
	go pump(stderrR, true, &wg)
	// Capture the Wait error in a goroutine-shared slot so the drainer can
	// fall back to it when ProcessState alone produces an unhelpful exit
	// code (e.g. "exit status -1" on signal kill).
	var waitErr error
	go func() {
		wg.Wait()
		waitErr = cmd.Wait()
		close(s.lines)
	}()

	// drain is the chainable Cmd. Each invocation pulls one item from the
	// channel; on a real line it returns a cliStreamLineMsg whose `next`
	// field IS the same drain Cmd so the handler can re-arm. When the
	// channel closes we emit the terminal fixDoneMsg.
	var drain tea.Cmd
	drain = func() tea.Msg {
		line, ok := <-s.lines
		if !ok {
			var exitErr error
			if st := cmd.ProcessState; st != nil && !st.Success() {
				exitErr = fmt.Errorf("exit status %d", st.ExitCode())
			}
			if exitErr == nil && waitErr != nil {
				// Signal-killed processes leave ProcessState reporting
				// success=false with a -1 code; prefer the richer Wait
				// error in that case so the user sees a real message.
				exitErr = waitErr
			}
			s.mu.Lock()
			out := append([]byte(nil), s.captured...)
			s.mu.Unlock()
			return fixDoneMsg{err: exitErr, proposal: p, output: out, origin: origin}
		}
		line.next = drain
		return line
	}
	return drain
}

// fixElapsed formats elapsed time since `started` to second precision (e.g.
// "12s") for the in-flight progress panel.
func fixElapsed(started time.Time) string {
	if started.IsZero() {
		return "0s"
	}
	return time.Since(started).Truncate(time.Second).String()
}

// killIfRunning best-effort kills the given exec.Cmd's process if it's still
// alive. Used by the global quit path so an in-flight claude --print doesn't
// outlive the TUI session. Safe to call with nil. Ignores all errors — by
// design, this is fire-and-forget cleanup.
func killIfRunning(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return
	}
	_ = cmd.Process.Kill()
}

// appendStreamLine inserts `line` (annotated with its stderr origin via the
// "!" prefix) into the view's ring buffer, capped at streamRingMax. The panel
// renders the last 10; the rest is headroom for "what happened just before"
// when the user reveals the panel mid-run.
func appendStreamLine(ring []string, line string, isStderr bool) []string {
	prefix := " "
	if isStderr {
		prefix = "!"
	}
	entry := prefix + " " + line
	ring = append(ring, entry)
	if len(ring) > streamRingMax {
		ring = ring[len(ring)-streamRingMax:]
	}
	return ring
}

// renderStreamPanel renders the bordered log panel shown when the user toggles
// `L` while a fix is running (or just finished). Limits to the last `tail`
// lines. stderr entries (prefix "!") render in styleErr; stdout in styleDim.
func renderStreamPanel(title string, ring []string, tail, width int) string {
	if width < 12 {
		width = 80
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(strings.Repeat("─", width-2)))
	b.WriteString("\n")
	b.WriteString(title + "\n")
	if len(ring) == 0 {
		b.WriteString(styleDim.Render("  (no output yet)"))
		b.WriteString("\n")
		return b.String()
	}
	start := 0
	if len(ring) > tail {
		start = len(ring) - tail
	}
	for _, entry := range ring[start:] {
		stylized := styleDim.Render(entry)
		if strings.HasPrefix(entry, "!") {
			stylized = styleErr.Render(entry)
		}
		b.WriteString("  ")
		b.WriteString(stylized)
		b.WriteString("\n")
	}
	return b.String()
}

// chatDoneMsg is delivered when an interactive `claude` follow-up session
// exits. The view re-lints so the result of any in-session edits surface.
type chatDoneMsg struct {
	err    error
	origin tabID
}

// launchClaudeChat spawns an interactive `claude` session in `workdir`,
// suspending the TUI for the duration. Optional `contextPrompt` is appended
// to the system prompt via Claude Code's `--append-system-prompt` flag so the
// model knows what the user was just doing in ccmcp. Returns a tea.Cmd that
// the caller chains; on exit a chatDoneMsg lands on the originating view.
// Tests stub `execChatCmd` to bypass the real spawn.
var execChatCmd = func(workdir, contextPrompt string, origin tabID) tea.Cmd {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return func() tea.Msg { return chatDoneMsg{err: err, origin: origin} }
	}
	args := []string{}
	if strings.TrimSpace(contextPrompt) != "" {
		args = append(args, "--append-system-prompt", contextPrompt)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = workdir
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return chatDoneMsg{err: err, origin: origin}
	})
}

// runClaudePrint shells out to `claude --print` with the prompt on stdin,
// returning combined stdout (the model response). Used by Summary's `l` review
// path. Test stubs override claudeReviewCmd to bypass the subprocess.
var claudeReviewCmd = func(workdir, prompt string) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bin, "--print", "--model", doctor.DefaultAnthropicModel, "--max-turns", "4")
	cmd.Dir = workdir
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}
