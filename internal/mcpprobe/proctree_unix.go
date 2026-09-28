//go:build !windows

package mcpprobe

import (
	"os/exec"
	"syscall"
)

// prepareProcessGroup puts the server in its own process group so the whole
// tree can be signalled at once. exec.CommandContext only kills the direct
// child, and npx-style servers routinely spawn children that would outlive it.
func prepareProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// processTree is a handle on a started server and everything it spawned.
type processTree struct{ pgid int }

// newProcessTree records the group id; the group already exists because
// prepareProcessGroup ran before Start.
func newProcessTree(cmd *exec.Cmd) (processTree, error) {
	return processTree{pgid: cmd.Process.Pid}, nil
}

// Kill SIGKILLs the whole group.
func (t processTree) Kill() { _ = syscall.Kill(-t.pgid, syscall.SIGKILL) }

// Close is a no-op; a process group holds no handle.
func (t processTree) Close() {}
