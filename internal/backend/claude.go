package backend

import (
	"fmt"
	"strings"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Claude Code takes a config file for one session on its command line:
// --mcp-config <file> --strict-mcp-config, and nothing on disk changes.

func init() { Register(claudeBackend{}) }

// claudeSubcommands are the argv[1] values that make claude do something other
// than start a session; injecting session flags in front of them changes what
// runs, and `claude mcp list` ignores --mcp-config anyway
// (anthropics/claude-code#15388).
var claudeSubcommands = map[string]bool{
	"mcp": true, "config": true, "doctor": true, "update": true, "plugin": true,
	"install": true, "migrate-installer": true, "setup-token": true, "agents": true,
}

type claudeBackend struct{}

func (claudeBackend) Info() Meta {
	return Meta{
		Name:           "claude",
		Glyph:          "✻",
		Color:          "#D97757",
		ClaudeSettings: true,
		ValueFlags:     []string{"--agent", "-p", "--print"},
		Summary:        "--mcp-config + --strict-mcp-config; nothing written outside the runtime dir",
		Docs:           "https://code.claude.com/docs/en/mcp",
	}
}

func (claudeBackend) Dialect() spec.Dialect { return spec.Claude }

func (claudeBackend) Preview(_ spec.Selection, argv []string) Preview {
	if len(argv) > 1 && claudeSubcommands[argv[1]] {
		return Preview{Argv: argv, Note: argv[1] + " is a claude subcommand: no flags added"}
	}
	return Preview{Argv: append([]string{argv[0], "--mcp-config", "<rendered config>", "--strict-mcp-config"}, argv[1:]...)}
}

func (b claudeBackend) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	data, err := spec.Claude.Emit(sel)
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

// Prepare deals with Claude Code's list of disabled servers, which it honours
// even for servers passed with --mcp-config (anthropics/claude-code#14490):
//
//   - a disabled server the user checked in the picker is switched back on in
//     Claude Code, for good;
//   - one selected without the picker (--select, --profile, -y), which must not
//     change Claude's settings on its own, runs for this session only, under a
//     name the disabled list does not contain.
func (claudeBackend) Prepare(p Prep, sel *spec.Selection) ([]string, error) {
	notes, err := enableInClaude(p)
	if err != nil {
		return notes, err
	}
	return append(notes, sessionAliases(p.Catalog, sel)...), nil
}

func enableInClaude(p Prep) ([]string, error) {
	var entries, names []string
	for i := range p.Catalog.Servers {
		s := &p.Catalog.Servers[i]
		if s.Disabled && p.Reenable[s.Name] {
			entries = append(entries, s.DisabledAs)
			names = append(names, s.Name)
			s.Disabled, s.DisabledAs = false, ""
		}
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if err := catalog.EnableInClaude(p.Catalog.ClaudePath, p.Catalog.ProjectKey, entries); err != nil {
		return nil, fmt.Errorf("re-enabling %s in Claude Code: %w", strings.Join(names, ", "), err)
	}
	return []string{"re-enabled in Claude Code: " + strings.Join(names, ", ")}, nil
}

// sessionAliases hands a selected disabled server to Claude under an alias. A
// plugin's server needs none: Claude disables it under its namespaced name,
// which the plain name already differs from.
func sessionAliases(cat *catalog.Catalog, sel *spec.Selection) []string {
	taken := map[string]bool{}
	for _, s := range cat.Servers {
		taken[s.Name] = true
		if s.DisabledAs != "" {
			taken[s.DisabledAs] = true
		}
	}
	var notes []string
	for i, name := range sel.Names {
		s, ok := cat.Find(name)
		if !ok || !s.Disabled {
			continue
		}
		if s.DisabledAs != s.Name {
			notes = append(notes, name+" is disabled in Claude Code; running it for this session")
			continue
		}
		alias := name + "_mcpick"
		for n := 2; taken[alias]; n++ {
			alias = fmt.Sprintf("%s_mcpick%d", name, n)
		}
		taken[alias] = true
		sel.Names[i] = alias
		sel.Specs[alias] = sel.Specs[name]
		delete(sel.Specs, name)
		notes = append(notes, fmt.Sprintf("%s is disabled in Claude Code; running it for this session as %s "+
			"(permission rules for mcp__%s__* do not apply to it)", name, alias, name))
	}
	return notes
}
