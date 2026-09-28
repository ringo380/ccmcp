//go:build !windows

package tui

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processGone reports whether pid is gone for the purposes of the probe-cancel
// tests: either no longer addressable by a signal, or dead and waiting to be
// reaped.
//
// A zombie has to count as gone, and this is the whole reason the helper exists:
// syscall.Kill(pid, 0) SUCCEEDS for a process that has been killed but not yet
// reaped, so "a signal would be deliverable" is a different question from "the
// process is running". The probe reaps its own direct child through cmd.Wait,
// but the grandchild is reparented to PID 1 and reaped asynchronously, and
// platforms differ in how fast that happens. Verified in a linux/amd64
// container: after the quit path the direct child was ESRCH while the grandchild
// read PPid 1, State Z (zombie) - killed, not orphaned - which is exactly the
// case that failed CI on Linux while passing on macOS.
//
// /proc is Linux-only; on macOS the fallback is simply the kill(0) answer, which
// is what has always been checked there.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
		return true
	}
	return isZombie(pid)
}

// isZombie reads the kernel's own view of the process state. Returns false when
// /proc is unavailable (macOS), where the kill(0) check above stands alone.
func isZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.Contains(line, "Z")
		}
	}
	return false
}

// killPid is the test-side cleanup for fixture processes that outlive a test.
func killPid(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
