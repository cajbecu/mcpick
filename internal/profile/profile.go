// Package profile keeps the user's own named selections: which servers go
// together for a kind of work, valid in every project. They live in one file
// under the mcpick home, never in a repository.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/cajbecu/mcpick/internal/fsutil"
)

// Default is the built-in profile: everything available. It is never stored,
// so it cannot drift from the catalog of whichever project is open.
const Default = "default"

// Profile is one named selection. Servers keeps the order the user gave it,
// including names the current project does not have.
type Profile struct {
	Name    string   `yaml:"name"`
	Servers []string `yaml:"servers,flow"`
}

// Set is the user's profiles, in the order they are shown.
type Set struct {
	Profiles []Profile
	// Warnings are entries dropped on load: a reserved, empty or duplicate
	// name. They stay dropped once the file is next written.
	Warnings []string
	path     string
	loadErr  error // the file exists but could not be read; Save refuses
}

type file struct {
	Profiles []Profile `yaml:"profiles"`
}

const header = `# mcpick profiles: your own named selections, valid in every project.
# The order here is the order shown. Servers a project does not have are
# shown as missing there and skipped at launch.
`

// Path is the profiles file, under the mcpick home.
func Path() string { return filepath.Join(fsutil.MCPickHome(), "profiles.yaml") }

// New is an empty set that saves to path. Tests use a temporary path; a
// caller with nothing to offer passes "" and gets a set whose saves fail
// with a reason instead of a panic.
func New(path string) *Set { return &Set{path: path} }

// Load reads the profiles file. It never fails: a missing file is an empty
// set, and one that cannot be read is an empty set with Err set, so the
// picker still works and nothing is ever written over the unreadable file.
func Load() *Set { return load(Path()) }

func load(path string) *Set {
	s := New(path)
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		s.loadErr = err
		return s
	}
	// Strict: a key this version does not know (a typo such as `server:`)
	// or a second document is a load error, not something to read as empty
	// and then write back over. The user's hand edits are worth more than
	// a lenient parse. Callers name the file; the message starts with what
	// matters.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f file
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		s.loadErr = err
		return s
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		s.loadErr = errors.New("more than one YAML document; only one is expected")
		return s
	case !errors.Is(err, io.EOF):
		s.loadErr = err
		return s
	}
	seen := map[string]bool{}
	for _, p := range f.Profiles {
		p.Name = strings.TrimSpace(p.Name)
		switch {
		case p.Name == "":
			s.Warnings = append(s.Warnings, "a profile without a name was dropped")
		case p.Name == Default:
			s.Warnings = append(s.Warnings, fmt.Sprintf("profile %q is built-in; the stored one was dropped", Default))
		case seen[p.Name]:
			s.Warnings = append(s.Warnings, fmt.Sprintf("profile %q is defined twice; the second was dropped", p.Name))
		default:
			seen[p.Name] = true
			s.Profiles = append(s.Profiles, p)
		}
	}
	return s
}

// Path is where the set is saved.
func (s *Set) Path() string { return s.path }

// Err is why the file could not be read, if it could not.
func (s *Set) Err() error { return s.loadErr }

// Update applies one change to the file as it is on disk now, not to the
// copy loaded when the session began. The file is shared by every project and
// every mcpick session, so a picker left open for an hour must not save its
// stale snapshot over a profile another one added meanwhile: under a lock the
// set is reloaded, fn runs on the fresh one, and s takes it over. An error
// from fn (a name in use, a profile gone) leaves s as it was.
func (s *Set) Update(fn func(*Set) error) error {
	if s.path == "" {
		return errors.New("no profiles file")
	}
	unlock, err := fsutil.Lock(s.path, 2*time.Second, 30*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	fresh := load(s.path)
	if fresh.loadErr != nil {
		return fresh.refuse()
	}
	if err := fn(fresh); err != nil {
		return err
	}
	if err := fresh.Save(); err != nil {
		return err
	}
	*s = *fresh
	return nil
}

func (s *Set) refuse() error {
	return fmt.Errorf("profiles.yaml could not be read (%v); fix it before editing", s.loadErr)
}

// Save writes the set as it is in memory, private to the user. A file that
// failed to parse is never overwritten: the user's hand edits are worth more
// than the picker's convenience. Callers that hold a set for a while should
// go through Update instead, so another session's changes are not lost.
func (s *Set) Save() error {
	if s.loadErr != nil {
		return s.refuse()
	}
	if s.path == "" {
		return errors.New("no profiles file")
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(file{Profiles: s.Profiles}); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.path, buf.Bytes(), 0o600)
}

// Names lists the profiles in stored order.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.Profiles))
	for _, p := range s.Profiles {
		out = append(out, p.Name)
	}
	return out
}

// Find returns the profile named name and its position.
func (s *Set) Find(name string) (Profile, int, bool) {
	for i, p := range s.Profiles {
		if p.Name == name {
			return p, i, true
		}
	}
	return Profile{}, -1, false
}

// MaxNameLen is the longest a profile name may be, in characters.
const MaxNameLen = 40

// NameRule is the rule a new profile name has to meet, in the words every
// rejection uses.
const NameRule = "1-40 characters: letters, digits, '.', '_' and '-'"

// CheckName says whether name may be given to a profile: MaxNameLen
// characters at most, each a letter (any script), a digit, '.', '_' or '-'.
// Text pasted where a key was expected — spaces, slashes, colons, control
// characters — is refused whole rather than saved as a profile. Names stored
// before this rule are not checked on load: they are shown, and can be
// deleted or renamed, but a new one has to meet it.
func CheckName(name string) error {
	if name == "" {
		return errors.New("profile name cannot be empty")
	}
	if name == Default {
		return fmt.Errorf("%q is the built-in profile; pick another name", Default)
	}
	if n := len([]rune(name)); n > MaxNameLen {
		return fmt.Errorf("profile name is %d characters; the rule is %s", n, NameRule)
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("profile name %q has %q in it; the rule is %s", name, r, NameRule)
	}
	return nil
}

func (s *Set) checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := CheckName(name); err != nil {
		return "", err
	}
	return name, nil
}

// Add appends a new profile. A name already in use is an error, so a slip
// does not silently replace a profile.
func (s *Set) Add(name string, servers []string) error {
	name, err := s.checkName(name)
	if err != nil {
		return err
	}
	if _, _, ok := s.Find(name); ok {
		return fmt.Errorf("profile %q already exists", name)
	}
	s.Profiles = append(s.Profiles, Profile{Name: name, Servers: append([]string{}, servers...)})
	return nil
}

// Put creates the profile or replaces its servers, keeping its position.
func (s *Set) Put(name string, servers []string) error {
	name, err := s.checkName(name)
	if err != nil {
		return err
	}
	if _, i, ok := s.Find(name); ok {
		s.Profiles[i].Servers = append([]string{}, servers...)
		return nil
	}
	return s.Add(name, servers)
}

// Rename changes a profile's name in place.
func (s *Set) Rename(oldName, newName string) error {
	_, i, ok := s.Find(oldName)
	if !ok {
		return fmt.Errorf("no profile %q", oldName)
	}
	newName, err := s.checkName(newName)
	if err != nil {
		return err
	}
	if newName == oldName {
		return nil
	}
	if _, _, ok := s.Find(newName); ok {
		return fmt.Errorf("profile %q already exists", newName)
	}
	s.Profiles[i].Name = newName
	return nil
}

// Delete removes a profile. A missing one is an error, so a typo does not
// look like success.
func (s *Set) Delete(name string) error {
	_, i, ok := s.Find(name)
	if !ok {
		return fmt.Errorf("no profile %q", name)
	}
	s.Profiles = append(s.Profiles[:i], s.Profiles[i+1:]...)
	return nil
}

// Move shifts a profile by delta positions, stopping at the ends.
func (s *Set) Move(name string, delta int) error {
	_, i, ok := s.Find(name)
	if !ok {
		return fmt.Errorf("no profile %q", name)
	}
	j := max(0, min(len(s.Profiles)-1, i+delta))
	p := s.Profiles[i]
	s.Profiles = append(s.Profiles[:i], s.Profiles[i+1:]...)
	s.Profiles = append(s.Profiles[:j], append([]Profile{p}, s.Profiles[j:]...)...)
	return nil
}

// Toggle adds server to the profile or removes it, and says which. Other
// names, including ones this project does not have, are left as they are.
func (s *Set) Toggle(name, server string) (on bool, err error) {
	_, i, ok := s.Find(name)
	if !ok {
		return false, fmt.Errorf("no profile %q", name)
	}
	p := &s.Profiles[i]
	for k, n := range p.Servers {
		if n == server {
			p.Servers = append(p.Servers[:k], p.Servers[k+1:]...)
			return false, nil
		}
	}
	p.Servers = append(p.Servers, server)
	return true, nil
}

// Missing returns the servers has does not know, in the profile's order. The
// caller resolves the present ones in catalog order.
func Missing(servers []string, has func(string) bool) []string {
	var out []string
	for _, n := range servers {
		if !has(n) {
			out = append(out, n)
		}
	}
	return out
}
