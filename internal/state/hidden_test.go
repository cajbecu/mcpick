package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A server hidden in one checkout must stay hidden the next time the picker
// opens there, and only there: the set is keyed like the selection, per
// workspace, and lives under mcpick's state directory.
func TestHiddenIsPerWorkspaceUnderHome(t *testing.T) {
	home := testHome(t)
	a := filepath.Join(t.TempDir(), "app")
	b := filepath.Join(t.TempDir(), "app")
	if err := SaveHidden(a, map[string]bool{"noisy": true, "gone": false}); err != nil {
		t.Fatal(err)
	}
	path := HiddenPath(a)
	if rel, err := filepath.Rel(filepath.Join(home, "state", "hidden"), path); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("hidden set at %s, want it under %s/state/hidden", path, home)
	}
	if filepath.Base(path) != filepath.Base(Dir(a))+".json" {
		t.Errorf("hidden set is %s; it should carry the workspace's name and hash like %s", path, Dir(a))
	}
	got, err := LoadHidden(a)
	if err != nil {
		t.Fatal(err)
	}
	if !got["noisy"] || got["gone"] || len(got) != 1 {
		t.Errorf("hidden = %v, want noisy only (a false entry is not hidden)", got)
	}
	other, err := LoadHidden(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("a checkout with the same name inherited the hidden set: %v", other)
	}
}

// Nothing hidden yet, or a file that cannot be read, must not stop the
// picker from opening: the worst case is that every server is in view.
func TestHiddenMissingOrCorruptIsEmpty(t *testing.T) {
	testHome(t)
	root := t.TempDir()
	got, err := LoadHidden(root)
	if err != nil || len(got) != 0 {
		t.Errorf("no file: got %v, %v; want empty", got, err)
	}
	write(t, HiddenPath(root), "{not json")
	got, err = LoadHidden(root)
	if err != nil || len(got) != 0 {
		t.Errorf("corrupt file: got %v, %v; want empty", got, err)
	}
}

// The hidden set must not live in the flat <uid>.json namespace: a session
// may be called anything, including "hidden", and a file system that folds
// case would let "HIDDEN" reach the same file. No uid, however spelled, may
// share a path with the hidden set, and a selection saved under such a uid
// before this version is still read.
func TestNoUIDSharesAPathWithTheHiddenSet(t *testing.T) {
	testHome(t)
	root := t.TempDir()
	hidden := strings.ToLower(HiddenPath(root))
	for _, uid := range []string{"hidden", "hidden-uid", "HIDDEN", "Hidden.json"} {
		p := Path(root, uid)
		if strings.ToLower(p) == hidden || filepath.Dir(p) == filepath.Dir(HiddenPath(root)) {
			t.Errorf("uid %q maps to %s, beside or onto the hidden set %s", uid, p, HiddenPath(root))
		}
	}
	if Path(root, "hidden") == Path(root, "hidden-uid") {
		t.Error("two different uids share a selection file")
	}
	write(t, filepath.Join(Dir(root), "hidden.json"), `{"selected":["a"]}`) // saved by 0.1.x as --uid hidden
	if err := SaveHidden(root, map[string]bool{"b": true}); err != nil {
		t.Fatal(err)
	}
	st, _ := Load(root, "hidden")
	set, _ := LoadHidden(root)
	if len(st.Selected) != 1 || st.Selected[0] != "a" || !set["b"] || set["a"] {
		t.Errorf("selection = %v, hidden = %v; one read or overwrote the other", st.Selected, set)
	}
}

// Two pickers open on the same project each hold a copy of the set from
// when they started. Each h changes one server; saving a whole stale copy
// would drop what the other picker hid, and --all would load it again.
func TestSetHiddenKeepsChangesMadeByAnotherPicker(t *testing.T) {
	testHome(t)
	root := t.TempDir()
	first, err := SetHidden(root, "github", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SetHidden(root, "local-tool", true) // the other picker never saw github go
	if err != nil {
		t.Fatal(err)
	}
	if !first["github"] || first["local-tool"] {
		t.Errorf("first = %v, want github only", first)
	}
	if !second["github"] || !second["local-tool"] {
		t.Errorf("second = %v, want both: the returned set is what is on disk", second)
	}
	got, _ := LoadHidden(root)
	if !got["github"] || !got["local-tool"] {
		t.Errorf("on disk = %v, want both", got)
	}
	got, err = SetHidden(root, "github", false)
	if err != nil || got["github"] || !got["local-tool"] {
		t.Errorf("after unhiding github: %v, %v", got, err)
	}
	if _, err := os.Stat(HiddenPath(root) + ".mcpick-lock"); !os.IsNotExist(err) {
		t.Error("the lock was left behind")
	}
}
