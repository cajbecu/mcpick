package backend

import (
	"github.com/cajbecu/mcpick/internal/spec"
)

// Devin CLI has no way to point one run at another config, so mcpick rewrites
// the project's and restores it on exit. Its format is Claude's. Since Devin
// CLI 3 the servers live in .devin/mcp_config.json; a mcpServers block found
// in the older .devin/config.json is migrated into that file on start-up,
// which would leave a copy of the selection in a file the restore knows
// nothing about.

func init() {
	Register(projectBackend{
		Meta: Meta{
			Name: "devin", Glyph: "◈", Color: "#6366F1", Docs: "https://cli.devin.ai/docs/extensibility/mcp/configuration",
		},
		file: ".devin/mcp_config.json", topKey: "mcpServers",
		dialect: spec.JSON{Agent: "devin", TopKey: "mcpServers", Type: true},
	})
}
