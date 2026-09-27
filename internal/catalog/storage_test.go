package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Every picker adding a server writes the same file; none of the adds may
// be lost to another's read-modify-write. The goroutines start together so
// the edits overlap.
func TestConcurrentAddServerKeepsEvery(t *testing.T) {
	for _, file := range []string{".mcp.yaml", ".mcp.json"} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), file)
			const n = 24
			start := make(chan struct{})
			var wg sync.WaitGroup
			errs := make(chan error, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					if err := AddServer(path, fmt.Sprintf("s%d", i), map[string]any{"url": fmt.Sprintf("https://x/%d", i)}); err != nil {
						errs <- err
					}
				}(i)
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
			names, _, _, err := ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(names) != n {
				t.Errorf("%d servers in the file, want %d: concurrent adds lost some", len(names), n)
			}
			if _, err := os.Stat(path + ".mcpick-lock"); err == nil {
				t.Error("the lock was not released")
			}
		})
	}
}

// A catalog file whose shape is not a mapping of names to servers is
// refused, not written into: a server "added" to `servers: []` lands
// nowhere a reader looks, and a second YAML document was dropped on write.
// A null block, or an empty file, is the one shape an add may fill in.
func TestCatalogEditRefusesWrongShape(t *testing.T) {
	refused := map[string]string{
		"servers is a list":      "servers: []\n",
		"servers is a scalar":    "servers: none\n",
		"root is a list":         "- a\n",
		"root is a scalar":       "just text\n",
		"two documents":          "---\nservers: {}\n---\nservers: {}\n",
		"json servers is a list": `{"mcpServers": []}`,
	}
	for name, body := range refused {
		t.Run(name, func(t *testing.T) {
			file := ".mcp.yaml"
			if strings.HasPrefix(body, "{") {
				file = ".mcp.json"
			}
			path := filepath.Join(t.TempDir(), file)
			write(t, path, body)
			if err := AddServer(path, "a", map[string]any{"url": "https://a"}); err == nil {
				t.Fatal("the add was not refused")
			}
			if got, _ := os.ReadFile(path); string(got) != body {
				t.Errorf("a refused add rewrote the file:\n%s", got)
			}
			if err := DeleteServer(path, "a"); err == nil {
				t.Error("the delete was not refused")
			}
			if err := DeleteProfile(path, "p"); err == nil {
				t.Error("the profile delete was not refused")
			}
		})
	}

	filled := map[string]string{
		"empty file":         "",
		"comments only":      "# nothing yet\n",
		"null document":      "null\n",
		"servers is null":    "servers:\n",
		"json servers null":  `{"mcpServers": null}`,
		"json without block": `{"other": 1}`,
	}
	for name, body := range filled {
		t.Run(name, func(t *testing.T) {
			file := ".mcp.yaml"
			if strings.HasPrefix(body, "{") {
				file = ".mcp.json"
			}
			path := filepath.Join(t.TempDir(), file)
			write(t, path, body)
			if err := AddServer(path, "a", map[string]any{"url": "https://a"}); err != nil {
				t.Fatal(err)
			}
			names, specs, _, err := ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(names) != 1 || specs["a"]["url"] != "https://a" {
				t.Errorf("after the add the file reads as %v %v; want the one server", names, specs)
			}
		})
	}
}

// A multi-document file is refused on load too, rather than read as its
// first document alone.
func TestLoadRefusesMultiDocumentYAML(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	write(t, filepath.Join(root, ".mcp.yaml"), "servers:\n  a:\n    url: https://a\n---\nservers:\n  b:\n    url: https://b\n")
	if _, err := Load(filepath.Join(root, ".mcp.yaml"), root, root); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("err = %v, want the second document refused", err)
	}
}

// The picker holds the catalog as it was when it opened. If the source
// entry was edited since — by hand, or by another session — the move must
// not carry the old definition over and delete the new one: it refuses,
// says so, and writes nothing.
func TestMoveRefusesWhenSourceChangedSinceLoad(t *testing.T) {
	root, claudePath := moveFixture(t)
	cat := load(t, root)
	yaml := filepath.Join(root, ".mcp.yaml")

	// Workspace source edited after the load.
	write(t, yaml, "servers:\n  ws:\n    type: http\n    url: https://x/ws-edited\n")
	claudeBefore, _ := os.ReadFile(claudePath)
	s, _ := cat.Find("ws")
	_, err := cat.Move("ws", OriginUser, s.Spec)
	if err == nil || !strings.Contains(err.Error(), "changed since the picker loaded it") {
		t.Fatalf("err = %v, want the change reported", err)
	}
	if got, _ := os.ReadFile(yaml); !strings.Contains(string(got), "ws-edited") {
		t.Error("the edited source was deleted")
	}
	if got, _ := os.ReadFile(claudePath); string(got) != string(claudeBefore) {
		t.Error("a refused move wrote ~/.claude.json")
	}

	// ~/.claude.json source edited after the load; the workspace file is
	// the destination.
	write(t, claudePath, `{"numStartups": 1, "mcpServers": {"gl": {"type": "http", "url": "https://x/gl-edited"}}}`)
	yamlBefore, _ := os.ReadFile(yaml)
	g, _ := cat.Find("gl")
	_, err = cat.Move("gl", OriginProject, g.Spec)
	if err == nil || !strings.Contains(err.Error(), "changed since the picker loaded it") {
		t.Fatalf("err = %v, want the change reported", err)
	}
	if got, _ := os.ReadFile(claudePath); !strings.Contains(string(got), "gl-edited") {
		t.Error("the edited source was deleted")
	}
	if got, _ := os.ReadFile(yaml); string(got) != string(yamlBefore) {
		t.Error("a refused move wrote the catalog")
	}

	// A source that went is a change too.
	write(t, claudePath, `{"numStartups": 1, "mcpServers": {}}`)
	if _, err := cat.Move("gl", OriginProject, g.Spec); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Errorf("err = %v, want a gone source reported as changed", err)
	}
}

// The check under the source lock, right before the delete, is what holds
// when the edit lands between the pre-check and the delete: a definition
// other than the one expected is left alone.
func TestDeleteServerWithExpectLeavesAChangedEntry(t *testing.T) {
	for _, file := range []string{".mcp.yaml", ".mcp.json"} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), file)
			if err := AddServer(path, "a", map[string]any{"url": "https://a/new"}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			err := deleteServer(path, "a", map[string]any{"url": "https://a/old"})
			if err == nil || !strings.Contains(err.Error(), "changed since") {
				t.Fatalf("err = %v, want the change reported", err)
			}
			if got, _ := os.ReadFile(path); string(got) != string(before) {
				t.Error("the changed entry was deleted or the file rewritten")
			}
			if err := deleteServer(path, "a", map[string]any{"url": "https://a/new"}); err != nil {
				t.Fatal(err)
			}
			if names, _, _, _ := ReadFile(path); len(names) != 0 {
				t.Errorf("the expected entry was not deleted: %v", names)
			}
		})
	}
}

// A name taken in the destination after the picker loaded is found under
// the destination's lock as the server is added: the move refuses and the
// destination's own definition is not written over.
func TestMoveRefusesADestinationTakenSinceLoad(t *testing.T) {
	root, claudePath := moveFixture(t)
	cat := load(t, root)
	yaml := filepath.Join(root, ".mcp.yaml")
	write(t, yaml, "servers:\n  ws:\n    type: http\n    url: https://x/ws\n  gl:\n    type: http\n    url: https://x/gl-theirs\n")
	g, _ := cat.Find("gl")
	if _, err := cat.Move("gl", OriginProject, g.Spec); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want the taken name refused", err)
	}
	if got, _ := os.ReadFile(yaml); !strings.Contains(string(got), "gl-theirs") {
		t.Error("the destination's definition was written over")
	}
	if got, _ := os.ReadFile(claudePath); !strings.Contains(string(got), `"gl"`) {
		t.Error("the source was deleted")
	}
}

// A destination of the wrong shape must not cost the source: the add is
// refused, and the source stays. Both the catalog file and ~/.claude.json
// are covered.
func TestMoveKeepsSourceWhenDestinationHasWrongShape(t *testing.T) {
	root, claudePath := moveFixture(t)
	cat := load(t, root)
	yaml := filepath.Join(root, ".mcp.yaml")
	claudeBefore, _ := os.ReadFile(claudePath)

	for _, body := range []string{"servers: []\n", "- a\n", "---\nservers: {}\n---\nservers: {}\n"} {
		write(t, yaml, body)
		g, _ := cat.Find("gl")
		if _, err := cat.Move("gl", OriginProject, g.Spec); err == nil {
			t.Errorf("%q: the move succeeded", body)
		}
		if got, _ := os.ReadFile(claudePath); string(got) != string(claudeBefore) {
			t.Errorf("%q: the source was deleted or ~/.claude.json rewritten", body)
		}
		if got, _ := os.ReadFile(yaml); string(got) != body {
			t.Errorf("%q: the malformed destination was rewritten:\n%s", body, got)
		}
	}

	// The other way: ~/.claude.json's global map is a list.
	write(t, yaml, "servers:\n  ws:\n    type: http\n    url: https://x/ws\n")
	write(t, claudePath, `{"numStartups": 1, "mcpServers": [], "projects": {`+jsonStr(root)+`: {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}}}}`)
	cat = load(t, root)
	s, _ := cat.Find("ws")
	if _, err := cat.Move("ws", OriginUser, s.Spec); err == nil {
		t.Error("the move into a list succeeded")
	}
	if got, _ := os.ReadFile(yaml); !strings.Contains(string(got), "ws") {
		t.Error("the source was deleted")
	}
	var top map[string]json.RawMessage
	data, _ := os.ReadFile(claudePath)
	if json.Unmarshal(data, &top) != nil || string(top["mcpServers"]) != "[]" {
		t.Errorf("the malformed destination was rewritten:\n%s", data)
	}
}
