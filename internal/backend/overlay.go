package backend

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// overlayBackend is the mechanism for an agent that lets an environment
// variable relocate its whole config directory. mcpick points the variable at
// an overlay: a shadow of the real directory in which every entry is a symlink
// back, except the one config file mcpick generated.
type overlayBackend struct {
	Meta
	env     string        // the variable that relocates the directory
	dir     func() string // the real directory; a function so it follows HOME
	file    string        // the config file inside it
	topKey  string        // JSON key holding the servers; unused for TOML
	toml    bool
	dialect spec.Dialect
}

func (h overlayBackend) Info() Meta {
	in := h.Meta
	in.Summary = fmt.Sprintf("%s points at an overlay of %s; only %s is generated",
		h.env, fsutil.ShortenHome(h.dir()), h.file)
	return in
}

func (h overlayBackend) Dialect() spec.Dialect { return h.dialect }

func (h overlayBackend) Preview(_ spec.Selection, argv []string) Preview {
	return Preview{
		Env:  []string{h.env + "=<overlay of " + fsutil.ShortenHome(h.dir()) + ">"},
		Argv: argv,
	}
}

func (h overlayBackend) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	src := h.dir()
	gen, err := h.dialect.Emit(sel)
	if err != nil {
		return Plan{}, err
	}

	// The tool's real config holds far more than MCP servers, so the generated
	// block is spliced into a copy of it rather than replacing it.
	realPath := filepath.Join(src, filepath.FromSlash(h.file))
	orig, err := os.ReadFile(realPath)
	if err != nil && !os.IsNotExist(err) {
		return Plan{}, err
	}
	merged, err := spec.Splice(orig, gen, h.topKey, h.toml)
	if err != nil {
		return Plan{}, fmt.Errorf("%s: %w", fsutil.ShortenHome(realPath), err)
	}

	dir := filepath.Join(ctx.Runtime, runtimeName(ctx, "home-"+h.Name))
	if err := os.RemoveAll(dir); err != nil {
		return Plan{}, err
	}
	if err := overlay(src, dir, h.file, merged, 0o600); err != nil {
		os.RemoveAll(dir)
		return Plan{}, fmt.Errorf("building %s overlay: %w", h.Name, err)
	}

	notes := []string{fmt.Sprintf("%s=%s (overlay of %s)", h.env, dir, fsutil.ShortenHome(src))}
	if h.env == "XDG_CONFIG_HOME" {
		notes = append(notes, "XDG_CONFIG_HOME is redirected for the whole child process, not just "+h.Name)
	}
	notes = append(notes, lossy(h.dialect, sel)...)
	notes = append(notes, Leaks(h.Meta, ctx.Root, sel)...)

	return Plan{
		Argv: argv,
		Env: []string{
			h.env + "=" + dir,
			"MCPICK_CONFIG=" + filepath.Join(dir, filepath.FromSlash(h.file)),
		},
		// The agent may create or atomically replace files while it runs — a
		// refreshed login, a new session log. Those land in the overlay, not
		// behind a symlink, and would vanish with it; syncing them back is
		// what keeps the overlay from quietly logging the user out. A file
		// that cannot be synced back is kept (see keepUnsynced), never
		// removed with the overlay: it may be the only copy of a login.
		Cleanup: func() {
			notes, failed := syncBack(src, dir, h.file, merged, func(current []byte) ([]byte, error) {
				return spec.Restore(current, orig, h.topKey, h.toml)
			})
			for _, n := range notes {
				fmt.Fprintln(os.Stderr, "mcpick:", n)
			}
			if len(failed) > 0 {
				kept, err := keepUnsynced(ctx.State, dir, failed)
				if err != nil {
					fmt.Fprintf(os.Stderr, "mcpick: could not set the unsaved files aside (%v); the overlay is kept at %s\n", err, dir)
					return
				}
				fmt.Fprintf(os.Stderr, "mcpick: the files that could not be saved are in %s (not removed automatically)\n", fsutil.ShortenHome(kept))
			}
			os.RemoveAll(dir)
		},
		Notes: notes,
	}, nil
}

// overlay builds a shadow of src at dst in which every entry is a symlink back
// into src, except the file at rel, which is written from data. Directories
// along rel are recreated as real directories whose siblings are symlinked, so
// a tool pointed at dst still finds its credentials, history and sessions while
// reading the config mcpick generated.
func overlay(src, dst, rel string, data []byte, mode os.FileMode) error {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("overlay: empty relative path")
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if e.Name() == parts[0] {
			continue
		}
		if err := os.Symlink(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return symlinkHint(err)
		}
	}

	if len(parts) == 1 {
		return fsutil.WriteFileAtomic(filepath.Join(dst, parts[0]), data, mode)
	}
	return overlay(filepath.Join(src, parts[0]), filepath.Join(dst, parts[0]),
		strings.Join(parts[1:], "/"), data, mode)
}

func symlinkHint(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w (on Windows, symlinks need Developer Mode; use --agent generic or mcpick serve instead)", err)
	}
	return err
}

// syncBack moves whatever the agent created or replaced in the overlay back
// into the real directory, and carries the agent's own edits to the generated
// config back as well — minus the server block, which is put back the way it
// was. Entries that are still symlinks were written through to the real files
// and need nothing. It returns what it has to say, and the entries — paths
// relative to the overlay — it could not bring back, which the caller has
// to keep.
func syncBack(src, dst, rel string, written []byte, restore func([]byte) ([]byte, error)) (notes, failed []string) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for depth := range parts {
		sdir := filepath.Join(append([]string{src}, parts[:depth]...)...)
		odir := filepath.Join(append([]string{dst}, parts[:depth]...)...)
		entries, err := os.ReadDir(odir)
		if err != nil {
			continue
		}
		last := depth == len(parts)-1
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := e.Name()
			inOverlay := filepath.Join(append(append([]string{}, parts[:depth]...), name)...)
			switch {
			case !last && name == parts[depth]:
				continue // a directory mcpick recreated; walked at the next depth
			case last && name == parts[depth]:
				current, err := os.ReadFile(filepath.Join(odir, name))
				if err != nil || bytes.Equal(current, written) {
					continue
				}
				restored, err := restore(current)
				if err != nil {
					notes = append(notes, fmt.Sprintf("kept %s's edits to %s out: %v", filepath.Base(src), name, err))
					failed = append(failed, inOverlay)
					continue
				}
				target := filepath.Join(sdir, name)
				if err := fsutil.WriteFileAtomic(target, restored, fsutil.ModeOf(target, 0o600)); err != nil {
					notes = append(notes, fmt.Sprintf("could not save edits to %s: %v", fsutil.ShortenHome(target), err))
					failed = append(failed, inOverlay)
				}
			default:
				target := filepath.Join(sdir, name)
				if err := fsutil.MoveAny(filepath.Join(odir, name), target); err != nil {
					notes = append(notes, fmt.Sprintf("could not save %s: %v", fsutil.ShortenHome(target), err))
					failed = append(failed, inOverlay)
				}
			}
		}
	}
	return notes, failed
}

// keepUnsynced sets aside the overlay entries that could not be synced
// back — a refreshed login, a session log, the agent's edits to its config
// — under <state>/recovered/<timestamp>/, keeping their paths, and returns
// that directory. They may hold credentials, so the directory is private,
// like the state directory it is in. Nothing removes them: the state
// directory is not pruned, and only the user knows what they are worth.
func keepUnsynced(state, overlay string, failed []string) (string, error) {
	dest := filepath.Join(state, "recovered", time.Now().UTC().Format("20060102T150405.000000000Z"))
	for _, rel := range failed {
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return dest, err
		}
		if err := fsutil.MoveAny(filepath.Join(overlay, rel), target); err != nil {
			return dest, err
		}
	}
	return dest, nil
}
