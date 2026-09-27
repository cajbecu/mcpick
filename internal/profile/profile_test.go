package profile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempSet(t *testing.T) *Set {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "profiles.yaml"))
}

// The file must live under the mcpick home — the one directory the user
// mounts, backs up or deletes — and be private, like everything else there.
func TestProfilesLiveUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MCPICK_HOME", home)
	if got := Path(); got != filepath.Join(home, "profiles.yaml") {
		t.Fatalf("Path() = %s, want it under %s", got, home)
	}
	s := Load()
	if err := s.Add("review", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
	}
}

// Order is what J/K edit; a map would lose it between runs.
func TestSaveKeepsOrder(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	s := Load()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := s.Add(n, []string{n + "-srv", "shared"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# mcpick profiles") {
		t.Errorf("the file should start with the explanatory header:\n%s", data)
	}
	if !strings.Contains(string(data), "servers: [zeta-srv, shared]") {
		t.Errorf("servers should be written on one line, as the catalog does:\n%s", data)
	}
	if got := strings.Join(Load().Names(), ","); got != "zeta,alpha,mid" {
		t.Errorf("order after reload = %s", got)
	}
}

// A first run has no file; that is not an error, and the first save makes it.
func TestMissingFileIsEmpty(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	s := Load()
	if s.Err() != nil || len(s.Profiles) != 0 {
		t.Fatalf("missing file: err = %v, profiles = %v", s.Err(), s.Profiles)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path()); err != nil {
		t.Errorf("Save should have created the file: %v", err)
	}
}

// A hand-edited file with a typo must not be replaced by an empty one the
// next time a key is pressed in the picker.
func TestCorruptFileIsNeverOverwritten(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	garbage := []byte("profiles: [\n")
	if err := os.WriteFile(Path(), garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	s := Load()
	if s.Err() == nil {
		t.Fatal("a file that does not parse must set Err")
	}
	if err := s.Add("x", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("Save on a corrupt file must refuse and say why, got %v", err)
	}
	data, _ := os.ReadFile(Path())
	if string(data) != string(garbage) {
		t.Error("the file was changed")
	}
}

// "default" is built-in; a stored one would shadow it with stale contents.
// Duplicates and blanks would make the screen ambiguous.
func TestReservedAndDuplicateNames(t *testing.T) {
	s := tempSet(t)
	if err := s.Add(Default, nil); err == nil {
		t.Error("Add(default) must fail")
	}
	if err := s.Add("", nil); err == nil {
		t.Error("Add(\"\") must fail")
	}
	if err := s.Add("a", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("a", nil); err == nil {
		t.Error("Add twice must fail")
	}
	if err := s.Add("b", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("b", "a"); err == nil {
		t.Error("Rename onto an existing name must fail")
	}
	if err := s.Rename("b", Default); err == nil {
		t.Error("Rename to default must fail")
	}
	if err := s.Put(Default, nil); err == nil {
		t.Error("Put(default) must fail")
	}

	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(Path(), []byte("profiles:\n  - {name: default, servers: [x]}\n  - {name: ok, servers: []}\n  - {name: ok, servers: [y]}\n  - {name: '', servers: []}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := Load()
	if got := strings.Join(loaded.Names(), ","); got != "ok" {
		t.Errorf("names = %s, want only ok", got)
	}
	if len(loaded.Warnings) != 3 {
		t.Errorf("warnings = %v, want one per dropped entry", loaded.Warnings)
	}
}

// Moving past an end must neither fail nor wrap: J on the last row is a no-op.
func TestMoveClamps(t *testing.T) {
	s := tempSet(t)
	for _, n := range []string{"a", "b", "c"} {
		_ = s.Add(n, nil)
	}
	if err := s.Move("a", -1); err != nil {
		t.Fatal(err)
	}
	if err := s.Move("c", 5); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Names(), ""); got != "abc" {
		t.Errorf("order = %s after moves at the ends", got)
	}
	_ = s.Move("a", 1)
	if got := strings.Join(s.Names(), ""); got != "bac" {
		t.Errorf("order = %s after moving a down", got)
	}
	_ = s.Move("c", -2)
	if got := strings.Join(s.Names(), ""); got != "cba" {
		t.Errorf("order = %s after moving c to the top", got)
	}
	if err := s.Move("nope", 1); err == nil {
		t.Error("moving a missing profile must be an error")
	}
}

// A profile is global: toggling a server in project A must not drop a server
// that only exists in project B.
func TestToggleKeepsForeignServers(t *testing.T) {
	s := tempSet(t)
	_ = s.Add("review", []string{"github", "only-elsewhere"})
	on, err := s.Toggle("review", "github")
	if err != nil || on {
		t.Fatalf("toggle off: on = %v, err = %v", on, err)
	}
	on, err = s.Toggle("review", "sentry")
	if err != nil || !on {
		t.Fatalf("toggle on: on = %v, err = %v", on, err)
	}
	p, _, _ := s.Find("review")
	if got := strings.Join(p.Servers, ","); got != "only-elsewhere,sentry" {
		t.Errorf("servers = %s", got)
	}
	if _, err := s.Toggle("nope", "x"); err == nil {
		t.Error("toggling in a missing profile must be an error")
	}
}

// The right pane's "Missing here" group and the launch warning both depend
// on this split; the profile's own order keeps the list stable.
func TestMissingSplit(t *testing.T) {
	here := map[string]bool{"a": true, "c": true}
	got := Missing([]string{"z", "a", "y", "c"}, func(n string) bool { return here[n] })
	if strings.Join(got, ",") != "z,y" {
		t.Errorf("missing = %v", got)
	}
	if Missing([]string{"a"}, func(string) bool { return true }) != nil {
		t.Error("nothing missing should be nil")
	}
}

// Delete must say so when nothing was there, and leave the rest in order.
func TestDeleteAndRename(t *testing.T) {
	s := tempSet(t)
	for _, n := range []string{"a", "b", "c"} {
		_ = s.Add(n, []string{n})
	}
	if err := s.Delete("b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("b"); err == nil {
		t.Error("deleting a missing profile must be an error")
	}
	if err := s.Rename("c", "cc"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("zz", "y"); err == nil {
		t.Error("renaming a missing profile must be an error")
	}
	if got := strings.Join(s.Names(), ","); got != "a,cc" {
		t.Errorf("names = %s", got)
	}
	if err := s.Put("a", []string{"x", "y"}); err != nil {
		t.Fatal(err)
	}
	if p, i, _ := s.Find("a"); i != 0 || strings.Join(p.Servers, ",") != "x,y" {
		t.Errorf("Put should replace in place: i = %d, servers = %v", i, p.Servers)
	}
}

// A set without a path (a caller that passed nothing) must refuse to save
// with a reason, not panic or write somewhere surprising.
func TestNoPathRefusesSave(t *testing.T) {
	s := New("")
	_ = s.Add("x", nil)
	if err := s.Save(); err == nil {
		t.Error("Save with no path must fail")
	}
}

// Two pickers open at once each hold their own copy of the file. If the
// second one saved its copy, the profile the first one added meanwhile would
// vanish although both reported success; Update reloads under the lock first.
func TestTwoEditorsKeepEachOthersChanges(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	a, b := Load(), Load()
	if err := a.Update(func(s *Set) error { return s.Add("browser", []string{"playwright"}) }); err != nil {
		t.Fatal(err)
	}
	if err := b.Update(func(s *Set) error { return s.Add("review", []string{"github"}) }); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(Load().Names(), ","); got != "browser,review" {
		t.Errorf("on disk = %s, want both profiles", got)
	}
	if got := strings.Join(b.Names(), ","); got != "browser,review" {
		t.Errorf("b should have taken over the merged set, has %s", got)
	}
	// A change to a profile the other editor deleted is refused, and the
	// refusing set is left as it was rather than half-updated.
	if err := a.Update(func(s *Set) error { return s.Delete("review") }); err != nil {
		t.Fatal(err)
	}
	err := b.Update(func(s *Set) error { _, err := s.Toggle("review", "x"); return err })
	if err == nil || !strings.Contains(err.Error(), `no profile "review"`) {
		t.Fatalf("toggling a profile deleted elsewhere: %v", err)
	}
	if _, err := os.Stat(Path() + ".mcpick-lock"); err == nil {
		t.Error("the lock was left behind")
	}
}

// Update on a corrupt file must refuse like Save does, and on a set without
// a path say so instead of locking "".
func TestUpdateRefusesCorruptAndPathless(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(Path(), []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Load()
	err := s.Update(func(s *Set) error { return s.Add("x", nil) })
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("Update on a corrupt file: %v", err)
	}
	if err := New("").Update(func(*Set) error { return nil }); err == nil {
		t.Error("Update without a path must fail")
	}
}

// A hand-edited file with a mistyped key (`server:`) or a stray second
// document must not read as "no profiles" and be replaced on the next save:
// it is a load error, refused like a parse error, and left as it is.
func TestUnknownKeysAndExtraDocumentsAreLoadErrors(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	for name, text := range map[string]string{
		"typo key":      "profiles:\n  - name: review\n    server: [a, b]\n",
		"unknown top":   "profiles: []\nversion: 2\n",
		"two documents": "profiles:\n  - {name: review, servers: [a]}\n---\nprofiles: []\n",
	} {
		if err := os.WriteFile(Path(), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		s := Load()
		if s.Err() == nil {
			t.Errorf("%s: Err = nil, want a load error; profiles = %v", name, s.Profiles)
			continue
		}
		if err := s.Save(); err == nil || !strings.Contains(err.Error(), "could not be read") {
			t.Errorf("%s: Save = %v, want refused", name, err)
		}
		data, _ := os.ReadFile(Path())
		if string(data) != text {
			t.Errorf("%s: the file was changed", name)
		}
	}
	// A well-formed file, and an empty one, still load.
	if err := os.WriteFile(Path(), []byte("profiles:\n  - {name: review, servers: [a]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := Load(); s.Err() != nil || strings.Join(s.Names(), ",") != "review" {
		t.Errorf("well-formed: err = %v, names = %v", s.Err(), s.Names())
	}
	if err := os.WriteFile(Path(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := Load(); s.Err() != nil || len(s.Profiles) != 0 {
		t.Errorf("empty file: err = %v, profiles = %v", s.Err(), s.Profiles)
	}
}

// A profile name is a word: text pasted where a key was expected — a
// sentence, a URL, a control character — is refused with the rule, in every
// script; a name stored before the rule still loads, and can be deleted or
// renamed, but is not written under again.
func TestNameRule(t *testing.T) {
	for _, ok := range []string{"review", "dev-2", "ci_2026", "v1.2", "Überprüfung", "评审", strings.Repeat("a", MaxNameLen)} {
		if err := CheckName(ok); err != nil {
			t.Errorf("CheckName(%q) = %v, want ok", ok, err)
		}
	}
	for _, bad := range []string{"a b", "x/y", "a:b", "tab\there", "esc\x1b[8m", "q?", strings.Repeat("a", MaxNameLen+1),
		"tigate the failing checkout test in https://example.com/issues/4321 (\"Payments: 9 orders\""} {
		err := CheckName(bad)
		if err == nil {
			t.Errorf("CheckName(%q) accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), NameRule) {
			t.Errorf("CheckName(%q) = %v; the message should state the rule", bad, err)
		}
	}
	s := tempSet(t)
	if err := s.Add("a b", nil); err == nil {
		t.Error("Add must apply the rule")
	}
	if err := s.Put("x/y", nil); err == nil {
		t.Error("Put must apply the rule")
	}
	if err := s.Add("ok", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("ok", "not ok"); err == nil || s.Profiles[0].Name != "ok" {
		t.Errorf("Rename must apply the rule: %v, %q", err, s.Profiles[0].Name)
	}

	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(Path(), []byte("profiles:\n  - {name: 'old name', servers: [x]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := Load()
	if got := strings.Join(legacy.Names(), ","); got != "old name" || len(legacy.Warnings) != 0 {
		t.Fatalf("names = %s, warnings = %v; a legacy name loads as it is", got, legacy.Warnings)
	}
	if err := legacy.Put("old name", []string{"y"}); err == nil {
		t.Error("Put under a legacy name must be refused, not rewritten")
	}
	if err := legacy.Rename("old name", "new"); err != nil || legacy.Profiles[0].Name != "new" {
		t.Errorf("Rename off a legacy name: %v, %q", err, legacy.Profiles[0].Name)
	}
	if err := legacy.Delete("new"); err != nil {
		t.Error(err)
	}
}
