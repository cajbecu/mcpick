//go:build !unix

package main

import "os/exec"

// The runner only ever runs in the Linux container; elsewhere it has to
// compile, and killing the process alone is the best that can be done.
func inOwnGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Kill() }
}
