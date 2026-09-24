package proc

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMergeEnvReplacesInsteadOfDuplicating(t *testing.T) {
	got := MergeEnv([]string{"A=1", "CODEX_HOME=/real", "B=2"}, []string{"CODEX_HOME=/overlay"})
	joined := strings.Join(got, " ")
	if strings.Count(joined, "CODEX_HOME=") != 1 || !strings.Contains(joined, "CODEX_HOME=/overlay") {
		t.Errorf("env = %v", got)
	}
	if !strings.Contains(joined, "A=1") || !strings.Contains(joined, "B=2") {
		t.Errorf("unrelated variables lost: %v", got)
	}
}

func TestLaunchRunsCleanupAndReturnsExitCode(t *testing.T) {
	sh, err := lookSh()
	if err != nil {
		t.Skip(err)
	}
	cleaned := false
	err = Launch([]string{sh, "-c", "exit 7"}, nil, func() { cleaned = true })
	var code ExitCode
	if !errors.As(err, &code) || code != 7 {
		t.Errorf("err = %v, want exit code 7", err)
	}
	if !cleaned {
		t.Error("the cleanup did not run")
	}
}

// A child killed by a signal reports -1; the caller's $? should read 128+n.
func TestLaunchMapsSignalToShellCode(t *testing.T) {
	sh, err := lookSh()
	if err != nil {
		t.Skip(err)
	}
	err = Launch([]string{sh, "-c", "kill -TERM $$"}, nil, func() {})
	var code ExitCode
	if !errors.As(err, &code) || code != 128+15 {
		t.Errorf("err = %v, want 143", err)
	}
}

func TestLaunchMissingBinaryStillCleansUp(t *testing.T) {
	cleaned := false
	if err := Launch([]string{"/nonexistent/agent"}, nil, func() { cleaned = true }); err == nil {
		t.Fatal("expected an error")
	}
	if !cleaned {
		t.Error("a rewritten project file would be left behind")
	}
}

func TestPidAlive(t *testing.T) {
	if !PidAlive(os.Getpid()) {
		t.Error("this process is alive")
	}
	if PidAlive(0) || PidAlive(-1) {
		t.Error("non-positive pids are never alive")
	}
}

func TestShellQuote(t *testing.T) {
	got := strings.Join(ShellQuote([]string{"claude", "-p", "hello world", "<rendered config>", "it's"}), " ")
	want := `claude -p 'hello world' <rendered config> 'it'\''s'`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
