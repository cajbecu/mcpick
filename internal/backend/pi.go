package backend

import (
	"github.com/cajbecu/mcpick/internal/spec"
)

// pi reads MCP servers through pi-mcp-adapter, from PI_CODING_AGENT_DIR, so it
// runs against an overlay.

func init() {
	Register(overlayBackend{
		Meta: Meta{
			Name: "pi", Glyph: "π", Color: "#7C3AED", Docs: "https://github.com/nicobailon/pi-mcp-adapter",
			AlsoReads: []Source{
				{Path: "{xdg}/mcp/mcp.json", Keys: []string{"mcpServers"}},
				{Path: "{home}/.agents/mcp.json", Keys: []string{"mcpServers"}},
				{Path: "{home}/.agents/mcp/mcp.json", Keys: []string{"mcpServers"}},
				{Path: "{root}/.mcp.json", Keys: []string{"mcpServers"}},
			},
			Owns: []string{
				"lifecycle", "idleTimeout", "directTools", "debug",
				"inheritEnv", "literalEnv", "protocolVersion",
			},
		},
		env: "PI_CODING_AGENT_DIR", dir: home(".pi", "agent"), file: "mcp.json", topKey: "mcpServers",
		dialect: spec.JSON{Agent: "pi", TopKey: "mcpServers"},
	})
}
