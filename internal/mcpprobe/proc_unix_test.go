//go:build !windows

package mcpprobe

import "syscall"

// processAlive reports whether a signal could be delivered to pid. A zombie
// counts as alive here; waitForExit polls, so the few-millisecond reap window
// resolves on its own.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) != syscall.ESRCH
}

// killPid is the test-side cleanup for fixture processes that outlive a test.
func killPid(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
