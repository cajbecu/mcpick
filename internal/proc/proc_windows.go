//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// Windows has no execve: every launch runs the agent as a child process.
var errNoExec = errors.New("exec unsupported")

func replaceProcess(string, []string, []string) error { return errNoExec }

// TerminalSignals: Ctrl-C is delivered to every process attached to the
// console, the agent included, so mcpick only has to survive it.
func TerminalSignals() []os.Signal { return []os.Signal{os.Interrupt} }

// ForwardedSignals is empty: Windows cannot deliver signals to a child.
func ForwardedSignals() []os.Signal { return nil }

func signalExitCode(*os.ProcessState) (int, bool) { return 0, false }

// SetProcessGroup is a no-op on Windows.
func SetProcessGroup(*exec.Cmd) {}

// KillProcessGroup kills the process; Windows has no process groups to reach
// grandchildren through.
func KillProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// stillActive is the exit code GetExitCodeProcess reports for a process that
// has not exited (STILL_ACTIVE).
const stillActive = 259

// PidAlive reports whether pid still names a running process on this host.
// Answering "alive" unconditionally would be the safe-looking shortcut, but it
// means a crashed session's project file is never recovered.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied means the process exists and belongs to someone else.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
