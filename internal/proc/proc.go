// Package proc launches the agent and owns everything platform-specific about
// processes: exec, signals, process groups and liveness checks.
package proc

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

// ExitCode carries a child's exit status up to main so mcpick returns what the
// agent returned.
type ExitCode int

func (e ExitCode) Error() string { return "exit status" }

// Launch runs argv with extra layered over the current environment.
//
// With no cleanup, mcpick gets out of the way entirely and replaces its own
// process with the agent. With one, it has to stay alive to run it, so the
// agent runs as a child and Launch returns its exit status as an ExitCode.
func Launch(argv, extra []string, cleanup func()) error {
	run := func() {
		if cleanup != nil {
			cleanup()
		}
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		run()
		return err
	}
	env := MergeEnv(os.Environ(), extra)

	if cleanup == nil {
		if err := replaceProcess(bin, argv, env); !errors.Is(err, errNoExec) {
			return err
		}
	}

	cmd := exec.Command(bin, argv[1:]...) //nolint:noctx // the agent runs until it exits; nothing should cancel it
	cmd.Args = argv
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// Ctrl-C belongs to the agent. mcpick catches the terminal signals only so
	// they do not kill it before the cleanup, and passes on the ones aimed at
	// its own pid.
	quiet := make(chan os.Signal, 1)
	signal.Notify(quiet, TerminalSignals()...)
	defer signal.Stop(quiet)
	fwd := make(chan os.Signal, 1)
	if sigs := ForwardedSignals(); len(sigs) > 0 {
		signal.Notify(fwd, sigs...)
		defer signal.Stop(fwd)
	}

	if err := cmd.Start(); err != nil {
		run()
		return err
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-quiet:
			case s := <-fwd:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	run()

	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			// A child killed by a signal reports -1; the shell convention is
			// 128+signal, which is what a caller's `$?` expects.
			if code, ok := signalExitCode(ee.ProcessState); ok {
				return ExitCode(code)
			}
			return ExitCode(ee.ExitCode())
		}
		return waitErr
	}
	return nil
}

// MergeEnv appends extra over base, replacing rather than duplicating keys.
// Duplicates are legal but resolved differently by different libcs, and the
// overlay targets depend on their value winning.
func MergeEnv(base, extra []string) []string {
	override := map[string]bool{}
	for _, kv := range extra {
		if k, ok := envKey(kv); ok {
			override[k] = true
		}
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if k, ok := envKey(kv); ok && override[k] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

func envKey(kv string) (string, bool) {
	for i := 1; i < len(kv); i++ { // from 1: Windows has variables like "=C:"
		if kv[i] == '=' {
			return kv[:i], true
		}
	}
	return "", false
}

// ShellQuote quotes the arguments a POSIX shell would split or expand, so a
// printed command line can be copied and run. Placeholders like
// <rendered config> are left alone.
func ShellQuote(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		placeholder := strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">")
		if !placeholder && (a == "" || strings.ContainsAny(a, " \t\n\"'$`\\|&;()<>*?[]{}!#~")) {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
			continue
		}
		out[i] = a
	}
	return out
}
