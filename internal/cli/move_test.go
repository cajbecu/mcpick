package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moveWorkspace is a workspace on disk with one server in the catalog and,
// under CLAUDE_CONFIG_DIR, a ~/.claude.json with a local server and two
// user ones, one of them carrying a literal credential. It returns a
// runner for the command line.
func moveWorkspace(t *testing.T) (root, claudePath string, run func(args ...string) (out, errw string, err error)) {
	t.Helper()
	root = t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte("servers:\n  a:\n    type: http\n    url: https://x/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key, _ := json.Marshal(root)
	claudePath = filepath.Join(claude, ".claude.json")
	if err := os.WriteFile(claudePath, []byte(`{
  "history": ["a < b"],
  "mcpServers": {
    "gl": {"type": "http", "url": "https://x/gl"},
    "secret": {"type": "http", "url": "https://x/s", "headers": {"Authorization": "Bearer live-token"}}
  },
  "projects": {`+string(key)+`: {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run = func(args ...string) (string, string, error) {
		t.Helper()
		var out, errw strings.Builder
		a := &app{out: &out, errw: &errw, version: "test"}
		err := a.run(args)
		return out.String(), errw.String(), err
	}
	return root, claudePath, run
}

// `mcpick move` moves a server between the groups by the picker's rules,
// and says what it did.
func TestMoveCommand(t *testing.T) {
	root, claudePath, run := moveWorkspace(t)
	out, _, err := run("move", "a", "user")
	if err != nil || !strings.Contains(out, "moved a from project") || !strings.Contains(out, "to user") {
		t.Fatalf("move a user: %v, %q", err, out)
	}
	if y, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml")); strings.Contains(string(y), "https://x/a") {
		t.Errorf("a is still in the catalog:\n%s", y)
	}
	data, _ := os.ReadFile(claudePath)
	if !strings.Contains(string(data), `"https://x/a"`) || !strings.Contains(string(data), `"a < b"`) {
		t.Errorf("~/.claude.json = %s", data)
	}
	if out, _, _ := run("list"); !strings.Contains(out, "a") || strings.Contains(out, "project") {
		t.Errorf("list after the move = %q", out)
	}
	if _, _, err := run("move", "a", "user"); err == nil || !strings.Contains(err.Error(), "already in user") {
		t.Errorf("moving to its own group: %v", err)
	}
	if _, _, err := run("move", "pr", "user"); err != nil {
		t.Errorf("local -> user: %v", err)
	}
	if _, _, err := run("move", "gl", "local"); err != nil {
		t.Errorf("user -> local: %v", err)
	}
	if out, _, _ := run("list", "--json"); !strings.Contains(out, `"origin": "local"`) {
		t.Errorf("list --json after the moves = %q", out)
	}
	if _, _, err := run("move", "gl"); err == nil {
		t.Error("move without a group must be refused")
	}
	if _, _, err := run("move", "gl", "plugin"); err == nil {
		t.Error("move to the plugin group must be refused")
	}
	if _, _, err := run("move", "ghost", "user"); err == nil {
		t.Error("an unknown server must be refused")
	}
}

// A credential written in the spec must not reach the project catalog —
// a file that usually sits in git — on a bare command: without a terminal
// the move is refused unless --yes or --redact says what to do with it.
func TestMoveCommandSecretsNeedYesOrRedact(t *testing.T) {
	root, claudePath, run := moveWorkspace(t)
	yaml := filepath.Join(root, ".mcp.yaml")
	before, _ := os.ReadFile(yaml)
	_, _, err := run("move", "secret", "project")
	if err == nil || !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "--redact") {
		t.Fatalf("err = %v; want a refusal naming both flags", err)
	}
	if after, _ := os.ReadFile(yaml); string(after) != string(before) {
		t.Fatal("a refused move wrote the catalog")
	}
	if data, _ := os.ReadFile(claudePath); !strings.Contains(string(data), "live-token") {
		t.Fatal("a refused move touched ~/.claude.json")
	}

	out, errw, err := run("move", "secret", "project", "--redact")
	if err != nil {
		t.Fatal(err)
	}
	y, _ := os.ReadFile(yaml)
	// The reference fails a launch loudly when the variable is unset,
	// naming it, instead of rendering "Bearer ".
	if !strings.Contains(string(y), "Bearer ${SECRET_AUTHORIZATION:?export SECRET_AUTHORIZATION}") || strings.Contains(string(y), "live-token") {
		t.Errorf("--redact should write a reference:\n%s", y)
	}
	if !strings.Contains(out, "export SECRET_AUTHORIZATION=") {
		t.Errorf("stdout = %q; the variable to export must be named", out)
	}
	if !strings.Contains(errw, "backup") {
		t.Errorf("stderr = %q; where the value still is must be said", errw)
	}
	if data, _ := os.ReadFile(claudePath); strings.Contains(string(data), `"secret"`) {
		t.Error("the source entry should be gone")
	}

	// --yes writes the value as it is. A plain server needs neither flag.
	root, _, run = moveWorkspace(t)
	if _, _, err := run("--yes", "move", "secret", "project"); err != nil {
		t.Fatal(err)
	}
	if y, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml")); !strings.Contains(string(y), "Bearer live-token") {
		t.Errorf("--yes should write the value:\n%s", y)
	}
	if _, _, err := run("move", "gl", "project"); err != nil {
		t.Errorf("a server without credentials needs no flag: %v", err)
	}
}

// Out of the project catalog, ${VAR} placeholders move as written and the command
// says Claude Code expands them only when the variable is set.
func TestMoveCommandNotesPlaceholders(t *testing.T) {
	root, claudePath, run := moveWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte("servers:\n  a:\n    type: http\n    url: https://x/a\n    headers:\n      Authorization: Bearer ${A_TOKEN}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A_TOKEN", "expanded-if-wrong")
	_, errw, err := run("move", "a", "local")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw, "expands them only") {
		t.Errorf("stderr = %q", errw)
	}
	if data, _ := os.ReadFile(claudePath); !strings.Contains(string(data), "${A_TOKEN}") || strings.Contains(string(data), "expanded-if-wrong") {
		t.Errorf("placeholder not kept:\n%s", data)
	}
}

func TestParseArgsYesIsNotLast(t *testing.T) {
	opt, cmd, rest, err := parseArgs([]string{"move", "a", "project", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "move" || strings.Join(rest, " ") != "a project" {
		t.Errorf("cmd = %q, rest = %v", cmd, rest)
	}
	if !opt.yes || opt.last {
		t.Errorf("--yes should set yes and nothing else: %+v", opt)
	}
}
