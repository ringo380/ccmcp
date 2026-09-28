//go:build !windows

package main

import (
	"os"
	"syscall"
)

// escapeAttr makes the child leave the parent's process group (setsid), so a
// group SIGKILL cannot reach it. Paired with an inherited stderr in
// spawnSleeper, this is the combination that pins cmd.Wait open.
func escapeAttr(attr *os.ProcAttr) {
	attr.Sys = &syscall.SysProcAttr{Setsid: true}
}
