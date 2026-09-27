package catalog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/cajbecu/mcpick/internal/fsutil"
)

// Moving a server between the groups the picker shows — project (the
// catalog file), local and user (both in ~/.claude.json) — is an add to
// the destination followed by a delete from the source, in that order: a
// failure in between leaves the server in both places, never in neither.
// The plugin group is read-only, from and to. The spec is written as it is
// in the source, placeholders included; whether to redact a credential first
// is the caller's question to ask, since only it can ask the user.
//
// The files are shared with other sessions and with the user's editor, so
// the move trusts nothing it loaded: the source is re-read and has to be
// what the picker showed, before anything is written and again under the
// source file's lock before it is deleted; the destination is checked free
// under its own lock as the server is added; and the source is deleted
// only once re-reading the destination shows the server there.

// Writable are the origins a server can be moved between, in the order the
// picker lists them.
var Writable = []string{OriginProject, OriginLocal, OriginUser}

// Moved says what Move did, for the status line and the command line.
type Moved struct {
	Name     string
	From, To string // origins
	// FromFile and ToFile are the files edited, for display.
	FromFile, ToFile string
	// Disabled is set when the server is disabled in Claude Code: the entry
	// in disabledMcpServers is keyed by name, which the move keeps, so it
	// stays disabled and the entry is left where it is.
	Disabled bool
	// Placeholders is set when the spec written carries ${VAR} references,
	// which Claude Code expands only if the variable is set when it starts.
	Placeholders bool
}

// Describe is the one-line account of the move.
func (m Moved) Describe() string {
	return fmt.Sprintf("moved %s from %s (%s) to %s (%s)", m.Name,
		m.From, fsutil.ShortenHome(m.FromFile), m.To, fsutil.ShortenHome(m.ToFile))
}

// Notes are what the user should know after the move, one line each.
func (m Moved) Notes() []string {
	var out []string
	if m.Disabled {
		out = append(out, m.Name+" is disabled in Claude Code and stays so: its disabledMcpServers entry was kept")
	}
	if m.Placeholders && m.To != OriginProject {
		out = append(out, m.Name+" uses ${VAR} placeholders: Claude Code expands them only if the variable is set when it starts")
	}
	return out
}

// SplitError is a move whose add succeeded and whose delete did not: the
// server now exists in both files, and the user has to remove one by hand.
type SplitError struct {
	Name        string
	Added, Kept string // files
	Err         error
}

func (e *SplitError) Error() string {
	return fmt.Sprintf("%s was added to %s but could not be removed from %s: %v; it now exists in both",
		e.Name, fsutil.ShortenHome(e.Added), fsutil.ShortenHome(e.Kept), e.Err)
}

func (e *SplitError) Unwrap() error { return e.Err }

// Destinations are the groups the server named name can be moved to: the
// writable ones other than its own, or nothing for a plugin server.
func (c *Catalog) Destinations(name string) []string {
	s, ok := c.Find(name)
	if !ok || s.Origin == OriginPlugin {
		return nil
	}
	var out []string
	for _, o := range Writable {
		if o != s.Origin {
			out = append(out, o)
		}
	}
	return out
}

// CanMove says whether the server named name may be moved to origin to, and
// why not: a plugin server, an unknown or read-only destination, its own
// group, or a name already taken there.
func (c *Catalog) CanMove(name, to string) error {
	s, ok := c.Find(name)
	if !ok {
		return fmt.Errorf("no server named %q in the catalog", name)
	}
	if s.Origin == OriginPlugin {
		return fmt.Errorf("%s comes from a plugin; plugin servers cannot be moved", name)
	}
	if !slices.Contains(Writable, to) {
		return fmt.Errorf("cannot move to %q: the groups are %s", to, strings.Join(Writable, ", "))
	}
	if to == s.Origin {
		return fmt.Errorf("%s is already in %s", name, to)
	}
	_, taken, err := c.defined(name, to, c.Path)
	if err != nil {
		return err
	}
	if taken {
		if to == OriginProject {
			return fmt.Errorf("%s already exists in %s", name, fsutil.ShortenHome(c.Path))
		}
		return fmt.Errorf("%s already exists in %s mcpServers of %s", name, to, fsutil.ShortenHome(c.ClaudePath))
	}
	return nil
}

// defined reads the server named name as origin defines it now: from file
// for project (the catalog file it lives in), from ~/.claude.json for
// local and user. The spec comes back the way Load reads it, so
// it compares with what the picker holds.
func (c *Catalog) defined(name, origin, file string) (map[string]any, bool, error) {
	if origin == OriginProject {
		_, specs, _, err := ReadFile(file)
		if err != nil {
			return nil, false, err
		}
		sp, ok := specs[name]
		return sp, ok, nil
	}
	top, err := readClaudeJSON(c.ClaudePath)
	if err != nil {
		return nil, false, err
	}
	raw := claudeServers(top, c.ProjectKey, origin)
	servers := map[string]map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, false, fmt.Errorf("%s: %s mcpServers: %w", fsutil.ShortenHome(c.ClaudePath), origin, err)
		}
	}
	sp, ok := servers[name]
	return sp, ok, nil
}

// unchanged checks that the source entry on disk is still what the picker
// loaded; when it is not, nothing is moved (changedError).
func (c *Catalog) unchanged(s Server, file string) error {
	now, ok, err := c.defined(s.Name, s.Origin, file)
	if err != nil {
		return err
	}
	if !ok || !reflect.DeepEqual(now, s.Spec) {
		return changedError(s.Name)
	}
	return nil
}

// Move moves the server named name to origin to, writing sp as its spec —
// the spec as the catalog holds it, or a redacted copy of it. The add comes
// first and is atomic; a delete that then fails is a SplitError. The catalog
// in memory is not touched: the caller reloads or re-places the entry.
func (c *Catalog) Move(name, to string, sp map[string]any) (Moved, error) {
	if err := c.CanMove(name, to); err != nil {
		return Moved{}, err
	}
	s, _ := c.Find(name)
	m := Moved{Name: name, From: s.Origin, To: to, Disabled: s.Disabled, Placeholders: hasPlaceholder(sp)}
	switch s.Origin {
	case OriginProject:
		// The entry is deleted from the file it was read from, which need
		// not be the file mcpick writes to: both .mcp.yaml and .mcp.json
		// are read.
		m.FromFile = s.Source
		if m.FromFile == "" {
			m.FromFile = c.Path
		}
	default:
		m.FromFile = c.ClaudePath
	}
	// Before anything is written: a picker left open while the entry was
	// edited elsewhere would otherwise move a definition nobody looked at.
	if err := c.unchanged(s, m.FromFile); err != nil {
		return Moved{}, err
	}
	// A source entry that holds a YAML anchor used elsewhere cannot be
	// deleted; found now, the move is refused before the copy is written.
	if s.Origin == OriginProject {
		if err := deletable(m.FromFile, name); err != nil {
			return Moved{}, err
		}
	}

	switch to {
	case OriginProject:
		m.ToFile = c.Path
		if err := addServer(c.Path, name, sp, false); err != nil {
			return Moved{}, err
		}
	default:
		m.ToFile = c.ClaudePath
		if err := AddToClaudeJSON(c.ClaudePath, c.ProjectKey, name, to, sp); err != nil {
			return Moved{}, err
		}
	}
	// The destination is read back before the source goes: an add that
	// landed nowhere a reader looks must not cost the only copy.
	if _, ok, err := c.defined(name, to, c.Path); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("%s is not in %s after adding it", name, fsutil.ShortenHome(m.ToFile))
		}
		return Moved{}, fmt.Errorf("%w; %s was left in %s", err, name, fsutil.ShortenHome(m.FromFile))
	}

	var err error
	switch s.Origin {
	case OriginProject:
		err = deleteServer(m.FromFile, name, s.Spec)
	default:
		err = deleteFromClaudeJSON(c.ClaudePath, c.ProjectKey, name, s.Origin, s.Spec)
	}
	if err != nil {
		return m, &SplitError{Name: name, Added: m.ToFile, Kept: m.FromFile, Err: err}
	}
	return m, nil
}

// hasPlaceholder says whether any string in the spec carries a ${VAR}
// reference.
func hasPlaceholder(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, "${")
	case map[string]any:
		for _, val := range t {
			if hasPlaceholder(val) {
				return true
			}
		}
	case []any:
		for _, val := range t {
			if hasPlaceholder(val) {
				return true
			}
		}
	}
	return false
}
