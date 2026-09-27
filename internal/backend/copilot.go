package backend

import (
	"github.com/cajbecu/mcpick/internal/spec"
)

// GitHub Copilot CLI reads its config from COPILOT_HOME, so it runs against an
// overlay.

func init() {
	Register(overlayBackend{
		Meta: Meta{
			Name: "copilot", Glyph: "✦", Color: "#8957E5", Docs: "https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers",
			// Project files override the user config and are read from every
			// directory up to the repository root.
			AlsoReads: []Source{
				{Path: "{root}/.mcp.json", Keys: []string{"mcpServers"}},
				{Path: "{root}/.github/mcp.json", Keys: []string{"mcpServers"}},
			},
			Owns: []string{"tools"},
		},
		env: "COPILOT_HOME", dir: home(".copilot"), file: "mcp-config.json", topKey: "mcpServers",
		// Copilot's user config lists the tools to expose; without "tools"
		// the server connects and offers nothing.
		dialect: spec.JSON{Agent: "copilot", TopKey: "mcpServers", Type: true,
			Defaults: map[string]any{"tools": []any{"*"}}},
	})
}
