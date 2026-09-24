package target

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// homeTarget reaches an agent that lets an environment variable relocate its
// whole config directory. mcpick points the variable at an overlay: a shadow of
// the real directory in which every entry is a symlink back, except the one
// config file mcpick generated.
type homeTarget struct {
	Meta
	env    string // the variable that relocates the directory
	src    string // the real directory
	rel    string // the config file inside it
	topKey string // JSON key holding the servers; unused for TOML
	toml   bool
	emitFn func(spec.Selection) ([]byte, error)
}

func (h homeTarget) Info() Meta {
	in := h.Meta
	in.Summary = fmt.Sprintf("%s points at an overlay of %s; only %s is generated",
		h.env, fsutil.ShortenHome(h.src), h.rel)
	return in
}

func (h homeTarget) Emit(sel spec.Selection) ([]byte, error) { return h.emitFn(sel) }

func (h homeTarget) Preview(_ spec.Selection, argv []string) Preview {
	return Preview{
		Env:  []string{h.env + "=<overlay of " + fsutil.ShortenHome(h.src) + ">"},
		Argv: argv,
	}
}

func (h homeTarget) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	gen, err := h.emitFn(sel)
	if err != nil {
		return Plan{}, err
	}

	// The tool's real config holds far more than MCP servers, so the generated
	// block is spliced into a copy of it rather than replacing it.
	realPath := filepath.Join(h.src, filepath.FromSlash(h.rel))
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
	if err := overlay(h.src, dir, h.rel, merged, 0o600); err != nil {
		os.RemoveAll(dir)
		return Plan{}, fmt.Errorf("building %s overlay: %w", h.Name, err)
	}

	notes := []string{fmt.Sprintf("%s=%s (overlay of %s)", h.env, dir, fsutil.ShortenHome(h.src))}
	if h.env == "XDG_CONFIG_HOME" {
		notes = append(notes, "XDG_CONFIG_HOME is redirected for the whole child process, not just "+h.Name)
	}
	notes = append(notes, spec.Lossy(h.Name, sel)...)
	notes = append(notes, Leaks(h.Meta, ctx.Root, sel)...)

	return Plan{
		Argv: argv,
		Env: []string{
			h.env + "=" + dir,
			"MCPICK_CONFIG=" + filepath.Join(dir, filepath.FromSlash(h.rel)),
		},
		// The agent may create or atomically replace files while it runs — a
		// refreshed login, a new session log. Those land in the overlay, not
		// behind a symlink, and would vanish with it; syncing them back is
		// what keeps the overlay from quietly logging the user out.
		Cleanup: func() {
			for _, n := range syncBack(h.src, dir, h.rel, merged, func(current []byte) ([]byte, error) {
				return spec.Restore(current, orig, h.topKey, h.toml)
			}) {
				fmt.Fprintln(os.Stderr, "mcpick:", n)
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
		return fmt.Errorf("%w (on Windows, symlinks need Developer Mode; use --target generic or mcpick serve instead)", err)
	}
	return err
}

// syncBack moves whatever the agent created or replaced in the overlay back
// into the real directory, and carries the agent's own edits to the generated
// config back as well — minus the server block, which is put back the way it
// was. Entries that are still symlinks were written through to the real files
// and need nothing.
func syncBack(src, dst, rel string, written []byte, restore func([]byte) ([]byte, error)) []string {
	var notes []string
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
					continue
				}
				target := filepath.Join(sdir, name)
				if err := fsutil.WriteFileAtomic(target, restored, fsutil.ModeOf(target, 0o600)); err != nil {
					notes = append(notes, fmt.Sprintf("could not save edits to %s: %v", fsutil.ShortenHome(target), err))
				}
			default:
				target := filepath.Join(sdir, name)
				if err := fsutil.MoveAny(filepath.Join(odir, name), target); err != nil {
					notes = append(notes, fmt.Sprintf("could not save %s: %v", fsutil.ShortenHome(target), err))
				}
			}
		}
	}
	return notes
}
