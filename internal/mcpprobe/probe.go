package mcpprobe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/ringo380/ccmcp/internal/tokens"
)

// DefaultTimeout bounds one whole probe - spawn, initialize and tools/list
// together - when the caller's context carries no deadline of its own. A server
// that takes longer than this is reported as a timeout rather than waited on:
// the result is cached either way, so a hanging server costs the user this once,
// not once per visit.
//
// It is a FALLBACK, not a cap: see Probe. A var rather than a const so tests can
// shrink it and exercise the timeout path without waiting out the real one.
var DefaultTimeout = 10 * time.Second

// protocolVersion is the MCP revision ccmcp announces in initialize. Servers
// that speak a different revision are expected to answer with their own.
const protocolVersion = "2025-06-18"

// Tool is one tool's measured contribution to the prompt.
type Tool struct {
	Name         string `json:"name"`
	SchemaTokens int    `json:"schemaTokens"` // full name+description+inputSchema
}

// Result is one completed probe attempt, success or failure. A failure is a
// fact worth caching, so the identifying fields are filled in either way.
type Result struct {
	Key               string    `json:"key"`
	Name              string    `json:"name"`
	ProbedAt          time.Time `json:"probedAt"`
	OK                bool      `json:"ok"`
	Err               string    `json:"err,omitempty"`
	InstructionTokens int       `json:"instructionTokens"`
	Tools             []Tool    `json:"tools"`

	// NameTokens is the cost of the bare tool-name list Claude Code falls
	// back to when it collapses schemas behind ToolSearch. It is measured at
	// probe time and stored rather than recomputed by Deferred, because a
	// Result read back from cache in a process with no working encoder would
	// otherwise silently recompute it as zero - and a zero that means
	// "could not measure" is precisely the bug this feature exists to avoid.
	NameTokens int `json:"nameTokens"`
}

// Loaded is the per-turn cost with every tool schema inlined.
func (r Result) Loaded() int {
	total := r.InstructionTokens
	for _, t := range r.Tools {
		total += t.SchemaTokens
	}
	return total
}

// Deferred is the cost when Claude Code collapses schemas to a bare name list
// behind ToolSearch, which it does above an internal, undocumented threshold.
func (r Result) Deferred() int {
	return r.InstructionTokens + r.NameTokens
}

// rpcMessage is the subset of a JSON-RPC frame the probe cares about. ID is
// kept raw so a notification (no id) is distinguishable from a response.
type rpcMessage struct {
	ID     *json.RawMessage `json:"id"`
	Result json.RawMessage  `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type initializeResult struct {
	Instructions string `json:"instructions"`
}

type toolsListResult struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
}

// costedTool is the exact shape whose serialization is billed to a tool. It
// mirrors what a client puts in the prompt: name, description and schema.
type costedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Probe runs one target to completion or timeout. It ALWAYS returns a Result
// and never an error - a failure is a cached fact, not something to propagate,
// so a server that hangs does not re-hang the user on the next visit. It must
// not panic on any server behaviour, however malformed.
func Probe(ctx context.Context, t Target) (res Result) {
	res = Result{Key: t.Key(), Name: t.Name, ProbedAt: time.Now()}

	// Last-resort guard: a probe is driven entirely by untrusted subprocess
	// output, and a panic here would take down a TUI render.
	defer func() {
		if r := recover(); r != nil {
			res.OK = false
			res.Tools = nil
			res.InstructionTokens = 0
			res.NameTokens = 0
			res.Err = fmt.Sprintf("internal error while probing: %v", r)
		}
	}()

	if t.Command == "" {
		res.Err = "no command - cannot probe locally"
		return res
	}

	// A caller-supplied deadline wins outright, in BOTH directions: `ccmcp mcp
	// probe --timeout 30s` exists to give a slow server longer, and
	// context.WithTimeout keeps whichever deadline is sooner - so layering
	// DefaultTimeout on top unconditionally would silently clamp that flag back
	// to 10s. DefaultTimeout applies only when the caller set no deadline at
	// all, so a probe is never unbounded either.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	if err := probeInto(ctx, t, &res); err != nil {
		res.OK = false
		res.Tools = nil
		res.InstructionTokens = 0
		res.NameTokens = 0
		res.Err = err.Error()
		return res
	}

	res.OK = true
	return res
}

// probeInto does the work and reports the first failure. Everything it writes
// into res is discarded by Probe if it returns an error, so a partial
// measurement is never presented as a real one.
func probeInto(ctx context.Context, t Target, res *Result) error {
	cmd := exec.CommandContext(ctx, t.Command, t.Args...)
	cmd.Env = os.Environ()
	for k, v := range t.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// Put the server in its own process group so the whole tree can be
	// signalled at once. exec.CommandContext only kills the direct child, and
	// npx-style servers routinely spawn children that would outlive it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("cannot open stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		// Start is never reached, so os/exec's own parent-side pipe cleanup
		// never runs either - close what we already opened ourselves.
		_ = stdin.Close()
		return fmt.Errorf("cannot open stdout: %v", err)
	}
	// Servers log freely to stderr. Discarding through a pipe that os/exec
	// drains in the background keeps a chatty server from filling the pipe
	// buffer and blocking forever on its own log writes.
	cmd.Stderr = io.Discard
	// Because Stderr is not an *os.File, os/exec builds a pipe for it, and
	// Wait blocks until every holder of that pipe's write end closes it. A
	// descendant that escapes the process group - one that calls setsid, or
	// daemonizes - survives the group SIGKILL below while still holding the
	// inherited stderr, and Wait would then block forever, defeating the
	// timeout this whole function is built around. WaitDelay bounds exactly
	// that: once the context is done or the child has exited, Wait waits at
	// most this long before closing the pipes and returning.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start server: %v", err)
	}

	// Registered immediately after a successful Start so every later return
	// path - error, timeout or panic - tears the whole group down.
	pgid := cmd.Process.Pid
	stop := make(chan struct{})
	defer func() {
		close(stop)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	lines := make(chan []byte, 8)
	scanErr := make(chan error, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		// Tool schemas routinely exceed bufio's 64KB default line limit, and
		// the failure mode there is silent truncation.
		sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-stop:
				return
			}
		}
		scanErr <- sc.Err()
	}()

	rd := &responses{lines: lines, scanErr: scanErr, ctx: ctx}

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "ccmcp", "version": "1"},
		},
	}
	if err := writeMessage(stdin, initReq); err != nil {
		return fmt.Errorf("initialize request failed: %v", err)
	}
	initRaw, err := rd.await(1)
	if err != nil {
		return fmt.Errorf("initialize failed: %v", err)
	}
	var initRes initializeResult
	// Fail rather than skip: a swallowed unmarshal would leave
	// InstructionTokens at zero on an OK result, and a zero that means
	// "never measured" is the exact confusion this feature exists to remove.
	if err := json.Unmarshal(initRaw, &initRes); err != nil {
		return fmt.Errorf("initialize returned an unreadable result: %v", err)
	}
	instructionTokens, err := tokens.Count(initRes.Instructions)
	if err != nil {
		return fmt.Errorf("cannot measure tokens: %v", err)
	}
	res.InstructionTokens = instructionTokens

	notify := map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}
	if err := writeMessage(stdin, notify); err != nil {
		return fmt.Errorf("initialized notification failed: %v", err)
	}

	listReq := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}
	if err := writeMessage(stdin, listReq); err != nil {
		return fmt.Errorf("tools/list request failed: %v", err)
	}
	listRaw, err := rd.await(2)
	if err != nil {
		return fmt.Errorf("tools/list failed: %v", err)
	}
	var listRes toolsListResult
	if err := json.Unmarshal(listRaw, &listRes); err != nil {
		return fmt.Errorf("tools/list returned an unreadable result: %v", err)
	}

	names := make([]string, 0, len(listRes.Tools))
	for _, tl := range listRes.Tools {
		serialized, err := json.Marshal(costedTool{
			Name:        tl.Name,
			Description: tl.Description,
			InputSchema: tl.InputSchema,
		})
		if err != nil {
			return fmt.Errorf("tool %q has an unreadable schema: %v", tl.Name, err)
		}
		n, countErr := tokens.Count(string(serialized))
		if countErr != nil {
			return fmt.Errorf("cannot measure tokens: %v", countErr)
		}
		res.Tools = append(res.Tools, Tool{Name: tl.Name, SchemaTokens: n})
		names = append(names, tl.Name+"\n")
	}

	nameTokens, err := tokens.CountLines(names)
	if err != nil {
		return fmt.Errorf("cannot measure tokens: %v", err)
	}
	res.NameTokens = nameTokens
	return nil
}

// responses reads newline-delimited frames and hands back the one matching a
// request id, ignoring notifications, log noise and responses to other ids.
type responses struct {
	lines chan []byte
	// scanErr carries why the reader stopped. It is always populated before
	// lines is closed, so a closed lines channel can be explained rather than
	// blamed on the server exiting.
	scanErr chan error
	ctx     context.Context
	sawJunk bool
}

// await returns the result payload of the response carrying id, or an error
// describing why it never arrived.
func (r *responses) await(id int) (json.RawMessage, error) {
	for {
		select {
		case <-r.ctx.Done():
			if r.sawJunk {
				return nil, fmt.Errorf("timed out after writing non-JSON output to stdout instead of a response")
			}
			return nil, fmt.Errorf("timed out waiting for a response")
		case line, open := <-r.lines:
			if !open {
				select {
				case scanErr := <-r.scanErr:
					if scanErr != nil {
						return nil, fmt.Errorf("unreadable output from server: %v", scanErr)
					}
				default:
				}
				if r.sawJunk {
					return nil, fmt.Errorf("server exited after writing non-JSON output to stdout")
				}
				return nil, fmt.Errorf("server exited before responding")
			}
			var msg rpcMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				// Tolerated rather than fatal: a server that prints a banner
				// alongside valid protocol frames is still measurable. It is
				// only reported if nothing usable ever arrives.
				r.sawJunk = true
				continue
			}
			if msg.ID == nil {
				continue // a notification - not what we are waiting for
			}
			var got int
			if err := json.Unmarshal(*msg.ID, &got); err != nil || got != id {
				continue // someone else's response
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("server error %d: %s", msg.Error.Code, msg.Error.Message)
			}
			return msg.Result, nil
		}
	}
}

// writeMessage sends one newline-delimited JSON-RPC frame. MCP stdio transport
// is newline-delimited JSON - there is no Content-Length framing here.
func writeMessage(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
