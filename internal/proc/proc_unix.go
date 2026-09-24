//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

var errNoExec = errors.New("exec unsupported")

func replaceProcess(bin string, argv, env []string) error {
	return syscall.Exec(bin, argv, env)
}

// TerminalSignals reach the whole foreground process group straight from the
// tty, so the agent already has them. mcpick catches them only to survive long
// enough to clean up; forwarding them would deliver each Ctrl-C twice, and a
// double Ctrl-C is how several agents spell "quit now".
func TerminalSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGQUIT}
}

// ForwardedSignals are aimed at mcpick's pid alone (kill, a closing terminal),
// so the agent only learns about them if mcpick passes them on.
func ForwardedSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGHUP}
}

func signalExitCode(state *os.ProcessState) (int, bool) {
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0, false
	}
	return 128 + int(ws.Signal()), true
}

// SetProcessGroup puts cmd in a group of its own. npx, uvx and friends start
// the real server as a grandchild; a group lets KillProcessGroup take the
// whole tree down, not just the wrapper.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// KillProcessGroup kills the group SetProcessGroup created.
func KillProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

// PidAlive reports whether pid still names a running process on this host.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
