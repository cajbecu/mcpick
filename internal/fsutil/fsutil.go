// Package fsutil holds the file-system primitives the rest of mcpick relies on
// for not losing or leaking data: atomic writes that respect symlinks, the
// private runtime directory, the state directory, and lock files.
package fsutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cajbecu/mcpick/internal/proc"
)

// homeOverride is set from --home; it wins over $MCPICK_HOME.
var homeOverride string

// SetMCPickHome makes dir mcpick's home for this process: the --home flag.
func SetMCPickHome(dir string) { homeOverride = dir }

// MCPickHome is the one directory mcpick keeps its files in: --home, else
// $MCPICK_HOME, else ~/.mcpick. Everything else is a subdirectory of it, so
// there is one place to find, back up, mount into a container or delete.
//
//	selections/  what the user picked, per workspace and --uid
//	state/       OAuth tokens, measurements, restore records
//	run/         rendered configs and overlays; secrets, removed when done
func MCPickHome() string {
	switch {
	case homeOverride != "":
		return expandHome(homeOverride)
	case os.Getenv("MCPICK_HOME") != "":
		return expandHome(os.Getenv("MCPICK_HOME"))
	}
	if h := Home(".mcpick"); h != "" {
		return h
	}
	return filepath.Join(os.TempDir(), "mcpick-"+strconv.Itoa(os.Getuid()))
}

func expandHome(p string) string {
	if p == "~" {
		return Home()
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return Home(rest)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// SelectionsDir holds the saved selections.
func SelectionsDir() string { return filepath.Join(MCPickHome(), "selections") }

// StateDir holds what mcpick keeps between runs: OAuth tokens, measurements
// and restore records.
func StateDir() string { return filepath.Join(MCPickHome(), "state") }

// RuntimeDir is the private directory for files that hold expanded secrets:
// rendered configs and overlays. It is created 0700 — and so is the home
// above it — and refused if it is a symlink or another user's, so a
// directory planted in advance cannot collect secrets.
func RuntimeDir() (string, error) {
	home := MCPickHome()
	if err := privateDir(home); err != nil {
		return "", err
	}
	run := filepath.Join(home, "run")
	if err := privateDir(run); err != nil {
		return "", err
	}
	return run, nil
}

// EnsureHome creates mcpick's home, private to the user, if it is not there.
func EnsureHome() error { return privateDir(MCPickHome()) }

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if !ownedByMe(fi) {
		return fmt.Errorf("%s belongs to another user; refusing to write secrets into it", dir)
	}
	return os.Chmod(dir, 0o700)
}

// Home joins parts under the user's home directory.
func Home(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{home}, parts...)...)
}

// XDGConfigHome is $XDG_CONFIG_HOME, else ~/.config.
func XDGConfigHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return Home(".config")
}

// ShortHash is a stable 12-hex-digit fingerprint, used to key files by a path
// or a spec without putting the path itself in a file name.
func ShortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// Sanitize reduces s to a safe single path component.
func Sanitize(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		}
		return '-'
	}, s)
	if out == "" {
		return "default"
	}
	return out
}

// ShortenHome replaces the home directory prefix with ~ for display.
func ShortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home+string(filepath.Separator)) {
		return path
	}
	return "~" + strings.TrimPrefix(path, home)
}

// ModeOf returns path's permission bits, or fallback when it does not exist.
func ModeOf(path string, fallback os.FileMode) os.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return fallback
}

// WriteFileAtomic replaces path with data via a rename, so a reader never sees
// half a file. When path is a symlink — dotfile managers make ~/.claude.json
// one — the write goes to the file it points at; renaming over the link would
// silently replace it with a regular file.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcpick-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RuntimeName names one launch's artefact in the runtime directory:
//
//	<pid>.<host>.<name>
//
// The pid says when the file can go: exec keeps it, so it names the agent for
// as long as the agent runs. The host scopes the pid, because ~/.mcpick is
// often shared between containers whose pids mean nothing to each other.
func RuntimeName(pid int, name string) string {
	return fmt.Sprintf("%d.%s.%s", pid, HostTag(), name)
}

// HostTag is a short, file-name-safe stand-in for this host's name.
func HostTag() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "unknown"
	}
	return ShortHash(h)[:8]
}

// ParseRuntimeName reads back what RuntimeName wrote.
func ParseRuntimeName(name string) (pid int, host string, ok bool) {
	parts := strings.SplitN(name, ".", 3)
	if len(parts) != 3 {
		return 0, "", false
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid <= 0 {
		return 0, "", false
	}
	return pid, parts[1], true
}

// PruneRuntime removes artefacts whose session is over. Rendered configs hold
// expanded secrets and the process that wrote them is replaced by the agent,
// so nothing else cleans them up.
//
// On this host a dead pid means the entry can go at once. An entry from
// another host sharing the directory cannot be checked, so it goes only when
// it is older than maxAge — for a directory, a week, since an agent there may
// still be reading through it. Files are also dropped after maxAge whatever
// their owner, in case a pid was reused.
func PruneRuntime(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	here := HostTag()
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		pid, host, named := ParseRuntimeName(e.Name())
		if named && host == here && !proc.PidAlive(pid) {
			os.RemoveAll(path)
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		age := time.Since(info.ModTime())
		switch {
		case !e.IsDir() && age > maxAge:
			os.Remove(path)
		case e.IsDir() && named && host != here && age > 7*24*time.Hour:
			os.RemoveAll(path)
		}
	}
}

// Lock takes a best-effort exclusive lock next to path. A lock older than
// staleAfter is assumed abandoned.
func Lock(path string, wait, staleAfter time.Duration) (func(), error) {
	lock := path + ".mcpick-lock"
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(lock) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if fi, statErr := os.Stat(lock); statErr == nil && time.Since(fi.ModTime()) > staleAfter {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s is locked by another process", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// MoveAny renames src over dst, falling back to copy-and-delete when they sit
// on different file systems — the runtime dir is often a tmpfs.
func MoveAny(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := copyTree(src, dst); err != nil {
			return err
		}
	} else if err := copyFile(src, dst, fi.Mode().Perm()); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return WriteFileAtomic(dst, data, mode)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}
