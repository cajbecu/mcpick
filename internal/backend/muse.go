package backend

import (
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Muse reads its settings from XDG_CONFIG_HOME, so it runs against an overlay.
// The settings file is versioned: without schema_version muse calls it
// malformed and refuses to start, so the dialect writes the version along
// with the servers, and Splice keeps it only where the file has none.

func init() {
	Register(overlayBackend{
		Meta: Meta{
			Name: "muse", Glyph: "◆", Color: "#0668E1", Docs: "https://porteden.com/blog/muse-code-mcp-servers/",
		},
		env: "XDG_CONFIG_HOME", dir: fsutil.XDGConfigHome, file: "muse/settings.json", topKey: "mcpServers",
		dialect: museDialect{},
	})
}

type museDialect struct{}

func (museDialect) Emit(sel spec.Selection) ([]byte, error) {
	entries := spec.JSON{Agent: "muse", TopKey: "mcpServers", Type: true}
	servers := map[string]any{}
	for _, n := range sel.Names {
		servers[n] = entries.Entry(spec.ViewOf(sel.Specs[n]))
	}
	return spec.MarshalIndented(map[string]any{"schema_version": 1, "mcpServers": servers})
}
