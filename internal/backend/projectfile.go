package backend

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/spec"
)

// projectBackend is the mechanism for an agent that offers no per-run
// override: mcpick rewrites the project's config file, runs the agent as a
// child, and puts the file back when it exits.
type projectBackend struct {
	Meta
	file    string // the config file, relative to the project root
	topKey  string // JSON key holding the servers; unused for TOML
	toml    bool
	dialect spec.Dialect
	// extraArgs are flags the agent needs besides the file, if any; argv is
	// the user's command line, for an agent whose subcommands refuse them.
	extraArgs func(spec.Selection, []string) []string
	// caveat, when set, is a limit of the mechanism for this agent that the
	// user has to know before relying on the selection; it is shown in the
	// picker's command line and said again at launch.
	caveat string
	// envRefs marks an agent that expands ${VAR} in a server's env values
	// itself: those are written as the catalog wrote them (see keepEnvRefs),
	// so the secret behind a reference never reaches the project file.
	envRefs bool
}

func (p projectBackend) Info() Meta {
	in := p.Meta
	in.Summary = p.file + " is rewritten for the run and restored on exit"
	return in
}

func (p projectBackend) Dialect() spec.Dialect { return p.dialect }

func (p projectBackend) Preview(sel spec.Selection, argv []string) Preview {
	out := Preview{Argv: argv, Note: "rewrites " + p.file + " for the run"}
	if p.caveat != "" {
		out.Note += "; " + p.caveat
	}
	if p.extraArgs != nil {
		if extra := p.extraArgs(sel, argv); len(extra) > 0 {
			out.Argv = append(append([]string{argv[0]}, extra...), argv[1:]...)
		}
	}
	return out
}

// The project file sits in the repository and holds the selection
// expanded, secrets included, so it is treated like a rendered config: one
// mcpick creates is 0600 in a directory it creates 0700; one that exists
// and that others can read is 0600 for the run, the user is told, and its
// own mode comes back with its contents — on exit, or by RecoverStale
// after a crash (the record keeps it).
func (p projectBackend) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	if p.envRefs {
		sel = keepEnvRefs(sel)
	}
	gen, err := p.dialect.Emit(sel)
	if err != nil {
		return Plan{}, err
	}
	path := filepath.Join(ctx.Root, filepath.FromSlash(p.file))

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
	} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		releaseRecord(ctx.State, path)
		return Plan{}, err
	}
	rec.Mode = uint32(fsutil.ModeOf(path, 0o600))

	merged, err := spec.Splice(orig, gen, p.topKey, p.toml)
	if err != nil {
		releaseRecord(ctx.State, path)
		return Plan{}, fmt.Errorf("%s: %w", p.file, err)
	}
	rec.Written = fsutil.ShortHash(string(merged))
	rec.TopKey, rec.TOML = p.topKey, p.toml

	if err := saveRecord(ctx.State, rec, orig); err != nil {
		releaseRecord(ctx.State, path)
		return Plan{}, err
	}
	runMode := os.FileMode(rec.Mode)
	if runMode&0o077 != 0 {
		runMode = 0o600
	}
	if err := fsutil.WriteFileAtomic(path, merged, runMode); err != nil {
		releaseRecord(ctx.State, path)
		return Plan{}, err
	}

	notes := []string{fmt.Sprintf("%s rewritten for this run, restored on exit", p.file)}
	if p.caveat != "" {
		notes = append(notes, p.caveat)
	}
	if rec.Existed && runMode != os.FileMode(rec.Mode) {
		notes = append(notes, fmt.Sprintf("%s is mode %04o, readable by others: it is 0600 while it holds this run's servers, and gets its mode back on exit",
			p.file, rec.Mode))
	}
	notes = append(notes, lossy(p.dialect, sel)...)
	notes = append(notes, Leaks(p.Meta, ctx.Root, sel)...)

	plan := Plan{
		Argv: argv,
		Env:  []string{"MCPICK_CONFIG=" + path},
		Cleanup: func() {
			if note, err := restoreProjectFile(ctx.State, path); err != nil {
				fmt.Fprintf(os.Stderr, "mcpick: could not restore %s: %v (run mcpick restore)\n", p.file, err)
			} else if note != "" {
				fmt.Fprintln(os.Stderr, "mcpick:", note)
			}
		},
		Notes: notes,
	}
	if p.extraArgs != nil {
		if extra := p.extraArgs(sel, argv); len(extra) > 0 {
			plan.Argv = append(append([]string{argv[0]}, extra...), argv[1:]...)
		}
	}
	return plan, nil
}

// plainRef matches an env value the agent can expand exactly as mcpick
// would: literal text and `${NAME}` references only — no `$${` escape, no
// `${NAME:-default}` or `${NAME:?why}`, no bare `$NAME`, no `{UUID}`, and
// no `%NAME%`, which the agent may expand on Windows.
var plainRef = regexp.MustCompile(`^(?:[^$%{}]*\$\{[A-Za-z_][A-Za-z0-9_]*\})+[^$%{}]*$`)

// refForm matches one ${NAME}, ${NAME:-default} or ${NAME:?why} reference,
// or its $${…} escape: spec.Expand's forms.
var refForm = regexp.MustCompile(`(\$?)\$\{([A-Za-z_][A-Za-z0-9_]*)(?::([-?])[^}]*)?\}`)

// agentRefs rewrites a value as the catalog wrote it into one the agent
// can expand itself, when there is one: `${NAME:?why}` and
// `${NAME:-default}` become `${NAME}` once NAME is set and not empty —
// both then mean its value, as `${NAME}` does — and the result has to be
// plain (plainRef). An unset or empty NAME behind a default, an escape,
// {UUID} or any other form is not the agent's to expand.
func agentRefs(raw string) (string, bool) {
	ok := true
	out := refForm.ReplaceAllStringFunc(raw, func(m string) string {
		g := refForm.FindStringSubmatch(m)
		switch {
		case g[1] != "":
			ok = false
		case g[3] != "":
			if v, set := os.LookupEnv(g[2]); set && v != "" {
				return "${" + g[2] + "}"
			}
			ok = false
		}
		return m
	})
	return out, ok && plainRef.MatchString(out)
}

// keepEnvRefs returns the selection with each env value the agent can
// expand itself put back as a reference (agentRefs), so the value behind
// it is not written into the project file; every other value stays
// expanded. A rewritten one is kept only when expanding it now gives the
// value the selection holds. Nothing is known without Raw. The caller's maps are
// not touched.
func keepEnvRefs(sel spec.Selection) spec.Selection {
	if sel.Raw == nil {
		return sel
	}
	out := spec.Selection{Names: sel.Names, Specs: make(map[string]map[string]any, len(sel.Specs)), Raw: sel.Raw}
	for n, sp := range sel.Specs {
		out.Specs[n] = sp
		raw, _ := sel.Raw[n]["env"].(map[string]any)
		env, _ := sp["env"].(map[string]any)
		if len(raw) == 0 || env == nil {
			continue
		}
		kept := map[string]any{}
		for k, v := range env {
			kept[k] = v
			r, ok := raw[k].(string)
			if !ok {
				continue
			}
			ref, ok := agentRefs(r)
			if !ok {
				continue
			}
			// A rewritten form has to mean what the selection holds.
			if same, err := spec.Expand(ref, ""); ref == r || (err == nil && same == v) {
				kept[k] = ref
			}
		}
		copied := make(map[string]any, len(sp))
		for k, v := range sp {
			copied[k] = v
		}
		copied["env"] = kept
		out.Specs[n] = copied
	}
	return out
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
