package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readString(t *testing.T, body string) (Config, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Read(path)
}

// Most users will never create the file: no file means the defaults and no
// noise on stderr.
func TestMissingFileIsDefaults(t *testing.T) {
	cfg, warns := Read(filepath.Join(t.TempDir(), "config.yaml"))
	if cfg != Default() || len(warns) != 0 {
		t.Errorf("got %+v, %v; want the defaults and no warning", cfg, warns)
	}
	if Default().MaxRows != 10 {
		t.Errorf("default max_rows = %d, want 10", Default().MaxRows)
	}
}

func TestMaxRowsIsRead(t *testing.T) {
	cfg, warns := readString(t, "max_rows: 25\n")
	if cfg.MaxRows != 25 || len(warns) != 0 {
		t.Errorf("got %+v, %v", cfg, warns)
	}
}

// A key from a newer mcpick, or a typo, must not stop this one: unknown
// keys are ignored, and the known ones around them are still read.
func TestUnknownKeysAreIgnored(t *testing.T) {
	cfg, warns := readString(t, "colour: blue\nmax_rows: 4\nfuture_option:\n  nested: true\n")
	if cfg.MaxRows != 4 || len(warns) != 0 {
		t.Errorf("got %+v, %v", cfg, warns)
	}
}

// A value of the wrong kind keeps its default and says so, naming the file,
// the key and what it fell back to: a silent default would leave the user
// wondering why the setting has no effect, and a hard error would keep the
// agent from starting over a preference.
func TestBadValueWarnsAndKeepsDefault(t *testing.T) {
	for _, body := range []string{"max_rows: ten\n", "max_rows: 0\n", "max_rows: -3\n", "max_rows: 2.5\n", "max_rows: [1, 2]\n"} {
		cfg, warns := readString(t, body)
		if cfg.MaxRows != DefaultMaxRows {
			t.Errorf("%q: max_rows = %d, want the default", body, cfg.MaxRows)
		}
		if len(warns) != 1 || !strings.Contains(warns[0], "config.yaml") ||
			!strings.Contains(warns[0], "max_rows") || !strings.Contains(warns[0], "using 10") {
			t.Errorf("%q: warnings = %v", body, warns)
		}
	}
}

// A file that is not YAML at all is one warning and the defaults.
func TestUnparsableFileWarnsAndKeepsDefaults(t *testing.T) {
	cfg, warns := readString(t, "max_rows: [\n")
	if cfg != Default() || len(warns) != 1 || !strings.Contains(warns[0], "using defaults") {
		t.Errorf("got %+v, %v", cfg, warns)
	}
}

// The file lives in mcpick's home, so --home and MCPICK_HOME move it along
// with everything else.
func TestPathFollowsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MCPICK_HOME", home)
	if got, want := Path(), filepath.Join(home, "config.yaml"); got != want {
		t.Errorf("Path() = %s, want %s", got, want)
	}
}
