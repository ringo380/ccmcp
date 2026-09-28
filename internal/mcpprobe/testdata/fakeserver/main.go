// Command fakeserver is a stand-in MCP stdio server used by the mcpprobe
// tests. It speaks newline-delimited JSON-RPC on stdin/stdout and switches
// behaviour on the FAKE_MODE environment variable so one binary covers every
// case the probe has to survive.
//
// It lives under testdata/ so the go tool excludes it from ./... builds and
// vets of the parent module; the test binary builds it explicitly by path.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	mode := os.Getenv("FAKE_MODE")

	switch mode {
	case "crash":
		os.Exit(1)
	case "sleeper":
		// A long-lived grandchild. Its only job is to still be running (or
		// not) when the probe returns, which is how the no-leaked-process
		// test detects a kill that only reached the direct child.
		time.Sleep(60 * time.Second)
		return
	}

	if mode == "hangchild" {
		spawnSleeper(false)
	}
	if mode == "escapee" {
		// A descendant that BOTH leaves the process group (setsid) and keeps
		// the inherited stderr open. It survives the probe's group SIGKILL, so
		// the stderr pipe's write end stays open and cmd.Wait has nothing
		// bounding it but WaitDelay.
		spawnSleeper(true)
	}
	if mode == "banner" {
		// A non-JSON startup banner emitted BEFORE any valid frame. The probe
		// must tolerate it and still measure the server.
		fmt.Println("fakeserver 0.0.1 - listening on stdio")
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()

		var req struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue // a notification - nothing to answer
		}
		id := string(*req.ID)

		switch mode {
		case "hang", "hangchild", "escapee":
			continue // read everything, answer nothing
		case "garbage":
			fmt.Println("[info] starting up, this line is not JSON at all")
			continue
		}

		switch req.Method {
		case "initialize":
			if mode == "slowinit" {
				// Slower than a shrunken DefaultTimeout but far faster than a
				// caller deadline, so a test can tell which one the probe
				// actually applied.
				time.Sleep(400 * time.Millisecond)
			}
			if mode == "noinit" {
				emit(`{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32000,"message":"initialize refused by fake server"}}`)
				continue
			}
			if mode == "badinit" {
				// A well-formed JSON-RPC response whose result is not an
				// object. Unmarshalling it into initializeResult fails.
				emit(`{"jsonrpc":"2.0","id":` + id + `,"result":"not an object"}`)
				continue
			}
			// A log notification and a response to an id nobody is waiting
			// for, both emitted before the real answer: the probe must match
			// on id rather than assuming the next line is its response.
			emit(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"warming up"}}`)
			emit(`{"jsonrpc":"2.0","id":9001,"result":{"stray":true}}`)
			emit(`{"jsonrpc":"2.0","id":` + id + `,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fakeserver","version":"0.0.1"},"instructions":"Use the alpha tool to align widgets and the beta tool to burnish them. Always confirm the widget id with the operator before burnishing anything, because burnishing cannot be undone."}}`)
			if mode == "ok" {
				// Enough stderr to overflow the 64KB pipe buffer several
				// times over. A probe that wires stderr to a pipe and never
				// reads it deadlocks here instead of answering tools/list.
				noise := strings.Repeat("fakeserver stderr noise line\n", 8000)
				fmt.Fprint(os.Stderr, noise)
			}
		case "tools/list":
			emit(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"listing tools"}}`)
			tools := toolsJSON
			if mode == "big" {
				// One frame well past bufio's 64KB default line limit, whose
				// failure mode is silent truncation rather than an error.
				tools = `[{"name":"huge_tool","description":"` + strings.Repeat("verbose schema prose ", 12000) + `","inputSchema":{"type":"object"}}]`
			}
			emit(`{"jsonrpc":"2.0","id":` + id + `,"result":{"tools":` + tools + `}}`)
		default:
			emit(`{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32601,"message":"method not found"}}`)
		}
	}
}

// spawnSleeper starts a copy of this binary in sleeper mode - the stand-in for
// the child processes real npx-based servers leave behind - and records both
// pids in FAKE_PIDFILE, newest last.
//
// escape makes the child call setsid and inherit this process's stderr, which
// is the combination that pins cmd.Wait open: the group kill cannot reach it,
// and it holds the stderr pipe's write end.
func spawnSleeper(escape bool) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	// The inherited FAKE_MODE must be removed, not merely overridden: Go's
	// env parsing keeps the FIRST occurrence of a duplicate key, so appending
	// FAKE_MODE=sleeper to os.Environ() would leave the child in hangchild
	// mode and it would spawn a chain of its own.
	env := []string{"FAKE_MODE=sleeper"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FAKE_MODE=") || strings.HasPrefix(kv, "FAKE_PIDFILE=") {
			continue
		}
		env = append(env, kv)
	}

	attr := &os.ProcAttr{
		Env:   env,
		Files: []*os.File{nil, nil, nil},
	}
	if escape {
		attr.Files = []*os.File{nil, nil, os.Stderr}
		escapeAttr(attr)
	}

	child, err := os.StartProcess(self, []string{self}, attr)
	if err != nil {
		return
	}
	pidfile := os.Getenv("FAKE_PIDFILE")
	if pidfile == "" {
		return
	}
	body := strconv.Itoa(os.Getpid()) + "\n" + strconv.Itoa(child.Pid) + "\n"
	_ = os.WriteFile(pidfile, []byte(body), 0o644)
}

func emit(s string) {
	fmt.Println(s)
}

const toolsJSON = `[{"name":"align_widget","description":"Align a widget to the nearest grid line, optionally snapping to a coarser grid.","inputSchema":{"type":"object","properties":{"widgetId":{"type":"string","description":"Identifier of the widget to align."},"grid":{"type":"integer","description":"Grid size in points to snap to.","default":8}},"required":["widgetId"]}},{"name":"burnish_widget","description":"Burnish a widget so it reflects light evenly. This operation is irreversible and should be confirmed with the operator first.","inputSchema":{"type":"object","properties":{"widgetId":{"type":"string","description":"Identifier of the widget to burnish."},"passes":{"type":"integer","description":"How many burnishing passes to run.","default":1},"dryRun":{"type":"boolean","description":"Report what would happen without touching the widget."}},"required":["widgetId"]}}]`
