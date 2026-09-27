package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// ~/.claude.json is mostly somebody else's state: conversation history full
// of < and &, onboarding flags, project records. Adding one server must leave
// every other byte and the key order as they were, and leave a backup, like
// deleting does.
func TestAddToClaudeJSONKeepsBytesOrderAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	write(t, path, `{
  "numStartups": 42,
  "history": ["a < b && c > d"],
  "mcpServers": {
    "zeta": {"url": "https://x/z?a=1&b=2"}
  },
  "projects": {"/w": {"history": ["p"]}},
  "theme": "dark"
}`)
	if err := AddToClaudeJSON(path, "/w", "alpha", OriginUser, map[string]any{"type": "http", "url": "https://x/a?c=3&d=<4>"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	body := string(data)
	for _, want := range []string{`"a < b && c > d"`, `"https://x/z?a=1&b=2"`, `"https://x/a?c=3&d=<4>"`, `"numStartups": 42`} {
		if !strings.Contains(body, want) {
			t.Errorf("lost or re-encoded %s:\n%s", want, body)
		}
	}
	order, err := spec.TopLevelOrder(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "numStartups,history,mcpServers,projects,theme" {
		t.Errorf("top-level order = %v", order)
	}
	var top struct {
		McpServers json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if sorder, _ := spec.TopLevelOrder(top.McpServers); strings.Join(sorder, ",") != "zeta,alpha" {
		t.Errorf("the new server should follow the existing ones, got %v", sorder)
	}
	backups, _ := filepath.Glob(path + ".mcpick-bak-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one", backups)
	}
	if b, _ := os.ReadFile(backups[0]); !strings.Contains(string(b), `"zeta"`) || strings.Contains(string(b), `"alpha"`) {
		t.Error("the backup should be the file as it was before the add")
	}
	if _, err := os.Stat(path + ".mcpick-lock"); err == nil {
		t.Error("the lock was not released")
	}
}

// Claude Code keys project servers under the path it was launched from; a
// project it has never seen has no entry, and the move must create one under
// the key the catalog computed rather than fail or write at the top level.
func TestAddToClaudeJSONCreatesProjectEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{"projects":{"/other":{"mcpServers":{"o":{"url":"https://o"}}}}}`)
	if err := AddToClaudeJSON(path, "/w", "a", OriginLocal, map[string]any{"url": "https://a"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var top struct {
		McpServers map[string]any `json:"mcpServers"`
		Projects   map[string]struct {
			McpServers map[string]any `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top.Projects["/w"].McpServers["a"]; !ok {
		t.Errorf("a is not under projects[/w]:\n%s", data)
	}
	if _, ok := top.Projects["/other"].McpServers["o"]; !ok {
		t.Error("another project's servers were lost")
	}
	if len(top.McpServers) != 0 {
		t.Error("a local server must not land in the user map")
	}

	// And into a file that does not exist yet: a fresh machine.
	fresh := filepath.Join(t.TempDir(), ".claude.json")
	if err := AddToClaudeJSON(fresh, "/w", "a", OriginUser, map[string]any{"url": "https://a"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(fresh); !strings.Contains(string(data), `"https://a"`) {
		t.Errorf("fresh file = %s", data)
	}
}

// A server of that name already in the destination is somebody's live
// config; adding over it would replace it without a word.
func TestAddToClaudeJSONRefusesExistingName(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{"mcpServers":{"a":{"url":"https://old"}}}`)
	err := AddToClaudeJSON(path, "/w", "a", OriginUser, map[string]any{"url": "https://new"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "https://old") {
		t.Error("the existing server was replaced")
	}
}

// Two mcpicks, or mcpick and a Claude session, must not write ~/.claude.json
// at once: the add takes the same lock the delete does and gives up, rather
// than write over a half-finished edit.
func TestAddToClaudeJSONHonoursLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{}`)
	unlock, err := fsutil.Lock(path, time.Second, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	old := claudeLockWait
	claudeLockWait = 50 * time.Millisecond
	defer func() { claudeLockWait = old }()
	err = AddToClaudeJSON(path, "/w", "a", OriginUser, map[string]any{"url": "https://a"})
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v, want the lock reported", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "{}" {
		t.Error("the file was written under someone else's lock")
	}
}

// moveFixture is a workspace with one server in each writable group.
func moveFixture(t *testing.T) (root string, claudePath string) {
	t.Helper()
	root = t.TempDir()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	write(t, filepath.Join(root, ".mcp.yaml"), "servers:\n  ws:\n    type: http\n    url: https://x/ws\n")
	claudePath = filepath.Join(claude, ".claude.json")
	write(t, claudePath, `{
  "numStartups": 1,
  "mcpServers": {"gl": {"type": "http", "url": "https://x/gl"}},
  "projects": {`+jsonStr(root)+`: {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}, "history": ["a < b"]}}
}`)
	return root, claudePath
}

func load(t *testing.T, root string) *Catalog {
	t.Helper()
	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// A move in every direction ends with the server in the destination, gone
// from the source, with the same spec, and nothing else changed.
func TestMoveEveryDirection(t *testing.T) {
	byOrigin := map[string]string{OriginProject: "ws", OriginLocal: "pr", OriginUser: "gl"}
	for _, from := range Writable {
		for _, to := range Writable {
			if from == to {
				continue
			}
			t.Run(from+"->"+to, func(t *testing.T) {
				root, claudePath := moveFixture(t)
				cat := load(t, root)
				name := byOrigin[from]
				before, _ := cat.Find(name)
				m, err := cat.Move(name, to, before.Spec)
				if err != nil {
					t.Fatal(err)
				}
				if m.From != from || m.To != to {
					t.Errorf("moved = %+v", m)
				}
				after := load(t, root)
				s, ok := after.Find(name)
				if !ok || s.Origin != to {
					t.Fatalf("%s = %+v, %v; want origin %s", name, s, ok, to)
				}
				if s.Endpoint() != before.Endpoint() {
					t.Errorf("spec changed: %s -> %s", before.Endpoint(), s.Endpoint())
				}
				if len(after.Servers) != 3 {
					t.Errorf("servers = %v, want the same three", serverNames(after))
				}
				if len(after.Warnings) != 0 {
					t.Errorf("warnings = %v; the server must be in one place only", after.Warnings)
				}
				data, _ := os.ReadFile(claudePath)
				if !strings.Contains(string(data), `"a < b"`) || !strings.Contains(string(data), `"numStartups": 1`) {
					t.Errorf("unrelated state of ~/.claude.json was lost or re-encoded:\n%s", data)
				}
			})
		}
	}
}

// A name already in the destination is somebody's live server; the move is
// refused before anything is written, and both files stay as they were.
func TestMoveRefusesNameClash(t *testing.T) {
	root, claudePath := moveFixture(t)
	// Shadowed duplicates: the catalog shows the first definition, and the
	// destination holds the one it hides.
	write(t, claudePath, `{"mcpServers":{"ws":{"url":"https://shadowed"},"pr":{"url":"https://shadowed"}},"projects":{`+jsonStr(root)+`:{"mcpServers":{"pr":{"url":"https://x/pr"}}}}}`)
	cat := load(t, root)
	before, _ := os.ReadFile(claudePath)
	yamlBefore, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml"))

	s, _ := cat.Find("ws")
	if _, err := cat.Move("ws", OriginUser, s.Spec); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("project -> user over a user ws: err = %v", err)
	}
	p, _ := cat.Find("pr")
	if _, err := cat.Move("pr", OriginUser, p.Spec); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("local -> user over a user pr: err = %v", err)
	}
	if after, _ := os.ReadFile(claudePath); string(after) != string(before) {
		t.Error("a refused move wrote ~/.claude.json")
	}
	if after, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml")); string(after) != string(yamlBefore) {
		t.Error("a refused move wrote the catalog")
	}
	// And the other way: the project catalog already has the name.
	write(t, claudePath, `{"mcpServers":{"ws":{"url":"https://x/gl"}}}`)
	cat = load(t, root)
	if _, err := cat.Move("ws", OriginProject, map[string]any{"url": "https://x/gl"}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("into a project catalog that has the name: err = %v", err)
	}
}

// The add comes first, so a delete that fails leaves the server in two
// files; the error has to say so and name both, or the user would launch
// with a duplicate and never know which one wins.
func TestMoveReportsBothFilesWhenDeleteFails(t *testing.T) {
	root, claudePath := moveFixture(t)
	cat := load(t, root)
	// The source file is locked by someone else: the add (into
	// ~/.claude.json, another lock) goes through, the delete cannot.
	yaml := filepath.Join(root, ".mcp.yaml")
	unlock, err := fsutil.Lock(yaml, time.Second, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	old := claudeLockWait
	claudeLockWait = 50 * time.Millisecond
	defer func() { claudeLockWait = old }()
	s, _ := cat.Find("ws")
	m, err := cat.Move("ws", OriginUser, s.Spec)
	var split *SplitError
	if !errors.As(err, &split) {
		t.Fatalf("err = %v, want a SplitError", err)
	}
	msg := err.Error()
	for _, want := range []string{"ws", fsutil.ShortenHome(claudePath), fsutil.ShortenHome(yaml), "both"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should mention %q", msg, want)
		}
	}
	if m.ToFile != claudePath {
		t.Errorf("moved = %+v; the add did happen", m)
	}
	if data, _ := os.ReadFile(claudePath); !strings.Contains(string(data), `"ws"`) {
		t.Error("the add should have gone through")
	}
}

// disabledMcpServers is keyed by name, and the move keeps the name: a
// server switched off in Claude Code stays off after it, wherever it went,
// and the entry — in the project record, say, when the server went to user —
// is left where it is rather than moved or dropped.
func TestMoveKeepsDisabledState(t *testing.T) {
	root, claudePath := moveFixture(t)
	write(t, claudePath, `{
  "mcpServers": {"gl": {"url": "https://x/gl"}},
  "projects": {`+jsonStr(root)+`: {"mcpServers": {"pr": {"url": "https://x/pr"}}, "disabledMcpServers": ["pr", "ws"]}}
}`)
	cat := load(t, root)
	for _, tc := range []struct{ name, to string }{{"pr", OriginUser}, {"ws", OriginLocal}} {
		s, _ := cat.Find(tc.name)
		if !s.Disabled {
			t.Fatalf("%s should start disabled", tc.name)
		}
		m, err := cat.Move(tc.name, tc.to, s.Spec)
		if err != nil {
			t.Fatal(err)
		}
		if !m.Disabled || len(m.Notes()) == 0 || !strings.Contains(m.Notes()[0], "disabled") {
			t.Errorf("moved = %+v, notes = %v; what happened to the disabled state must be said", m, m.Notes())
		}
		cat = load(t, root)
		after, _ := cat.Find(tc.name)
		if after.Origin != tc.to || !after.Disabled || after.DisabledAs != tc.name {
			t.Errorf("%s after move = %+v; want origin %s, still disabled", tc.name, after, tc.to)
		}
	}
	data, _ := os.ReadFile(claudePath)
	var top struct {
		Disabled []string `json:"disabledMcpServers"`
		Projects map[string]struct {
			Disabled []string `json:"disabledMcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if strings.Join(top.Projects[root].Disabled, ",") != "pr,ws" || len(top.Disabled) != 0 {
		t.Errorf("disabled lists = %v / %v; the project entry should be kept as it was", top.Projects[root].Disabled, top.Disabled)
	}
}

// Plugin servers live in the plugin's own files, which mcpick does not edit:
// they can be neither moved out nor moved onto.
func TestMoveRefusesPlugin(t *testing.T) {
	root, _ := moveFixture(t)
	cat := load(t, root)
	cat.Servers = append(cat.Servers, Server{Name: "cf", Origin: OriginPlugin, Spec: httpSpec("https://cf")})
	if _, err := cat.Move("cf", OriginUser, httpSpec("https://cf")); err == nil || !strings.Contains(err.Error(), "plugin") {
		t.Errorf("out of a plugin: err = %v", err)
	}
	s, _ := cat.Find("ws")
	if _, err := cat.Move("ws", OriginPlugin, s.Spec); err == nil {
		t.Error("into the plugin group must be refused")
	}
	if d := cat.Destinations("cf"); d != nil {
		t.Errorf("destinations of a plugin server = %v, want none", d)
	}
	if d := cat.Destinations("pr"); strings.Join(d, ",") != "project,user" {
		t.Errorf("destinations of a local server = %v", d)
	}
}

// The spec moves as written: ${VAR} and {UUID} are placeholders in the
// destination too, never expanded on the way, and the move says that Claude
// Code will only expand them when the variable is set.
func TestMoveKeepsPlaceholders(t *testing.T) {
	root, claudePath := moveFixture(t)
	write(t, filepath.Join(root, ".mcp.yaml"), "servers:\n  ws:\n    type: http\n    url: https://x/ws?s={UUID}\n    headers:\n      Authorization: Bearer ${WS_TOKEN}\n")
	t.Setenv("WS_TOKEN", "live-secret")
	cat := load(t, root)
	s, _ := cat.Find("ws")
	m, err := cat.Move("ws", OriginUser, s.Spec)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(claudePath)
	if !strings.Contains(string(data), "${WS_TOKEN}") || !strings.Contains(string(data), "{UUID}") || strings.Contains(string(data), "live-secret") {
		t.Errorf("placeholders were not kept as written:\n%s", data)
	}
	if !m.Placeholders || !strings.Contains(strings.Join(m.Notes(), "\n"), "expands them only") {
		t.Errorf("moved = %+v, notes = %v; the ${VAR} caveat must be said", m, m.Notes())
	}
	// Back into the project catalog: a placeholder is not a secret, no caveat.
	cat = load(t, root)
	s, _ = cat.Find("ws")
	m, err = cat.Move("ws", OriginProject, s.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Notes()) != 0 {
		t.Errorf("notes = %v, want none", m.Notes())
	}
	if y, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml")); !strings.Contains(string(y), "${WS_TOKEN}") {
		t.Errorf("catalog = %s", y)
	}
}

// A move within ~/.claude.json writes it twice, well inside one second. Each
// write keeps its own backup, and the oldest is the file as it was before the
// move: backups once named by the second alone overwrote each other, and a
// restore from the one left brought back a half-moved server.
func TestMoveKeepsABackupOfTheFileBeforeIt(t *testing.T) {
	root, claudePath := moveFixture(t)
	original, _ := os.ReadFile(claudePath)
	cat := load(t, root)
	s, _ := cat.Find("pr")
	if _, err := cat.Move("pr", OriginUser, s.Spec); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(claudePath + ".mcpick-bak-*")
	if len(backups) != 2 {
		t.Fatalf("backups = %v, want one per write", backups)
	}
	sort.Strings(backups)
	if first, _ := os.ReadFile(backups[0]); string(first) != string(original) {
		t.Errorf("the oldest backup is not the file before the move:\n%s", first)
	}
}
