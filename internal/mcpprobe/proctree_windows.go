//go:build windows

package mcpprobe

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// prepareProcessGroup starts the server in a new console process group so a
// Ctrl+C aimed at ccmcp never reaches it - the Windows half of the Unix
// "own process group" rationale. Containment itself is the Job Object below.
func prepareProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// assignToJob is a seam for tests that need the assignment to fail.
var assignToJob = windows.AssignProcessToJobObject

// processTree is a Job Object with kill-on-close: every descendant the server
// spawns inherits membership, and TerminateJobObject (or the last handle
// closing) ends all of them. Known limit: the process is assigned AFTER Start,
// so a grandchild spawned inside that microsecond window is not a member.
// Go's os/exec cannot start a child suspended, so this is the smallest window
// available; TestProbeLeavesNoProcessBehind is the check that it does not bite.
type processTree struct{ job windows.Handle }

func newProcessTree(cmd *exec.Cmd) (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return processTree{}, fmt.Errorf("create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return processTree{}, fmt.Errorf("configure job object: %w", err)
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return processTree{}, fmt.Errorf("open server process: %w", err)
	}
	defer windows.CloseHandle(proc)
	if err := assignToJob(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return processTree{}, fmt.Errorf("assign server to job object: %w", err)
	}
	return processTree{job: job}, nil
}

// Kill terminates every process in the job.
func (t processTree) Kill() {
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	}
}

// Close releases the job handle; with kill-on-close this also ends anything
// Kill missed.
func (t processTree) Close() {
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
	}
}
