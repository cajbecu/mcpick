package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/spec"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serverNames(c *Catalog) []string {
	out := make([]string, 0, len(c.Servers))
	for _, s := range c.Servers {
		out = append(out, s.Name)
	}
	return out
}

func TestWorkspaceRootPrefersCatalogThenGit(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := WorkspaceRoot(sub); got != root {
		t.Errorf("git root = %q, want %q", got, root)
	}

	// A catalog closer to the cwd wins over the git root above it.
	write(t, filepath.Join(root, "a", ".mcp.yaml"), "servers: {}\n")
	if got, want := WorkspaceRoot(sub), filepath.Join(root, "a"); got != want {
		t.Errorf("catalog root = %q, want %q", got, want)
	}
}

func TestCatalogPathPrefersYAMLThenJSON(t *testing.T) {
	root := t.TempDir()
	if got, want := Path(root), filepath.Join(root, ".mcp.yaml"); got != want {
		t.Errorf("empty dir = %q, want the yaml default %q", got, want)
	}
	write(t, filepath.Join(root, ".mcp.json"), "{}")
	if got, want := Path(root), filepath.Join(root, ".mcp.json"); got != want {
		t.Errorf("json only = %q, want %q", got, want)
	}
	write(t, filepath.Join(root, ".mcp.yaml"), "servers: {}\n")
	if got, want := Path(root), filepath.Join(root, ".mcp.yaml"); got != want {
		t.Errorf("both present = %q, want the yaml %q", got, want)
	}
}

// A repo carrying the ecosystem-standard .mcp.json must work with no
// conversion step, which is the whole point of reading both.
func TestLoadCatalogReadsJSONAndYAML(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	write(t, filepath.Join(root, ".mcp.yaml"), `
servers:
  from-yaml:
    type: http
    url: https://example.com/yaml
profiles:
  review: [from-yaml]
`)
	write(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {"from-json": {"type": "http", "url": "https://example.com/json"}}
}`)

	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	names := serverNames(cat)
	if len(names) != 2 || names[0] != "from-yaml" || names[1] != "from-json" {
		t.Fatalf("names = %v, want [from-yaml from-json]", names)
	}
	if got := cat.Profiles["review"]; len(got) != 1 || got[0] != "from-yaml" {
		t.Errorf("profiles = %v", cat.Profiles)
	}
}

func TestLoadCatalogFirstMatchWins(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	write(t, filepath.Join(root, ".mcp.yaml"), `
servers:
  shared:
    type: http
    url: https://example.com/from-catalog
`)
	write(t, filepath.Join(home, ".claude.json"), `{
  "mcpServers": {"shared": {"type":"http","url":"https://example.com/from-global"}},
  "projects": {`+jsonStr(root)+`: {"mcpServers": {"shared": {"type":"http","url":"https://example.com/from-project"}}}}
}`)

	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Servers) != 1 {
		t.Fatalf("got %d servers, want the duplicate collapsed", len(cat.Servers))
	}
	if got := cat.Servers[0].Endpoint(); got != "https://example.com/from-catalog" {
		t.Errorf("target = %q, want the catalog entry to win", got)
	}
}

// Claude Code keys projects by the path it was launched from. A symlinked
// checkout resolves to a different string, and looking up only one of them is
// how project servers silently disappear.
func TestProjectKeyFollowsSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}

	top := map[string]json.RawMessage{
		"projects": json.RawMessage(`{` + jsonStr(resolved) + `: {"mcpServers": {}}}`),
	}
	if got := projectKey(top, link, link); got != resolved {
		t.Errorf("projectKey = %q, want the resolved path %q", got, resolved)
	}
}

func TestDisabledServersAreFlagged(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	write(t, filepath.Join(home, ".claude.json"), `{
  "mcpServers": {"a": {"type":"http","url":"https://x/a"}, "b": {"type":"http","url":"https://x/b"}},
  "disabledMcpServers": ["b"]
}`)

	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cat.Servers {
		if s.Name == "b" && !s.Disabled {
			t.Error("b is in disabledMcpServers and must be flagged")
		}
		if s.Name == "a" && s.Disabled {
			t.Error("a is not disabled")
		}
	}
}

// A malformed block used to make servers vanish without a word.
func TestMalformedClaudeBlockWarnsInsteadOfVanishing(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers": ["not", "a", "map"]}`)

	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Warnings) == 0 {
		t.Fatal("a malformed mcpServers block must produce a warning")
	}
	if !strings.Contains(cat.Warnings[0], "malformed") {
		t.Errorf("warning = %q", cat.Warnings[0])
	}
}

func TestYAMLAddUpdateDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.yaml")
	spec := map[string]any{"type": "http", "url": "https://x/1"}
	if err := AddServer(path, "one", spec); err != nil {
		t.Fatal(err)
	}
	if err := AddServer(path, "one", map[string]any{"type": "http", "url": "https://x/2"}); err != nil {
		t.Fatal(err)
	}
	names, specs, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("names = %v, want a single updated entry", names)
	}
	if specs["one"]["url"] != "https://x/2" {
		t.Errorf("url = %v, want the update to win", specs["one"]["url"])
	}
	if err := DeleteServer(path, "one"); err != nil {
		t.Fatal(err)
	}
	names, _, _, _ = ReadFile(path)
	if len(names) != 0 {
		t.Errorf("names = %v after delete, want none", names)
	}
}

func TestYAMLEditsKeepMcpServersKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.yaml")
	write(t, path, "mcpServers:\n  one:\n    type: http\n    url: https://x/1\n")
	if err := AddServer(path, "two", map[string]any{"type": "http", "url": "https://x/2"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "servers:\n") && !strings.Contains(string(body), "mcpServers:") {
		t.Fatalf("a second servers key was invented:\n%s", body)
	}
	names, _, _, _ := ReadFile(path)
	if len(names) != 2 {
		t.Errorf("names = %v, want both entries under the original key", names)
	}
}

func TestJSONCatalogEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	write(t, path, `{"mcpServers": {"keep": {"type":"http","url":"https://x/keep"}}, "other": 1}`)
	if err := AddServer(path, "added", map[string]any{"type": "http", "url": "https://x/a"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["other"]; !ok {
		t.Error("unrelated keys must survive an edit")
	}
	names, _, _, _ := ReadFile(path)
	if len(names) != 2 {
		t.Errorf("names = %v, want keep and added", names)
	}
}

func TestSaveProfileRoundTrip(t *testing.T) {
	for _, name := range []string{".mcp.yaml", ".mcp.json"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := AddServer(path, "a", map[string]any{"type": "http", "url": "https://x/a"}); err != nil {
				t.Fatal(err)
			}
			if err := SaveProfile(path, "review", []string{"a", "b"}); err != nil {
				t.Fatal(err)
			}
			names, _, profiles, err := ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(names) != 1 {
				t.Errorf("servers = %v, saving a profile must not disturb them", names)
			}
			if got := profiles["review"]; len(got) != 2 || got[0] != "a" {
				t.Errorf("profiles = %v", profiles)
			}
		})
	}
}

// ~/.claude.json is mostly somebody else's state — conversation history,
// project records, onboarding flags. Rewriting it through a Go map would
// reshuffle every key in a file the user never asked mcpick to reformat.
func TestDeleteKeepsTopLevelOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{
  "numStartups": 42,
  "mcpServers": {"keep": {"url": "https://x/keep"}, "drop": {"url": "https://x/drop"}},
  "projects": {"/w": {"history": ["a"]}},
  "theme": "dark"
}`)

	if err := DeleteFromClaudeJSON(path, "/w", "drop", OriginGlobal); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	order, err := spec.TopLevelOrder(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"numStartups", "mcpServers", "projects", "theme"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("key order = %v, want %v", order, want)
		}
	}
	if strings.Contains(string(data), "drop") {
		t.Error("the server was not removed")
	}
	if !strings.Contains(string(data), "keep") || !strings.Contains(string(data), `"a"`) {
		t.Error("unrelated state was lost")
	}
}

func TestDeleteWritesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	write(t, path, `{"mcpServers":{"drop":{"url":"https://x"}}}`)

	if err := DeleteFromClaudeJSON(path, "/w", "drop", OriginGlobal); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "mcpick-bak") {
			found = true
			info, err := e.Info()
			if err != nil {
				t.Fatal(err)
			}
			if unixPerms && info.Mode().Perm() != 0o600 {
				t.Errorf("backup mode = %v, want 0600", info.Mode().Perm())
			}
		}
	}
	if !found {
		t.Fatal("no backup was written")
	}
}

func TestDeleteProjectScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{"projects":{"/w":{"mcpServers":{"drop":{"url":"https://x"},"keep":{"url":"https://y"}}}}}`)

	if err := DeleteFromClaudeJSON(path, "/w", "drop", OriginProject); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var top struct {
		Projects map[string]struct {
			McpServers map[string]any `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	servers := top.Projects["/w"].McpServers
	if _, ok := servers["drop"]; ok {
		t.Error("drop survived")
	}
	if _, ok := servers["keep"]; !ok {
		t.Error("keep was removed too")
	}
}

func TestDeleteMissingServerIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{"mcpServers":{"a":{"url":"https://x"}}}`)
	if err := DeleteFromClaudeJSON(path, "/w", "nope", OriginGlobal); err == nil {
		t.Fatal("deleting a server that is not there must report it")
	}
}

func TestResolveKeepsCatalogOrder(t *testing.T) {
	cat := sampleCatalog()
	sel := map[string]bool{"neo": true, "ahrefs": true}
	got, err := Resolve(cat, sel, "uid-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Names) != 2 || got.Names[0] != "ahrefs" || got.Names[1] != "neo" {
		t.Fatalf("names = %v, want [ahrefs neo]", got.Names)
	}
	if u := got.Specs["neo"]["url"]; u != "http://127.0.0.1:9010/mcp?session=uid-1" {
		t.Errorf("uid was not substituted: %v", u)
	}
	// The catalog itself keeps its placeholder.
	if u := cat.Servers[6].Spec["url"]; !strings.Contains(u.(string), "{UUID}") {
		t.Errorf("catalog spec was mutated: %v", u)
	}
}

func sampleCatalog() *Catalog {
	return &Catalog{
		Path:       "/workspace/.mcp.yaml",
		ClaudePath: "/home/agent/.claude.json",
		ProjectKey: "/workspace",
		Profiles:   map[string][]string{"review": {"ahrefs", "freshdesk"}},
		Servers: []Server{
			{Name: "ahrefs", Origin: OriginWorkspace, Spec: httpSpec("https://api.ahrefs.com/mcp/mcp")},
			{Name: "local-tool", Origin: OriginWorkspace, Spec: map[string]any{
				"type": "stdio", "command": "uvx",
				"args": []any{"some-mcp", "--session", "{UUID}"}}},
			{Name: "claude_design", Origin: OriginProject, Spec: httpSpec("https://api.anthropic.com/v1/design/mcp")},
			{Name: "google-webmaster", Origin: OriginProject, Spec: httpSpec("https://mcp.example.com/google-webmaster")},
			{Name: "playwright-mcp", Origin: OriginProject, Spec: httpSpec("http://playwright-mcp:8931/mcp")},
			{Name: "freshdesk", Origin: OriginGlobal, Spec: httpSpec("https://mcp.example.com/freshdesk")},
			{Name: "neo", Origin: OriginGlobal, Spec: httpSpec("http://127.0.0.1:9010/mcp?session={UUID}")},
		},
	}
}

func httpSpec(u string) map[string]any {
	return map[string]any{"type": "http", "url": u}
}

// jsonStr quotes s as a JSON string. Test fixtures splice paths into JSON, and
// a Windows path's backslashes are escape sequences until they are quoted.
func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// unixPerms says whether mode bits mean anything here. Windows reports 0666 or
// 0777 whatever was asked for; access there is governed by per-user ACLs.
var unixPerms = runtime.GOOS != "windows"
