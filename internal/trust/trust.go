// Package trust remembers which commands the user has agreed to run when
// measuring. Measuring a stdio server executes its command, and a repository,
// a plugin or a stale entry in ~/.claude.json can put any command there, so
// nothing runs until the user has seen the command and said yes — once, per
// project and per command. Launching is not gated here: checking a server is
// the consent to run it.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Grant records one consent. Command is the executable alone (resolved, see
// view), for a person reading the file; the arguments and the environment
// are not copied because a catalog may carry a literal secret in either,
// and the file is an audit record, not a catalog.
type Grant struct {
	Root    string `json:"root"`
	Name    string `json:"name"`
	Spec    string `json:"spec"`
	Command string `json:"command"`
	At      string `json:"at"`
}

// Store is the list of grants, kept in <home>/state/trust.json: the commands
// trusted per project and server, and the project catalogs trusted whole
// (see catalog.go).
type Store struct {
	path     string
	Grants   []Grant        `json:"grants"`
	Catalogs []CatalogGrant `json:"catalogs,omitempty"`
	// pending are the mutations made since the last Save, in order. Save
	// replays them on the file as it is then, not on the copy loaded when
	// the session began (see Save).
	pending []func(*Store)
	mu      sync.Mutex // guards pending and the replay
}

// lockWait is how long Save waits for the lock another mcpick holds;
// lockStale is when a leftover lock is ignored.
var (
	lockWait  = 2 * time.Second
	lockStale = 30 * time.Second
)

// Load reads the store from the state directory. A missing or corrupt file is
// an empty store: nothing trusted, everything asks, which is the safe way to
// fail.
func Load() *Store { return Open(filepath.Join(fsutil.StateDir(), "trust.json")) }

// Open reads the store at path; tests point it at a temporary file.
func Open(path string) *Store {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if json.Unmarshal(data, s) != nil {
		s.Grants, s.Catalogs = nil, nil
	}
	return s
}

// Path is the file the store is read from and saved to.
func (s *Store) Path() string { return s.path }

// Save writes the mutations made since the last Save — grants, catalog
// trusts and withdrawals — into the file as it is on disk now, not the copy
// loaded when the session began. The file is shared by every project and
// every mcpick session, so a picker left open for an hour must not save its
// stale snapshot over what another one did meanwhile: a catalog trust
// withdrawn there would come back. Under a lock the store is reloaded, the
// mutations are applied to the fresh one, it is written atomically (mode
// 0600 like the tokens beside it), and s takes it over. A Save that fails
// keeps the mutations pending, so the next one carries them.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	unlock, err := fsutil.Lock(s.path, lockWait, lockStale)
	if err != nil {
		return err
	}
	defer unlock()
	fresh := Open(s.path)
	for _, fn := range s.pending {
		fn(fresh)
	}
	data, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(s.path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.Grants, s.Catalogs, s.pending = fresh.Grants, fresh.Catalogs, nil
	return nil
}

// mutate applies fn to the store now and records it for Save to apply again
// to the file as it is then. Every mutation goes through it, so none can be
// lost to a stale snapshot; fn has to be safe to apply twice.
func (s *Store) mutate(fn func(*Store)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
	s.pending = append(s.pending, fn)
}

// Runs reports whether measuring the spec executes a command: a stdio server
// with a command. A remote server, or a stdio entry without a command (which
// Probe reports as "no cmd" without running anything), does not.
func Runs(sp map[string]any) bool {
	v := spec.ViewOf(sp)
	return !v.Remote() && v.Command != ""
}

// view is the spec as trust looks at it: spec.ViewOf, with a relative
// command made absolute. `./run.sh` runs relative to the directory the
// measurement runs from — the process's, since Probe sets no directory —
// while trust is keyed by project root, so the same catalog line could run a
// different file from another directory on one grant. A command with a path
// separator is therefore resolved against the working directory, and that
// path is what is shown, fingerprinted and recorded. A bare name is looked
// up in PATH and a command starting with a placeholder decides its own path
// at expansion: both stay as written.
func view(sp map[string]any) spec.View {
	v := spec.ViewOf(sp)
	c := v.Command
	if c == "" || filepath.IsAbs(c) || strings.HasPrefix(c, "$") || strings.HasPrefix(c, "{") || !hasSeparator(c) {
		return v
	}
	if dir, err := os.Getwd(); err == nil {
		v.Command = filepath.Join(dir, c)
	}
	return v
}

// hasSeparator reports whether c is a path rather than a name to look up in
// PATH: it contains a slash, or the platform's separator.
func hasSeparator(c string) bool {
	return strings.ContainsRune(c, '/') || strings.ContainsRune(c, filepath.Separator)
}

// Fingerprint identifies what a measurement would execute: the full SHA-256
// of the command (resolved, see view), its arguments and the environment it
// sets, values included — BASH_ENV, NODE_OPTIONS, LD_PRELOAD or PATH run
// code of their own. It is taken over the unexpanded catalog spec, so
// `${TOKEN}` hashes as the placeholder: rotating the secret behind it does
// not re-ask, and only the hash is ever stored. Empty for a spec that does
// not run a command.
func Fingerprint(sp map[string]any) string {
	v := view(sp)
	if v.Remote() || v.Command == "" {
		return ""
	}
	args := v.Args
	if args == nil {
		args = []string{}
	}
	data, err := json.Marshal(struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}{v.Command, args, v.Env})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Mask says how much of a description is hidden, by where it goes.
type Mask int

const (
	// MaskNone is for the views that ask for consent: the user's own
	// terminal, and consent needs the real command, arguments and
	// environment — a masked `--no-auth evil-pkg` would hide the very thing
	// being trusted.
	MaskNone Mask = iota
	// MaskEnv is for the notice --trust prints before it runs: the
	// arguments as they execute, and env values whose name looks secret
	// as ****.
	MaskEnv
	// MaskAll is for output nobody consents on — the skip warning on
	// stderr, --json, the list's detail line: credential-bearing arguments
	// are masked too.
	MaskAll
)

// Describe renders what would run, as the catalog wrote it — `${VAR}` and
// `{UUID}` unexpanded, the command resolved (see view) — for the screens
// that ask for consent, the warnings that say what was skipped and the
// notice of what --trust approves. The catalog is untrusted input, so the
// text is made safe to print before mcpick styles it: control and invisible
// characters are spelled out (Safe), so an argument cannot hide a clause
// behind a terminal escape; an argument that is not one plain ASCII word is
// quoted, so its boundaries are plain and a look-alike is not; and mask says
// what is hidden.
func Describe(sp map[string]any, mask Mask) string {
	v := view(sp)
	if v.Remote() {
		return Safe(v.URL)
	}
	args := v.Args
	if mask == MaskAll {
		args = maskCredentials(args)
	}
	parts := []string{quoteArg(v.Command)}
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	out := strings.Join(parts, " ")
	if len(v.Env) > 0 {
		var env []string
		for _, k := range spec.SortedKeys(v.Env) {
			val := quoteArg(v.Env[k])
			if mask != MaskNone && credName.MatchString(k) {
				val = "****"
			}
			env = append(env, Safe(k)+"="+val)
		}
		out += "  env: " + strings.Join(env, ", ")
	}
	return out
}

// Safe spells out every character that would not show on a terminal — ESC,
// other controls, bidi overrides and the like — in Go's escape syntax
// (`\x1b`, `\u202e`), so text from the catalog is only ever text on screen.
// Names, environment keys and reasons go through it; arguments through
// quoteArg, which uses it too.
func Safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		q := strconv.QuoteRune(r)
		b.WriteString(q[1 : len(q)-1])
	}
	return b.String()
}

// quoteArg prints an argument bare when it is one plain word — printable
// ASCII without spaces, quotes or backslashes — and as an ASCII-quoted
// string otherwise: `sh -c "a; b"` makes plain where the script starts and
// ends; the quoting escapes controls and backslashes, so a `\x1b` on screen
// is unambiguous; and it escapes every non-ASCII rune, so U+2800 or U+3164
// cannot pass for a space and a wide letter cannot take a second cell.
// `${VAR}` and `{UUID}` stay as written.
func quoteArg(a string) string {
	if a != "" && plainASCII(a) {
		return a
	}
	return strconv.QuoteToASCII(a)
}

// plainASCII reports whether s is printable ASCII with no space, quote or
// backslash.
func plainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c > '~' || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// credName matches the tail of a flag or variable that carries a credential:
// --api-key, --token, DB_PASSWORD=, --auth. Loose on purpose; masking a
// value that was not secret costs nothing.
var credName = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|pwd|credentials?|bearer|auth)$`)

// credValue is what a masked value must look like: one word, not a flag.
// A script or a sentence after --token is left alone, so masking can never
// hide a clause from the person reviewing the command.
var credValue = regexp.MustCompile(`^[^\s"'-][^\s"']*$`)

// maskCredentials replaces the value of a credential-bearing argument —
// `--api-key VALUE`, `--token=VALUE`, `API_KEY=VALUE` — with ****. A catalog
// may write a literal secret in args, and Describe's MaskAll text goes to
// stderr and --json, where it must not be readable. It is a guess (`--no-auth
// evil-pkg` masks too), which is why the consent views do not use it; the
// fingerprint is taken over the real values, so a changed secret still asks
// again.
func maskCredentials(args []string) []string {
	out := make([]string, len(args))
	flag := false // the previous argument was a credential flag awaiting its value
	for i, a := range args {
		switch {
		case flag && credValue.MatchString(a):
			out[i] = "****"
			flag = false
		case strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && credName.MatchString(a):
			out[i] = a
			flag = true
		default:
			flag = false
			out[i] = a
			if k, v, ok := strings.Cut(a, "="); ok && credName.MatchString(k) && credValue.MatchString(v) {
				out[i] = k + "=****"
			}
		}
	}
	return out
}

// Effective checks that expansion left the spec doing what Gate looked at.
// Gate reads the catalog as written, where `url: ${VAR}` is a remote server
// and needs no trust; with the variable unset the URL expands to nothing,
// and mcp.Dial would then run the entry's command — one nobody was shown.
// Such a measurement is refused, in the picker and on the command line
// alike, and the row says why.
func Effective(raw, expanded map[string]any) error {
	if !spec.ViewOf(raw).Remote() {
		return nil
	}
	v := spec.ViewOf(expanded)
	if v.Remote() {
		return nil
	}
	msg := "url expands to nothing; not run"
	if v.Command != "" {
		msg += " (" + quoteArg(v.Command) + " would run in its place)"
	}
	return errors.New(msg)
}

// Trusted reports whether the command the spec would run has been granted
// for this server in this project. A nil store trusts nothing.
func (s *Store) Trusted(root, name string, sp map[string]any) bool {
	if s == nil {
		return false
	}
	fp := Fingerprint(sp)
	if fp == "" {
		return false
	}
	for _, g := range s.Grants {
		if g.Root == root && g.Name == name && g.Spec == fp {
			return true
		}
	}
	return false
}

// Grant records consent for the command the spec runs, once; the caller
// saves. A spec that runs nothing is not recorded.
func (s *Store) Grant(root, name string, sp map[string]any) {
	if s == nil || s.Trusted(root, name, sp) {
		return
	}
	fp := Fingerprint(sp)
	if fp == "" {
		return
	}
	g := Grant{
		Root: root, Name: name, Spec: fp,
		Command: view(sp).Command,
		At:      time.Now().UTC().Format(time.RFC3339),
	}
	s.mutate(func(f *Store) { f.addGrant(g) })
}

// addGrant appends g unless an equal grant is there already.
func (s *Store) addGrant(g Grant) {
	for _, have := range s.Grants {
		if have.Root == g.Root && have.Name == g.Name && have.Spec == g.Spec {
			return
		}
	}
	s.Grants = append(s.Grants, g)
}

// Verdict says whether a measurement may run and, when not, why.
type Verdict struct {
	Run    bool
	Reason string // why not, worded for the row and the detail line
	// NeedsTrust marks a reason consent can lift: the server runs a command
	// that has not been trusted. The other reason, a remote server from the
	// repository's catalog that is not checked and would expose something
	// (Exposure), is lifted by checking it or by trusting the catalog (T).
	NeedsTrust bool
	// PublicOnly marks a run allowed only because the spec, as written,
	// exposes nothing: the connection must refuse a private address when it
	// is dialled, redirects included (mcp's option of the same name), so
	// that a public name resolving to one is not a way around Exposure.
	PublicOnly bool
	// Exposure is the condition alone (see Exposure) when a repository
	// remote is left out for it; Reason has it in a sentence. The picker
	// puts what lifts the skip first and the condition after, so a narrow
	// terminal cuts the condition rather than the hint.
	Exposure string
	// CatalogChanged says the project's catalog was trusted and a file has
	// changed since, so the rows it would have covered ask again.
	CatalogChanged bool
}

// Scope is what a measurement runs in: the trust store, the project and
// where the project's catalog trust stands (Store.Catalog over CatalogHash
// of the catalog files as they are now).
type Scope struct {
	Store   *Store
	Root    string
	Catalog CatalogStatus
}

// Gate is the one place the rules combine, so the picker and the command
// line cannot drift. A command needs trust whatever the selection says: the
// owner wants no stdio server run by a measurement until it has been seen,
// the user's own included. A remote server the repository defines is
// measured when it is checked, or when the project's catalog is trusted
// whole, or — unchecked and untrusted — when measuring it exposes nothing
// (Exposure), and then only onto a public address. Otherwise it waits,
// because measuring it would send the user's environment, expanded into URL
// and headers, to the author's host, or reach a service on the user's own
// network. Everything else is measured.
func Gate(sc Scope, s catalog.Server, selected bool) Verdict {
	if Runs(s.Spec) {
		if sc.Store.Trusted(sc.Root, s.Name, s.Spec) {
			return Verdict{Run: true}
		}
		return Verdict{Reason: "runs " + Describe(s.Spec, MaskAll), NeedsTrust: true}
	}
	if s.Origin == catalog.OriginProject && !selected && spec.ViewOf(s.Spec).Remote() && sc.Catalog != CatalogTrusted {
		why := Exposure(s.Spec)
		if why == "" {
			return Verdict{Run: true, PublicOnly: true}
		}
		return Verdict{Reason: "defined by this repository's catalog and not checked: " + why,
			Exposure: why, CatalogChanged: sc.Catalog == CatalogChanged}
	}
	return Verdict{Run: true}
}
