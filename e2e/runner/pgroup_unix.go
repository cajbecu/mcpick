//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// inOwnGroup starts cmd in a process group of its own, so that a late probe
// can be taken down with everything mcpick and the agent started.
func inOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
