package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
)

// movePicker is a picker over real files: a project catalog with one
// server, and a ~/.claude.json (under CLAUDE_CONFIG_DIR) with one local
// and one user server, the user one carrying a literal credential.
func movePicker(t *testing.T) (*picker, string, string) {
	t.Helper()
	root := t.TempDir()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	yaml := filepath.Join(root, ".mcp.yaml")
	if err := os.WriteFile(yaml, []byte("servers:\n  ws:\n    type: http\n    url: https://x/ws\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key, _ := json.Marshal(root)
	claudePath := filepath.Join(claude, ".claude.json")
	if err := os.WriteFile(claudePath, []byte(`{
  "history": ["a < b"],
  "mcpServers": {"gl": {"type": "http", "url": "https://x/gl", "headers": {"Authorization": "Bearer live-token"}}},
  "projects": {`+string(key)+`: {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(yaml, root, root)
	if err != nil {
		t.Fatal(err)
	}
	p := newPicker(t)
	p.cat, p.root, p.cursor = cat, root, 0
	p.sel = map[string]bool{"ws": true}
	return p, yaml, claudePath
}

func rowIndex(p *picker, name string) int {
	for i, s := range p.cat.Servers {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// v then u moves the server under the cursor to the user group: the files
// change, the row appears under User with the cursor on it, and its check
// survives — a move must not quietly drop a server from the launch.
func TestMoveKeyFollowsServer(t *testing.T) {
	p, yaml, claudePath := movePicker(t)
	p.Update(keyMsg("v"))
	if p.mode != modeMoveTo {
		t.Fatalf("mode = %v, want the chooser", p.mode)
	}
	frame := plain(p.View().Content)
	if !strings.Contains(frame, `move "ws" to:`) || !strings.Contains(frame, "l local") || !strings.Contains(frame, "u user") || strings.Contains(frame, "p project") {
		t.Errorf("the chooser should offer the other writable groups only:\n%s", frame)
	}
	p.Update(keyMsg("u"))
	if p.mode != modeList {
		t.Fatalf("mode = %v after choosing", p.mode)
	}
	i := rowIndex(p, "ws")
	if i < 0 || p.cat.Servers[i].Origin != catalog.OriginUser {
		t.Fatalf("ws = %+v, want origin user", p.cat.Servers[i])
	}
	if p.cursor != i {
		t.Errorf("cursor = %d, want %d (on the moved server)", p.cursor, i)
	}
	if !p.sel["ws"] {
		t.Error("the selection was lost")
	}
	if !strings.HasPrefix(plain(p.status), "ws → user") {
		t.Errorf("status = %q; the move comes first", plain(p.status))
	}
	// User is read sorted: gl, then ws.
	if names := serverOrder(p); strings.Join(names, ",") != "pr,gl,ws" {
		t.Errorf("order = %v, want the row where a reload would put it", names)
	}
	if y, _ := os.ReadFile(yaml); strings.Contains(string(y), "ws") {
		t.Errorf("the catalog still has ws:\n%s", y)
	}
	data, _ := os.ReadFile(claudePath)
	if !strings.Contains(string(data), `"ws"`) || !strings.Contains(string(data), `"a < b"`) {
		t.Errorf("~/.claude.json = %s", data)
	}
	frame = plain(p.View().Content)
	if !strings.Contains(frame, "> [x] ws") {
		t.Errorf("the moved row should be drawn checked under the cursor:\n%s", frame)
	}
	// esc leaves the chooser without touching anything.
	p.cursor = rowIndex(p, "pr")
	before, _ := os.ReadFile(claudePath)
	p.Update(keyMsg("v"))
	p.Update(keyMsg("esc"))
	if p.mode != modeList {
		t.Error("esc should close the chooser")
	}
	if after, _ := os.ReadFile(claudePath); string(after) != string(before) {
		t.Error("esc wrote the file")
	}
}

func serverOrder(p *picker) []string {
	var out []string
	for _, s := range p.cat.Servers {
		out = append(out, s.Name)
	}
	return out
}

// A move into the project catalog of a server whose spec holds a literal
// credential asks first: that file usually sits in git. r writes ${VAR}
// references and says what to export; a typed yes writes the values.
func TestMoveIntoProjectAsksAboutSecrets(t *testing.T) {
	p, yaml, _ := movePicker(t)
	widen(p, yaml)
	p.cursor = rowIndex(p, "gl")
	p.Update(keyMsg("v"))
	p.Update(keyMsg("p"))
	if p.mode != modeMoveSecrets {
		t.Fatalf("mode = %v, want the credentials question", p.mode)
	}
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "credentials") || !strings.Contains(frame, ".mcp.yaml") || !strings.Contains(frame, "type 'yes'") {
		t.Errorf("the question should say what goes where:\n%s", frame)
	}
	if y, _ := os.ReadFile(yaml); strings.Contains(string(y), "gl") {
		t.Fatal("nothing may be written before the answer")
	}
	// esc: nothing happens.
	p.Update(keyMsg("esc"))
	if p.mode != modeList || p.status != "cancelled" {
		t.Errorf("mode = %v, status = %q after esc", p.mode, p.status)
	}
	// r: redacted.
	p.Update(keyMsg("v"))
	p.Update(keyMsg("p"))
	p.Update(keyMsg("r"))
	if p.mode != modeList {
		t.Fatalf("mode = %v after r", p.mode)
	}
	y, _ := os.ReadFile(yaml)
	if !strings.Contains(string(y), "Bearer ${GL_AUTHORIZATION:?export GL_AUTHORIZATION}") || strings.Contains(string(y), "live-token") {
		t.Errorf("the catalog should hold a reference, not the token:\n%s", y)
	}
	if !strings.Contains(p.status, "export GL_AUTHORIZATION") {
		t.Errorf("status = %q; the variable to export must be named", p.status)
	}
	if i := rowIndex(p, "gl"); p.cat.Servers[i].Origin != catalog.OriginProject || p.cursor != i {
		t.Errorf("gl = %+v, cursor = %d", p.cat.Servers[i], p.cursor)
	}
	if p.cat.Servers[rowIndex(p, "gl")].Spec["headers"].(map[string]any)["Authorization"] != "Bearer ${GL_AUTHORIZATION:?export GL_AUTHORIZATION}" {
		t.Error("the row should carry the spec as written to the file")
	}
	if names := serverOrder(p); strings.Join(names, ",") != "ws,gl,pr" {
		t.Errorf("order = %v; the project group appends", names)
	}
}

// A typed yes writes the credential as it is.
func TestMoveIntoProjectTypedYesWritesValues(t *testing.T) {
	p, yaml, _ := movePicker(t)
	p.cursor = rowIndex(p, "gl")
	p.Update(keyMsg("v"))
	p.Update(keyMsg("p"))
	for _, k := range []string{"y", "e", "s", "enter"} {
		p.Update(keyMsg(k))
	}
	if p.mode != modeList {
		t.Fatalf("mode = %v", p.mode)
	}
	if y, _ := os.ReadFile(yaml); !strings.Contains(string(y), "Bearer live-token") {
		t.Errorf("yes should write the values:\n%s", y)
	}
	// Anything else typed is not a yes.
	p, yaml, _ = movePicker(t)
	p.cursor = rowIndex(p, "gl")
	p.Update(keyMsg("v"))
	p.Update(keyMsg("p"))
	for _, k := range []string{"y", "enter"} {
		p.Update(keyMsg(k))
	}
	if y, _ := os.ReadFile(yaml); strings.Contains(string(y), "gl") {
		t.Errorf("a bare y is not a yes:\n%s", y)
	}
	if p.status != "cancelled" {
		t.Errorf("status = %q", p.status)
	}
}

// Out of the project catalog, a spec with ${VAR} placeholders moves as written and
// the status says Claude Code expands them only when the variable is set.
func TestMoveOutOfProjectNotesPlaceholders(t *testing.T) {
	p, yaml, claudePath := movePicker(t)
	if err := os.WriteFile(yaml, []byte("servers:\n  ws:\n    type: http\n    url: https://x/ws\n    headers:\n      Authorization: Bearer ${WS_TOKEN}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(yaml, p.root, p.root)
	if err != nil {
		t.Fatal(err)
	}
	p.cat, p.cursor = cat, rowIndex(p, "ws")
	p.Update(keyMsg("v"))
	p.Update(keyMsg("l"))
	if p.mode != modeList || !strings.HasPrefix(plain(p.status), "ws → local") || !strings.Contains(p.status, "expands them only") {
		t.Errorf("mode = %v, status = %q", p.mode, p.status)
	}
	if data, _ := os.ReadFile(claudePath); !strings.Contains(string(data), "${WS_TOKEN}") {
		t.Errorf("placeholder not kept:\n%s", data)
	}
}

// A plugin server's files are not mcpick's to edit: v says so and opens
// nothing.
func TestMovePluginRefused(t *testing.T) {
	p, _, _ := movePicker(t)
	p.cat.Servers = append(p.cat.Servers, catalog.Server{Name: "cf", Origin: catalog.OriginPlugin, Spec: httpSpec("https://cf")})
	p.cursor = len(p.cat.Servers) - 1
	p.Update(keyMsg("v"))
	if p.mode != modeList || !strings.Contains(p.status, "plugin") {
		t.Errorf("mode = %v, status = %q", p.mode, p.status)
	}
}

// A name already in the destination is refused from the chooser, with the
// reason on the status line, and nothing is written.
func TestMoveNameClashRefusedInPicker(t *testing.T) {
	p, yaml, _ := movePicker(t)
	if err := os.WriteFile(yaml, []byte("servers:\n  ws:\n    type: http\n    url: https://x/ws\n  pr:\n    type: http\n    url: https://x/pr-in-project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The catalog in memory still shows the project pr; the file has one.
	p.cursor = rowIndex(p, "pr")
	p.Update(keyMsg("v"))
	p.Update(keyMsg("p"))
	if p.mode != modeList || !strings.Contains(p.status, "already") {
		t.Errorf("mode = %v, status = %q", p.mode, p.status)
	}
	if p.cat.Servers[rowIndex(p, "pr")].Origin != catalog.OriginLocal {
		t.Error("the row must not move when the move was refused")
	}
}

// The key line teaches v where there is room, and drops it first when
// there is not.
func TestHintMentionsMove(t *testing.T) {
	p := newPicker(t)
	p.width = 200
	if line := p.hint(); !strings.Contains(line, "v move") {
		t.Errorf("a wide terminal should show v: %s", line)
	}
	p.width = 100
	if line := p.hint(); strings.Contains(line, "v move") && !strings.Contains(line, "H hidden") {
		t.Errorf("v should be dropped before the second spellings: %s", line)
	}
}

// Out of user, the chooser says what the move costs — ~/.claude.json's
// mcpServers are read by every project — before a letter is pressed; out
// of local or project it says nothing of the kind.
func TestMoveChooserWarnsWhenLeavingUser(t *testing.T) {
	p, _, _ := movePicker(t)
	p.width = 160
	p.cursor = rowIndex(p, "gl") // user
	p.Update(keyMsg("v"))
	if frame := plain(p.View().Content); !strings.Contains(frame, "(removes it from ~/.claude.json: every project loses it)") {
		t.Errorf("the chooser should say what leaving user means:\n%s", frame)
	}
	p.Update(keyMsg("esc"))
	p.cursor = rowIndex(p, "pr") // local
	p.Update(keyMsg("v"))
	if frame := plain(p.View().Content); strings.Contains(frame, "every project loses it") {
		t.Errorf("a local server is this project's alone:\n%s", frame)
	}
}

// After a redacting move the status leads with the move and the export
// the user has to do, so a narrow terminal cuts the notes and never the
// variable's name.
func TestMoveStatusLeadsWithTheExport(t *testing.T) {
	p, _, _ := movePicker(t)
	p.cursor = rowIndex(p, "gl")
	p.Update(keyMsg("v"))
	p.Update(keyMsg("p"))
	p.Update(keyMsg("r"))
	if !strings.HasPrefix(plain(p.status), "gl → project · export GL_AUTHORIZATION before launching") {
		t.Errorf("status = %q", plain(p.status))
	}
}
