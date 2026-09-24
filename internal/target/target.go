// Package target knows the agents mcpick can launch: where each keeps its MCP
// config, what dialect that config is in, and how to hand it a selection for a
// single run without taking over the user's files.
package target

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Plan is everything mcpick has to do to hand a selection to one agent:
// the argv to run, the environment to run it in, and the undo. A plan with a
// Cleanup cannot be exec'd — mcpick has to stay alive to run it.
type Plan struct {
	Argv    []string
	Env     []string
	Cleanup func()
	Notes   []string
}

func (p Plan) Mutates() bool { return p.Cleanup != nil }

// Ctx is what a plan needs to know about the launch.
type Ctx struct {
	UID     string // --uid, sanitised into file names
	Root    string // the workspace root
	Runtime string // private directory for rendered configs
	State   string // mcpick's state directory, for restore records
	PID     int    // this process; exec keeps it for the agent
}

// Preview is what a launch will run, for showing before it happens: the
// command line as the agent will receive it, the variables mcpick sets, and
// what it changes on disk. Paths that only exist once the launch starts are
// shown as placeholders.
type Preview struct {
	Env  []string
	Argv []string
	Note string
}

// Target is one agent mcpick knows how to launch.
type Target interface {
	Info() Meta
	// Emit renders the selection in this tool's dialect, for `mcpick export`.
	Emit(sel spec.Selection) ([]byte, error)
	Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error)
	// Preview describes what Plan would do, without doing any of it.
	Preview(sel spec.Selection, argv []string) Preview
}

// Meta is what every target shares: how it is named, how it is reached,
// where its behaviour is documented, and which other files the agent reads
// MCP servers from behind mcpick's back.
type Meta struct {
	Name    string
	Aliases []string
	Summary string
	Docs    string
	// Glyph and Color make the agent recognisable at a glance in the picker.
	// Glyphs are plain Unicode that every monospace font carries — brand
	// logos do not exist as characters, and Nerd Font icons would be empty
	// boxes for anyone without one. Colours are the brands' own, roughly.
	Glyph string
	Color string
	// AlsoReads are sources the agent merges in on its own. Servers found
	// there are loaded whatever the selection says, and mcpick cannot switch
	// them off — it can only say so.
	AlsoReads []Source
}

// Source is one config file an agent reads MCP servers from.
type Source struct {
	Path string // {root}, {home} and {xdg} are expanded
	TOML bool
	Keys []string // JSON keys holding the server map, tried in order
}

func All() []Target {
	return []Target{
		claudeTarget{},

		// Redirect the tool's config directory at an overlay, so the generated
		// config is the only file that is not the user's own.
		homeTarget{
			Meta: Meta{
				Name: "codex", Glyph: "◉", Color: "#10A37F", Docs: "https://developers.openai.com/codex/mcp",
				AlsoReads: []Source{{Path: "{root}/.codex/config.toml", TOML: true}},
			},
			env: "CODEX_HOME", src: fsutil.Home(".codex"), rel: "config.toml", toml: true, emitFn: spec.EmitCodex,
		},
		homeTarget{
			Meta: Meta{
				Name: "copilot", Glyph: "✦", Color: "#8957E5", Docs: "https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers",
				// Project files override the user config and are read from
				// every directory up to the repository root.
				AlsoReads: []Source{
					{Path: "{root}/.mcp.json", Keys: []string{"mcpServers"}},
					{Path: "{root}/.github/mcp.json", Keys: []string{"mcpServers"}},
				},
			},
			env: "COPILOT_HOME", src: fsutil.Home(".copilot"), rel: "mcp-config.json", topKey: "mcpServers", emitFn: spec.EmitCopilot,
		},
		homeTarget{
			Meta: Meta{
				Name: "pi", Glyph: "π", Color: "#7C3AED", Docs: "https://github.com/nicobailon/pi-mcp-adapter",
				AlsoReads: []Source{
					{Path: "{xdg}/mcp/mcp.json", Keys: []string{"mcpServers"}},
					{Path: "{home}/.agents/mcp.json", Keys: []string{"mcpServers"}},
					{Path: "{home}/.agents/mcp/mcp.json", Keys: []string{"mcpServers"}},
					{Path: "{root}/.mcp.json", Keys: []string{"mcpServers"}},
				},
			},
			env: "PI_CODING_AGENT_DIR", src: fsutil.Home(".pi", "agent"), rel: "mcp.json", topKey: "mcpServers", emitFn: spec.EmitPi,
		},
		homeTarget{
			Meta: Meta{
				Name: "muse", Glyph: "◆", Color: "#0668E1", Docs: "https://porteden.com/blog/muse-code-mcp-servers/",
			},
			env: "XDG_CONFIG_HOME", src: fsutil.XDGConfigHome(), rel: "muse/settings.json", topKey: "mcp_servers", emitFn: spec.EmitMuse,
		},
		homeTarget{
			Meta: Meta{
				Name: "opencode", Glyph: "▣", Color: "#F97316", Docs: "https://opencode.ai/docs/mcp-servers/",
				AlsoReads: []Source{
					{Path: "{root}/opencode.json", Keys: []string{"mcp"}},
				},
			},
			env: "XDG_CONFIG_HOME", src: fsutil.XDGConfigHome(), rel: "opencode/opencode.json", topKey: "mcp", emitFn: spec.EmitOpencode,
		},

		// No config-directory override: mcpick writes the project file, runs
		// the agent, and puts the file back.
		projectTarget{
			Meta: Meta{
				Name: "gemini", Glyph: "✧", Color: "#4285F4", Docs: "https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md",
				// Nothing listed: --allowed-mcp-server-names filters the user
				// settings and extensions too, so gemini ends up strict.
			},
			rel: ".gemini/settings.json", topKey: "mcpServers", emitFn: spec.EmitGemini,
			extraArgs: func(sel spec.Selection) []string {
				if sel.Empty() {
					return nil
				}
				return []string{"--allowed-mcp-server-names", strings.Join(sel.Names, ",")}
			},
		},
		projectTarget{
			Meta: Meta{
				Name: "antigravity", Glyph: "▲", Color: "#34A853", Aliases: []string{"agy"}, Docs: "https://antigravity.google/docs/mcp/",
				AlsoReads: []Source{{Path: "{home}/.gemini/config/mcp_config.json", Keys: []string{"mcpServers"}}},
			},
			rel: ".agents/mcp_config.json", topKey: "mcpServers", emitFn: spec.EmitAntigravity,
		},
		projectTarget{
			Meta: Meta{
				Name: "grok", Glyph: "✕", Color: "#E5E5E5", Docs: "https://docs.x.ai/build/features/mcp-servers",
				AlsoReads: []Source{
					{Path: "{home}/.grok/config.toml", TOML: true},
					{Path: "{home}/.claude.json", Keys: []string{"mcpServers"}},
					{Path: "{root}/.mcp.json", Keys: []string{"mcpServers"}},
					{Path: "{root}/.cursor/mcp.json", Keys: []string{"mcpServers"}},
				},
			},
			rel: ".grok/config.toml", toml: true, emitFn: spec.EmitGrok,
		},
		projectTarget{
			Meta: Meta{
				Name: "devin", Glyph: "◈", Color: "#6366F1", Docs: "https://cli.devin.ai/docs/extensibility/mcp/overview",
			},
			rel: ".devin/config.json", topKey: "mcpServers", emitFn: spec.EmitClaude,
		},
	}
}

// Pick resolves --target, else the basename of the command. A command
// mcpick has no adapter for gets the generic target.
func Pick(explicit string, argv []string) (Target, error) {
	want := explicit
	if want == "" && len(argv) > 0 {
		want = strings.TrimSuffix(filepath.Base(argv[0]), ".exe")
	}
	for _, t := range All() {
		in := t.Info()
		if in.Name == want {
			return t, nil
		}
		for _, a := range in.Aliases {
			if a == want {
				return t, nil
			}
		}
	}
	if explicit != "" && explicit != "generic" {
		return nil, fmt.Errorf("unknown target %q (see mcpick targets)", explicit)
	}
	return genericTarget{}, nil
}

// runtimeName is the file name of one launch's artefact; see
// fsutil.RuntimeName for why it leads with the pid and the host.
func runtimeName(ctx Ctx, name string) string {
	return fsutil.RuntimeName(ctx.PID, fsutil.Sanitize(ctx.UID)+"-"+name)
}

// writeRuntime drops a rendered config in the private runtime directory. The
// file holds expanded secrets, so it is 0600 inside a 0700 directory and is
// removed by pruneRuntime once its session is gone.
func writeRuntime(ctx Ctx, name string, data []byte) (string, error) {
	path := filepath.Join(ctx.Runtime, runtimeName(ctx, name))
	if err := fsutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Leaks reports servers the agent will load from files mcpick does not
// control. The selection is then a floor, not a ceiling, and the user should
// know that before they rely on it.
func Leaks(in Meta, root string, sel spec.Selection) []string {
	chosen := map[string]bool{}
	for _, n := range sel.Names {
		chosen[n] = true
	}
	var out []string
	for _, src := range in.AlsoReads {
		path := expandSourcePath(src.Path, root)
		names := sourceServers(path, src)
		var extra []string
		for _, n := range names {
			if !chosen[n] {
				extra = append(extra, n)
			}
		}
		if len(extra) > 0 {
			out = append(out, fmt.Sprintf("%s also loads %s from %s; mcpick cannot switch those off",
				in.Name, strings.Join(extra, ", "), fsutil.ShortenHome(path)))
		}
	}
	return out
}

func expandSourcePath(p, root string) string {
	p = strings.ReplaceAll(p, "{root}", root)
	p = strings.ReplaceAll(p, "{home}", fsutil.Home())
	p = strings.ReplaceAll(p, "{xdg}", fsutil.XDGConfigHome())
	return filepath.Clean(p)
}

func sourceServers(path string, src Source) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if src.TOML {
		return spec.TOMLServerNames(data)
	}
	var top map[string]map[string]any
	if json.Unmarshal(data, &top) != nil {
		return nil
	}
	for _, k := range src.Keys {
		if m, ok := top[k]; ok && len(m) > 0 {
			return spec.SortedKeys(m)
		}
	}
	return nil
}

// --- claude ---------------------------------------------------------------

// claudeSubcommands are the argv[1] values that make claude do something other
// than start a session; injecting session flags in front of them changes what
// runs, and `claude mcp list` ignores --mcp-config anyway
// (anthropics/claude-code#15388).
var claudeSubcommands = map[string]bool{
	"mcp": true, "config": true, "doctor": true, "update": true, "plugin": true,
	"install": true, "migrate-installer": true, "setup-token": true, "agents": true,
}

type claudeTarget struct{}

func (claudeTarget) Info() Meta {
	return Meta{
		Name:    "claude",
		Glyph:   "✻",
		Color:   "#D97757",
		Summary: "--mcp-config + --strict-mcp-config; nothing written outside the runtime dir",
		Docs:    "https://code.claude.com/docs/en/mcp",
	}
}

func (claudeTarget) Emit(sel spec.Selection) ([]byte, error) { return spec.EmitClaude(sel) }

func (claudeTarget) Preview(_ spec.Selection, argv []string) Preview {
	if len(argv) > 1 && claudeSubcommands[argv[1]] {
		return Preview{Argv: argv, Note: argv[1] + " is a claude subcommand: no flags added"}
	}
	return Preview{Argv: append([]string{argv[0], "--mcp-config", "<rendered config>", "--strict-mcp-config"}, argv[1:]...)}
}

func (t claudeTarget) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	data, err := t.Emit(sel)
	if err != nil {
		return Plan{}, err
	}
	path, err := writeRuntime(ctx, "claude.json", data)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Argv: argv,
		Env:  []string{"MCPICK_CONFIG=" + path},
	}
	if len(argv) > 1 && claudeSubcommands[argv[1]] {
		plan.Notes = append(plan.Notes,
			fmt.Sprintf("%q is a claude subcommand: session flags not injected", argv[1]))
		return plan, nil
	}
	plan.Argv = append([]string{argv[0], "--mcp-config", path, "--strict-mcp-config"}, argv[1:]...)
	return plan, nil
}

// --- generic --------------------------------------------------------------

// genericTarget covers anything mcpick has no dialect for: the command runs
// untouched with MCPICK_CONFIG pointing at a Claude-shaped file.
type genericTarget struct{}

func (genericTarget) Info() Meta {
	return Meta{
		Name:    "generic",
		Glyph:   "?",
		Summary: "MCPICK_CONFIG points at a Claude-shaped config; the command is not modified",
	}
}

func (genericTarget) Emit(sel spec.Selection) ([]byte, error) { return spec.EmitClaude(sel) }

func (genericTarget) Preview(_ spec.Selection, argv []string) Preview {
	return Preview{
		Env:  []string{"MCPICK_CONFIG=<rendered config>"},
		Argv: argv,
		Note: "unknown agent: the command is not changed",
	}
}

func (t genericTarget) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	data, err := t.Emit(sel)
	if err != nil {
		return Plan{}, err
	}
	path, err := writeRuntime(ctx, "config.json", data)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Argv:  argv,
		Env:   []string{"MCPICK_CONFIG=" + path},
		Notes: []string{"no adapter for this command: only MCPICK_CONFIG is set"},
	}, nil
}
