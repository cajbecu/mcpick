package state

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MCPICK_HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return home
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The selection lives in mcpick's home, not in the user's repository.
func TestSelectionLivesUnderHome(t *testing.T) {
	home := testHome(t)
	root := filepath.Join(t.TempDir(), "myproject")
	if err := Save(root, "box-1", State{Selected: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	path := Path(root, "box-1")
	if rel, err := filepath.Rel(filepath.Join(home, "selections"), path); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("selection at %s, want it under %s/selections", path, home)
	}
	// Recognisable by a person browsing the directory.
	if !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "myproject-") {
		t.Errorf("workspace directory = %s, want it to start with the workspace name", filepath.Dir(path))
	}
	if _, err := os.Stat(filepath.Join(root, ".tmp")); !os.IsNotExist(err) {
		t.Error("nothing may be written into the workspace")
	}
	got, err := Load(root, "box-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Selected) != 1 || got.Selected[0] != "a" || got.Workspace != root {
		t.Errorf("state = %+v", got)
	}
}

// Two checkouts with the same name must not share a selection.
func TestSameNameDifferentWorkspaces(t *testing.T) {
	testHome(t)
	a := filepath.Join(t.TempDir(), "app")
	b := filepath.Join(t.TempDir(), "app")
	if Dir(a) == Dir(b) {
		t.Fatalf("both workspaces map to %s", Dir(a))
	}
}

func TestStateIsPerWorkspaceAndPerUID(t *testing.T) {
	testHome(t)
	a, b := t.TempDir(), t.TempDir()
	for _, tc := range []struct{ root, uid, sel string }{{a, "one", "x"}, {a, "two", "y"}, {b, "one", "z"}} {
		if err := Save(tc.root, tc.uid, State{Selected: []string{tc.sel}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ root, uid, want string }{{a, "one", "x"}, {a, "two", "y"}, {b, "one", "z"}} {
		got, err := Load(tc.root, tc.uid)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Selected) != 1 || got.Selected[0] != tc.want {
			t.Errorf("state(%s,%s) = %v, want %s", tc.root, tc.uid, got.Selected, tc.want)
		}
	}
}

// Upgrading must not lose a selection saved by an earlier version; the next
// save moves it to the new place and leaves the old file alone.
func TestLegacySelectionsAreRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(root string) string
	}{
		{"0.1.0 .tmp", func(root string) string { return filepath.Join(root, ".tmp", "mcpick-box.json") }},
		{"prototype .scratchpad", func(root string) string { return filepath.Join(root, ".scratchpad", "mcpick", "box.json") }},
		{"xdg state", func(root string) string { return legacyPaths(root, "box")[2] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testHome(t)
			root := t.TempDir()
			legacy := tc.path(root)
			write(t, legacy, `{"selected":["old"]}`)

			got, err := Load(root, "box")
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Selected) != 1 || got.Selected[0] != "old" {
				t.Fatalf("legacy selection ignored: %+v", got)
			}
			if err := Save(root, "box", State{Selected: []string{"new"}}); err != nil {
				t.Fatal(err)
			}
			got, _ = Load(root, "box")
			if got.Selected[0] != "new" {
				t.Errorf("state = %+v, want the new location to win", got)
			}
			if body, _ := os.ReadFile(legacy); !strings.Contains(string(body), "old") {
				t.Error("the legacy file must never be written")
			}
		})
	}
}

func TestHostileUIDStaysInside(t *testing.T) {
	testHome(t)
	root := t.TempDir()
	got := Path(root, "../../etc/passwd")
	if rel, err := filepath.Rel(Dir(root), got); err != nil || strings.Contains(rel, "..") || strings.ContainsRune(rel, filepath.Separator) {
		t.Errorf("a hostile uid escaped the selections directory: %s", got)
	}
}

func TestStateFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	testHome(t)
	root := t.TempDir()
	if err := Save(root, "box", State{Selected: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path(root, "box"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestCorruptSelectionIsEmpty(t *testing.T) {
	testHome(t)
	root := t.TempDir()
	write(t, Path(root, "box"), "{not json")
	got, err := Load(root, "box")
	if err != nil || len(got.Selected) != 0 {
		t.Errorf("got %+v, %v; a corrupt selection should read as empty, not fail the launch", got, err)
	}
}
