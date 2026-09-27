package backend

import (
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// opencode reads its config from XDG_CONFIG_HOME, so it runs against an
// overlay. Its format is its own: a command is one array, the environment is
// "environment", and every entry says whether it is local or remote.

func init() {
	Register(overlayBackend{
		Meta: Meta{
			Name: "opencode", Glyph: "▣", Color: "#F97316", Docs: "https://opencode.ai/docs/mcp-servers/",
			AlsoReads: []Source{
				{Path: "{root}/opencode.json", Keys: []string{"mcp"}},
			},
		},
		env: "XDG_CONFIG_HOME", dir: fsutil.XDGConfigHome, file: "opencode/opencode.json", topKey: "mcp",
		dialect: opencodeDialect{},
	})
}

type opencodeDialect struct{}

func (opencodeDialect) Emit(sel spec.Selection) ([]byte, error) {
	servers := map[string]any{}
	for _, n := range sel.Names {
		v := spec.ViewOf(sel.Specs[n])
		entry := map[string]any{"enabled": true}
		for k, val := range spec.Extras("opencode", v) {
			entry[k] = val
		}
		if v.Remote() {
			entry["type"] = "remote"
			entry["url"] = v.URL
			if len(v.Headers) > 0 {
				entry["headers"] = spec.StringMap(v.Headers)
			}
		} else {
			entry["type"] = "local"
			entry["command"] = append([]any{v.Command}, spec.AnySlice(v.Args)...)
			if len(v.Env) > 0 {
				entry["environment"] = spec.StringMap(v.Env)
			}
		}
		servers[n] = entry
	}
	return spec.MarshalIndented(map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"mcp":     servers,
	})
}
