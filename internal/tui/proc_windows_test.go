//go:build windows

package tui

import "golang.org/x/sys/windows"

const stillActive = 259 // STATUS_PENDING: GetExitCodeProcess's "still running"

// processGone reports whether pid has exited. Windows has no zombie state, so
// the exit-code check stands alone.
func processGone(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code != stillActive
}

// killPid is the test-side cleanup for fixture processes that outlive a test.
func killPid(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 1)
}
