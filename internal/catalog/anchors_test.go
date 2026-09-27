package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// anchoredCatalog shares b's spec with c through an alias, and a list of
// names between two profiles.
const anchoredCatalog = `servers:
  a:
    command: echo
  b: &b
    type: http
    url: https://x/b
  c: *b
profiles:
  one: &names [a, b]
  two: *names
`

// Deleting the node an anchor is on, while an alias elsewhere still uses it,
// would write `*b` with no `&b`: a file no YAML reader accepts, and every
// server in it lost. The delete is refused, the file untouched.
func TestDeleteRefusesAnAnchorUsedElsewhere(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.yaml")
	write(t, path, anchoredCatalog)
	err := DeleteServer(path, "b")
	if err == nil || !strings.Contains(err.Error(), "anchor &b") {
		t.Fatalf("DeleteServer(b) = %v; want a refusal naming the anchor", err)
	}
	if err := DeleteProfile(path, "one"); err == nil || !strings.Contains(err.Error(), "anchor &names") {
		t.Fatalf("DeleteProfile(one) = %v; want a refusal naming the anchor", err)
	}
	if data, _ := os.ReadFile(path); string(data) != anchoredCatalog {
		t.Errorf("the file changed:\n%s", data)
	}
	// What uses the anchor, or has none, goes as before.
	if err := DeleteServer(path, "c"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProfile(path, "two"); err != nil {
		t.Fatal(err)
	}
	names, specs, profiles, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "a,b" || specs["b"]["url"] != "https://x/b" || len(profiles) != 1 {
		t.Errorf("after deleting c and two: %v %v %v", names, specs, profiles)
	}
	// With nothing left using it, the anchored entry can go too.
	if err := DeleteServer(path, "b"); err != nil {
		t.Fatal(err)
	}
}

// A server written over (AddServer replaces) must not strand an alias
// either.
func TestReplaceRefusesAnAnchorUsedElsewhere(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.yaml")
	write(t, path, anchoredCatalog)
	if err := AddServer(path, "b", map[string]any{"type": "http", "url": "https://x/new"}); err == nil || !strings.Contains(err.Error(), "anchor &b") {
		t.Fatalf("AddServer over b = %v; want a refusal", err)
	}
	if data, _ := os.ReadFile(path); string(data) != anchoredCatalog {
		t.Errorf("the file changed:\n%s", data)
	}
}

// Whatever the checks before it miss, a write is read back first: bytes
// that no longer parse, or that lose a server, are not written.
func TestWriteIsReadBackBeforeItLands(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.yaml")
	write(t, path, anchoredCatalog)
	doc, want, err := readYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	servers, _, _ := serversBlock(doc.Content[0], path)
	deleteMapEntry(servers, "b") // strands c's alias, unchecked
	delete(want, "b")
	if err := writeYAMLDoc(path, doc, want); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("writeYAMLDoc = %v; want the stranded alias caught", err)
	}
	doc, want, _ = readYAML(path)
	servers, _, _ = serversBlock(doc.Content[0], path)
	deleteMapEntry(servers, "a")
	if err := writeYAMLDoc(path, doc, want); err == nil || !strings.Contains(err.Error(), "change a") {
		t.Errorf("writeYAMLDoc = %v; want the lost server caught", err)
	}
	if data, _ := os.ReadFile(path); string(data) != anchoredCatalog {
		t.Errorf("the file changed:\n%s", data)
	}
}

// A move out of the catalog of a server whose entry holds an anchor used
// elsewhere is refused before anything is written: the destination does not
// get a copy and the source keeps the server.
func TestMoveRefusesAnAnchoredSource(t *testing.T) {
	root, claudePath := moveFixture(t)
	yml := filepath.Join(root, ".mcp.yaml")
	write(t, yml, "servers:\n  ws: &w\n    type: http\n    url: https://x/ws\n  ws2: *w\n")
	before, _ := os.ReadFile(claudePath)
	cat := load(t, root)
	s, _ := cat.Find("ws")
	if _, err := cat.Move("ws", OriginUser, s.Spec); err == nil || !strings.Contains(err.Error(), "anchor &w") {
		t.Fatalf("Move = %v; want a refusal naming the anchor", err)
	}
	if after, _ := os.ReadFile(claudePath); string(after) != string(before) {
		t.Errorf("~/.claude.json was written:\n%s", after)
	}
	if names, _, _, err := ReadFile(yml); err != nil || strings.Join(names, ",") != "ws,ws2" {
		t.Errorf("catalog = %v, %v", names, err)
	}
}
