package backend

import (
	"github.com/cajbecu/mcpick/internal/spec"
)

// Grok CLI has no way to point one run at another config, so mcpick rewrites
// the project's and restores it on exit. It also reads Claude's and Cursor's
// files, which mcpick can only warn about.

func init() {
	Register(projectBackend{
		Meta: Meta{
			Name: "grok", Glyph: "✕", Color: "#E5E5E5", Docs: "https://docs.x.ai/build/features/mcp-servers",
			AlsoReads: []Source{
				{Path: "{home}/.grok/config.toml", TOML: true},
				{Path: "{home}/.claude.json", Keys: []string{"mcpServers"}},
				{Path: "{root}/.mcp.json", Keys: []string{"mcpServers"}},
				{Path: "{root}/.cursor/mcp.json", Keys: []string{"mcpServers"}},
			},
		},
		file: ".grok/config.toml", toml: true,
		dialect: spec.TOML{Agent: "grok", Headers: "headers"},
	})
}
