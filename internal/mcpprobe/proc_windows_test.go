//go:build windows

package mcpprobe

import "golang.org/x/sys/windows"

// stillActive is STATUS_PENDING, what GetExitCodeProcess reports for a
// running process. x/sys/windows does not export it.
const stillActive = 259

// processAlive asks the kernel for the exit code: a running process has none.
// There is no zombie state on Windows; a terminated process reports its code
// as soon as TerminateProcess lands.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
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
