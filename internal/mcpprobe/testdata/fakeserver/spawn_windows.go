//go:build windows

package main

import (
	"os"
	"syscall"
)

// detachedProcess is DETACHED_PROCESS from processthreadsapi.h; syscall does
// not export it.
const detachedProcess = 0x00000008

// escapeAttr is the closest Windows analog of setsid: a new process group,
// detached from the console. It does NOT leave a kill-on-close Job Object,
// which is why the escapee test skips on Windows - there is nothing to escape.
func escapeAttr(attr *os.ProcAttr) {
	attr.Sys = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}
