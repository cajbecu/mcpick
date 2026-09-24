//go:build !windows

package proc

import "os/exec"

func lookSh() (string, error) { return exec.LookPath("sh") }
