package target

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/spec"
)

// projectTarget reaches an agent that offers no per-run override: mcpick
// rewrites the project's config file, runs the agent as a child, and puts the
// file back when it exits.
type projectTarget struct {
	Meta
	rel       string
	topKey    string
	toml      bool
	emitFn    func(spec.Selection) ([]byte, error)
	extraArgs func(spec.Selection) []string
}

func (p projectTarget) Info() Meta {
	in := p.Meta
	in.Summary = p.rel + " is rewritten for the run and restored on exit"
	return in
}

func (p projectTarget) Emit(sel spec.Selection) ([]byte, error) { return p.emitFn(sel) }

func (p projectTarget) Preview(sel spec.Selection, argv []string) Preview {
	out := Preview{Argv: argv, Note: "rewrites " + p.rel + " for the run"}
	if p.extraArgs != nil {
		if extra := p.extraArgs(sel); len(extra) > 0 {
			out.Argv = append(append([]string{argv[0]}, extra...), argv[1:]...)
		}
	}
	return out
}

func (p projectTarget) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	gen, err := p.emitFn(sel)
	if err != nil {
		return Plan{}, err
	}
	path := filepath.Join(ctx.Root, filepath.FromSlash(p.rel))

	// The record doubles as the lock: while it exists and its pid is alive,
	// another session owns this file. Two sessions rewriting it at once would
	// each "restore" the other's generated config as the original.
	rec, err := claimProjectFile(ctx, path)
	if err != nil {
		return Plan{}, err
	}

	orig, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		releaseRecord(ctx.State, path)
		return Plan{}, readErr
	}
	rec.Existed = readErr == nil
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		rec.DirExisted = true
	}
	rec.Mode = uint32(fsutil.ModeOf(path, 0o644))

	merged, err := spec.Splice(orig, gen, p.topKey, p.toml)
	if err != nil {
		releaseRecord(ctx.State, path)
		return Plan{}, fmt.Errorf("%s: %w", p.rel, err)
	}
	rec.Written = fsutil.ShortHash(string(merged))
	rec.TopKey, rec.TOML = p.topKey, p.toml

	if err := saveRecord(ctx.State, rec, orig); err != nil {
		releaseRecord(ctx.State, path)
		return Plan{}, err
	}
	if err := fsutil.WriteFileAtomic(path, merged, os.FileMode(rec.Mode)); err != nil {
		releaseRecord(ctx.State, path)
		return Plan{}, err
	}

	notes := []string{fmt.Sprintf("%s rewritten for this run, restored on exit", p.rel)}
	notes = append(notes, spec.Lossy(p.Name, sel)...)
	notes = append(notes, Leaks(p.Meta, ctx.Root, sel)...)

	plan := Plan{
		Argv: argv,
		Env:  []string{"MCPICK_CONFIG=" + path},
		Cleanup: func() {
			if note, err := restoreProjectFile(ctx.State, path); err != nil {
				fmt.Fprintf(os.Stderr, "mcpick: could not restore %s: %v (run mcpick restore)\n", p.rel, err)
			} else if note != "" {
				fmt.Fprintln(os.Stderr, "mcpick:", note)
			}
		},
		Notes: notes,
	}
	if p.extraArgs != nil {
		if extra := p.extraArgs(sel); len(extra) > 0 {
			plan.Argv = append(append([]string{argv[0]}, extra...), argv[1:]...)
		}
	}
	return plan, nil
}

// fileRecord remembers what a project file looked like before mcpick touched
// it, and what mcpick wrote, so the restore can tell its own content from edits
// the agent or the user made during the run.
type fileRecord struct {
	Path string `json:"path"`
	PID  int    `json:"pid"`
	// Host scopes PID: workspaces and home directories are routinely shared
	// between containers, whose pids mean nothing to each other.
	Host string `json:"host"`
	// Ready is set once the original is safely stored. A record that is not
	// ready belongs to a session that died before touching the file, so the
	// file must be left exactly as it is.
	Ready   bool `json:"ready"`
	Existed bool `json:"existed"`
	// DirExisted says whether the file's directory was there before; a
	// directory mcpick created for the file goes when the file does.
	DirExisted bool   `json:"dir_existed"`
	Mode       uint32 `json:"mode"`
	Written    string `json:"written"`
	TopKey     string `json:"top_key,omitempty"`
	TOML       bool   `json:"toml,omitempty"`
}

func recordPaths(state, path string) (meta, orig string) {
	base := filepath.Join(state, "restore", fsutil.ShortHash(path))
	return base + ".json", base + ".orig"
}

// claimProjectFile takes ownership of path for this session. A record left by
// a session that is no longer running is recovered first; one held by a live
// session is refused.
func claimProjectFile(ctx Ctx, path string) (fileRecord, error) {
	meta, _ := recordPaths(ctx.State, path)
	if err := os.MkdirAll(filepath.Dir(meta), 0o700); err != nil {
		return fileRecord{}, err
	}
	rec := fileRecord{Path: path, PID: ctx.PID, Host: hostname()}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(meta, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// The close is checked: this record is what a crash recovery
			// reads, and a write that fails at close never reached disk.
			err = json.NewEncoder(f).Encode(rec)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			return rec, err
		}
		if !os.IsExist(err) {
			return rec, err
		}
		old, err := loadRecord(meta)
		if err != nil {
			// Another session may have created the record a moment ago and
			// not finished writing it. A young unreadable record is a live
			// claim; only an old one is debris.
			if fi, statErr := os.Stat(meta); statErr == nil && time.Since(fi.ModTime()) < 10*time.Second {
				return rec, fmt.Errorf("%s is being claimed by another mcpick session; try again", fsutil.ShortenHome(path))
			}
		}
		if err == nil && old.Host != rec.Host {
			return rec, fmt.Errorf("%s is in use by mcpick on %s (pid %d); if that session is gone, run mcpick restore",
				fsutil.ShortenHome(path), old.Host, old.PID)
		}
		if err == nil && old.PID != ctx.PID && proc.PidAlive(old.PID) {
			return rec, fmt.Errorf("%s is in use by another mcpick session (pid %d)",
				fsutil.ShortenHome(path), old.PID)
		}
		if _, err := restoreProjectFile(ctx.State, path); err != nil {
			return rec, fmt.Errorf("recovering %s from a crashed session: %w", fsutil.ShortenHome(path), err)
		}
	}
	return rec, fmt.Errorf("could not claim %s", fsutil.ShortenHome(path))
}

func loadRecord(meta string) (fileRecord, error) {
	var rec fileRecord
	data, err := os.ReadFile(meta)
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(data, &rec)
	return rec, err
}

func saveRecord(state string, rec fileRecord, orig []byte) error {
	meta, origPath := recordPaths(state, rec.Path)
	rec.Ready = true
	if rec.Existed {
		if err := fsutil.WriteFileAtomic(origPath, orig, 0o600); err != nil {
			return err
		}
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(meta, append(data, '\n'), 0o600)
}

func releaseRecord(state, path string) {
	meta, orig := recordPaths(state, path)
	os.Remove(orig)
	os.Remove(meta)
}

// restoreProjectFile undoes one rewrite. When the file still holds exactly
// what mcpick wrote, the original comes back byte for byte. When the agent or
// the user changed it during the run, their edits are kept and only the server
// block is put back.
func restoreProjectFile(state, path string) (string, error) {
	meta, origPath := recordPaths(state, path)
	rec, err := loadRecord(meta)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		// A record nobody can read cannot be acted on; dropping it is the
		// only way the file ever becomes usable again.
		releaseRecord(state, path)
		return "", fmt.Errorf("unreadable restore record: %w", err)
	}
	if !rec.Ready {
		releaseRecord(state, path)
		return "", nil
	}
	var orig []byte
	if rec.Existed {
		if orig, err = os.ReadFile(origPath); err != nil {
			return "", fmt.Errorf("the backup of %s is missing: %w", fsutil.ShortenHome(path), err)
		}
	}

	note := ""
	current, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if rec.Existed {
			err = fsutil.WriteFileAtomic(path, orig, os.FileMode(rec.Mode))
		} else {
			err = nil
		}
	case err != nil:
		return "", err
	case fsutil.ShortHash(string(current)) == rec.Written:
		if rec.Existed {
			err = fsutil.WriteFileAtomic(path, orig, os.FileMode(rec.Mode))
		} else {
			err = os.Remove(path)
		}
	default:
		var restored []byte
		restored, err = spec.Restore(current, orig, rec.TopKey, rec.TOML)
		if err == nil {
			if !rec.Existed && isEmptyConfig(restored, rec.TOML) {
				err = os.Remove(path)
			} else {
				err = fsutil.WriteFileAtomic(path, restored, os.FileMode(rec.Mode))
				note = fmt.Sprintf("%s was edited during the run; kept the edits, restored the servers", fsutil.ShortenHome(path))
			}
		}
	}
	if err != nil {
		return "", err
	}
	if !rec.Existed && !rec.DirExisted {
		os.Remove(filepath.Dir(path)) // only succeeds while empty
	}
	releaseRecord(state, path)
	return note, nil
}

func isEmptyConfig(data []byte, toml bool) bool {
	trimmed := bytes.TrimSpace(data)
	if toml {
		return len(trimmed) == 0
	}
	return len(trimmed) == 0 || string(trimmed) == "{}" || string(trimmed) == "{\n}"
}

// RecoverStale restores every project file whose session died without cleaning
// up. Records from another host are only touched when anyHost is set — that is
// `mcpick restore`, an explicit request; a launch cannot tell whether a session
// in another container is still running — killed with SIGKILL, a closed laptop lid, a crashed container. It runs
// before every launch, so a crash costs one launch's worth of stale config,
// not a permanently rewritten file.
func RecoverStale(state string, anyHost bool) []string {
	dir := filepath.Join(state, "restore")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		rec, err := loadRecord(filepath.Join(dir, n))
		if err != nil || rec.Path == "" {
			continue
		}
		foreign := rec.Host != hostname()
		if (foreign && !anyHost) || (!foreign && proc.PidAlive(rec.PID)) {
			continue
		}
		if note, err := restoreProjectFile(state, rec.Path); err != nil {
			out = append(out, fmt.Sprintf("could not restore %s: %v", fsutil.ShortenHome(rec.Path), err))
		} else {
			out = append(out, "restored "+fsutil.ShortenHome(rec.Path)+" after a session that did not exit cleanly")
			if note != "" {
				out = append(out, note)
			}
		}
	}
	return out
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}
