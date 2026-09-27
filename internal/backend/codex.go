package backend

import (
	"github.com/cajbecu/mcpick/internal/spec"
)

// Codex reads its config from CODEX_HOME, so it runs against an overlay.

func init() {
	Register(overlayBackend{
		Meta: Meta{
			Name: "codex", Glyph: "◉", Color: "#10A37F", Docs: "https://developers.openai.com/codex/mcp",
			AlsoReads: []Source{{Path: "{root}/.codex/config.toml", TOML: true}},
			Owns: []string{
				"bearer_token_env_var", "env_http_headers", "env_vars",
				"startup_timeout_sec", "tool_timeout_sec", "enabled_tools",
				"disabled_tools", "required", "auth", "cwd",
				"default_tools_approval_mode", "http_headers_helper",
			},
			ValueFlags: []string{"--profile", "-p"},
		},
		env: "CODEX_HOME", dir: home(".codex"), file: "config.toml", toml: true,
		dialect: spec.TOML{Agent: "codex", Headers: "http_headers", NoSSE: true},
	})
}
