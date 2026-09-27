package backend

import (
	"github.com/cajbecu/mcpick/internal/spec"
)

// The Antigravity CLI (agy) is not supported: it reads MCP servers from
// ~/.gemini/config/mcp_config.json and from plugins only. The file 0.1.0
// rewrote for the run, .agents/mcp_config.json, is the IDE's workspace
// file, and the rewrite changed nothing for the CLI — the end-to-end run
// found it listing "No MCP servers configured" whatever the selection.
// Rewriting the global file would reach every workspace, well beyond one
// project's launch. So `mcpick run agy` launches the CLI unchanged and says
// so, once. The dialect stays: `mcpick --agent antigravity export` writes
// the shape of the IDE's workspace file (serverUrl), for whoever keeps that
// file by hand (decision #64).

const agyNotice = "antigravity: the agy CLI is not supported — it reads ~/.gemini/config/mcp_config.json and plugins only, not the selection; agy is launched unchanged"

func init() { Register(antigravityBackend{}) }

type antigravityBackend struct{}

func (antigravityBackend) Info() Meta {
	return Meta{
		Name: "antigravity", Glyph: "▲", Color: "#34A853", Aliases: []string{"agy"}, Docs: "https://antigravity.google/docs/mcp/",
		Summary: "not supported (CLI): agy reads ~/.gemini/config/mcp_config.json and plugins only, so it is launched unchanged; export writes the IDE's .agents/mcp_config.json shape",
	}
}

func (antigravityBackend) Dialect() spec.Dialect {
	return spec.JSON{Agent: "antigravity", TopKey: "mcpServers", URLKey: "serverUrl"}
}

func (antigravityBackend) Preview(_ spec.Selection, argv []string) Preview {
	return Preview{Argv: argv, Note: "not supported: agy loads its global servers, not the selection; launched unchanged"}
}

func (antigravityBackend) Plan(_ Ctx, _ spec.Selection, argv []string) (Plan, error) {
	return Plan{Argv: argv, Notes: []string{agyNotice}}, nil
}
