package backend

import (
	"strings"

	"github.com/cajbecu/mcpick/internal/spec"
)

// Gemini CLI has no way to point one run at another settings file, so mcpick
// rewrites the project's and restores it on exit.

// geminiSubcommands are the argv[1] values that make gemini manage things
// rather than start a session. yargs rejects a session flag put in front of
// them ("Unknown arguments: allowed-mcp-server-names"), so `gemini mcp list`
// would never run; and a listing has no session to restrict anyway. The
// project file is still rewritten: it is what the listing reads.
var geminiSubcommands = map[string]bool{
	"mcp": true, "extensions": true, "extension": true, "skills": true, "skill": true,
	"hooks": true, "hook": true, "gemma": true,
}

func init() {
	Register(projectBackend{
		Meta: Meta{
			Name: "gemini", Glyph: "✧", Color: "#4285F4", Docs: "https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md",
			// No AlsoReads: --allowed-mcp-server-names filters the user
			// settings and extensions too, so gemini ends up strict.
			Owns: []string{
				"timeout", "trust", "includeTools", "excludeTools",
				"authProviderType", "targetAudience", "targetServiceAccount",
				"cwd", "oauth",
			},
		},
		file: ".gemini/settings.json", topKey: "mcpServers",
		// Gemini tells the transports apart by the key alone.
		dialect: spec.JSON{Agent: "gemini", TopKey: "mcpServers", URLKey: "httpUrl", SSEURLKey: "url"},
		// Gemini expands `$VAR` and `${VAR}` in a server's env block itself
		// (docs/tools/mcp-server.md, "Environment variable expansion"; an
		// unset variable becomes the empty string, as it does here), so
		// those references are written as the catalog has them and the
		// secret stays out of settings.json. It documents no expansion for
		// headers, url or args: those are written expanded.
		envRefs: true,
		extraArgs: func(sel spec.Selection, argv []string) []string {
			if sel.Empty() || (len(argv) > 1 && geminiSubcommands[argv[1]]) {
				return nil
			}
			return []string{"--allowed-mcp-server-names", strings.Join(sel.Names, ",")}
		},
	})
}
