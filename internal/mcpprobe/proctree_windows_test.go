//go:build windows

package mcpprobe

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestProbeFailsClosedWhenTheJobAssignmentFails pins that a server ccmcp could
// not contain is reported as a failure, never measured, and that its direct
// child is killed at once rather than left to the caller's deadline.
//
// The fixture runs in "sleeper" mode: it never reads stdin, so closing stdin
// does not end it and the only things that can are the explicit kill or the
// context deadline. A probe that returns well inside the deadline therefore
// proves the kill landed; without it, cmd.Wait sits until the context expires.
func TestProbeFailsClosedWhenTheJobAssignmentFails(t *testing.T) {
	orig := assignToJob
	assignToJob = func(windows.Handle, windows.Handle) error { return errors.New("injected refusal") }
	t.Cleanup(func() { assignToJob = orig })

	const deadline = 5 * time.Second
	start := time.Now()
	res := Probe(shortCtx(t, deadline), fakeTarget("sleeper"))
	elapsed := time.Since(start)

	if res.OK {
		t.Fatalf("an uncontained server must not report OK: %+v", res)
	}
	if !strings.Contains(res.Err, "cannot contain") {
		t.Fatalf("Err = %q, want the containment failure named", res.Err)
	}
	// Half the deadline is far above the cost of a kill and far below the
	// deadline itself, so a missing kill fails here instead of passing by luck.
	if elapsed > deadline/2 {
		t.Fatalf("Probe took %s after a failed containment; the direct child was not killed and Wait ran to the deadline", elapsed)
	}
}
