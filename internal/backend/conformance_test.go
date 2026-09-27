package backend

import (
	"os"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/spec"
)

// Every registered backend has to hold up to the same checks, so adding an
// agent is adding a file, not remembering what the others were tested for.
func TestConformance(t *testing.T) {
	if len(All()) == 0 {
		t.Fatal("no backend registered")
	}
	names := map[string]bool{}
	for _, b := range append(All(), generic) {
		in := b.Info()
		t.Run(in.Name, func(t *testing.T) {
			for _, n := range append([]string{in.Name}, in.Aliases...) {
				if names[n] {
					t.Errorf("%q is claimed twice", n)
				}
				names[n] = true
			}
			if in.Glyph == "" || in.Summary == "" {
				t.Errorf("glyph %q, summary %q: neither may be empty", in.Glyph, in.Summary)
			}
			// The generic backend is plain on purpose: it is not a brand.
			if b != generic {
				if in.Color == "" {
					t.Error("no colour")
				}
				if !strings.HasPrefix(in.Docs, "https://") {
					t.Errorf("docs = %q; link what the adapter was written against", in.Docs)
				}
				if got, err := Pick("", []string{"/usr/local/bin/" + in.Name}); err != nil || got.Info().Name != in.Name {
					t.Errorf("the command %s does not pick its own backend", in.Name)
				}
			}

			// The rendered config carries every selected server, local and
			// remote.
			sel := spec.Selection{
				Names: []string{"local-srv", "remote-srv"},
				Specs: map[string]map[string]any{
					"local-srv":  {"type": "stdio", "command": "uvx", "args": []any{"pkg"}},
					"remote-srv": {"type": "http", "url": "https://example.com/mcp"},
				},
			}
			data, err := b.Dialect().Emit(sel)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"local-srv", "remote-srv", "uvx", "https://example.com/mcp"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("config is missing %s:\n%s", want, data)
				}
			}

			// A preview keeps the user's arguments and does nothing.
			dir := t.TempDir()
			t.Chdir(dir)
			pv := b.Preview(sel, []string{in.Name, "--flag"})
			if len(pv.Argv) == 0 || pv.Argv[len(pv.Argv)-1] != "--flag" {
				t.Errorf("preview lost the user's arguments: %v", pv.Argv)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Error("a preview wrote to disk")
			}
		})
	}
}
