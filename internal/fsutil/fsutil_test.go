package fsutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Every Claude session rewrites this file when it exits, so two writers is a
// real case.
func TestLockFileIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	unlock, err := Lock(path, 50*time.Millisecond, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path, 50*time.Millisecond, time.Minute); err == nil {
		t.Fatal("a second lock must not be granted")
	}
	unlock()
	unlock2, err := Lock(path, 50*time.Millisecond, time.Minute)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	unlock2()
}

func TestLockFileStealsStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	lock := path + ".mcpick-lock"
	write(t, lock, "999999\n")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(path, 50*time.Millisecond, time.Minute)
	if err != nil {
		t.Fatalf("a lock left behind by a dead process must not block forever: %v", err)
	}
	unlock()
}

func TestSanitizeKeepsPathsFlat(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "a/b", "..", "", "with space"} {
		got := Sanitize(in)
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." {
			t.Errorf("Sanitize(%q) = %q, which can still escape a directory", in, got)
		}
	}
}

func TestPruneRuntimeRemovesOldRenders(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.json")
	fresh := filepath.Join(dir, "fresh.json")
	write(t, old, "{}")
	write(t, fresh, "{}")
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	PruneRuntime(dir, 12*time.Hour)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a stale render holding expanded secrets must be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent render belongs to a running session and must stay")
	}
}

func TestRuntimeDirIsPrivate(t *testing.T) {
	home := filepath.Join(t.TempDir(), "h")
	t.Setenv("MCPICK_HOME", home)
	dir, err := RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if unixPerms && fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", fi.Mode().Perm())
	}
	// The home above it holds tokens and must be private as well.
	hi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if unixPerms && hi.Mode().Perm() != 0o700 {
		t.Errorf("home mode = %v, want 0700", hi.Mode().Perm())
	}
	if dir != filepath.Join(home, "run") {
		t.Errorf("runtime dir = %s, want %s/run", dir, home)
	}
}

// One knob: --home beats $MCPICK_HOME beats ~/.mcpick, and every
// subdirectory follows it.
func TestHomePrecedence(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("USERPROFILE", user)
	t.Setenv("MCPICK_HOME", "")
	defer SetMCPickHome("")

	SetMCPickHome("")
	if got := MCPickHome(); got != filepath.Join(user, ".mcpick") {
		t.Errorf("default home = %s", got)
	}
	env := t.TempDir()
	t.Setenv("MCPICK_HOME", env)
	if got := MCPickHome(); got != env {
		t.Errorf("env home = %s, want %s", got, env)
	}
	flag := t.TempDir()
	SetMCPickHome(flag)
	if got := MCPickHome(); got != flag {
		t.Errorf("flag home = %s, want %s", got, flag)
	}
	if StateDir() != filepath.Join(flag, "state") || SelectionsDir() != filepath.Join(flag, "selections") {
		t.Error("subdirectories must follow the home")
	}
	SetMCPickHome("~/elsewhere")
	if got := MCPickHome(); got != filepath.Join(user, "elsewhere") {
		t.Errorf("~ not expanded: %s", got)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A render named for a session that has ended holds secrets nobody needs; it
// goes on the next run, not after a day.
func TestPruneRuntimeRemovesDeadSessionsNow(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn a child: %v", err)
	}
	dead := filepath.Join(dir, RuntimeName(cmd.Process.Pid, "box-claude.json"))
	live := filepath.Join(dir, RuntimeName(os.Getpid(), "box-claude.json"))
	liveDir := filepath.Join(dir, RuntimeName(os.Getpid(), "box-home-codex"))
	write(t, dead, "{}")
	write(t, live, "{}")
	if err := os.MkdirAll(liveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(liveDir, old, old); err != nil {
		t.Fatal(err)
	}

	PruneRuntime(dir, 24*time.Hour)

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Error("a dead session's render survived")
	}
	if _, err := os.Stat(live); err != nil {
		t.Error("a live session's render was removed")
	}
	if _, err := os.Stat(liveDir); err != nil {
		t.Error("a live session's overlay was removed for being old; the agent reads through it")
	}
}

func TestWriteFileAtomicFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "claude.json")
	write(t, real, "old")
	link := filepath.Join(dir, ".claude.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := WriteFileAtomic(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file; the user's dotfiles are now detached")
	}
	if got, _ := os.ReadFile(real); string(got) != "new" {
		t.Errorf("target = %q, want the write to land there", got)
	}
}

func TestRuntimeDirRefusesSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MCPICK_HOME", home)
	if err := os.Symlink(t.TempDir(), filepath.Join(home, "run")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := RuntimeDir(); err == nil {
		t.Fatal("a symlink standing in for the runtime dir must be refused")
	}
}

// unixPerms says whether mode bits mean anything here. Windows reports 0666 or
// 0777 whatever was asked for; access there is governed by per-user ACLs.
var unixPerms = runtime.GOOS != "windows"

// ~/.mcpick is often shared between containers, whose pids mean nothing to
// each other. A dead-looking pid from another host may be a live agent there.
func TestPruneRuntimeLeavesOtherHostsAlone(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "1.deadbeef.box-claude.json") // pid 1 on another host
	foreignDir := filepath.Join(dir, "1.deadbeef.box-home-codex")
	write(t, foreign, "{}")
	if err := os.MkdirAll(foreignDir, 0o700); err != nil {
		t.Fatal(err)
	}
	PruneRuntime(dir, 24*time.Hour)
	if _, err := os.Stat(foreign); err != nil {
		t.Error("another host's fresh render was removed")
	}
	if _, err := os.Stat(foreignDir); err != nil {
		t.Error("another host's overlay was removed")
	}

	week := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(foreignDir, week, week); err != nil {
		t.Fatal(err)
	}
	PruneRuntime(dir, 24*time.Hour)
	if _, err := os.Stat(foreignDir); !os.IsNotExist(err) {
		t.Error("an overlay a week old from another host is debris and should go")
	}
}

func TestRuntimeNameRoundTrip(t *testing.T) {
	pid, host, ok := ParseRuntimeName(RuntimeName(4242, "box-1-claude.json"))
	if !ok || pid != 4242 || host != HostTag() {
		t.Errorf("parsed %d %s %v", pid, host, ok)
	}
	if _, _, ok := ParseRuntimeName("box-1-config.json"); ok {
		t.Error("a name without a pid must not parse")
	}
}
