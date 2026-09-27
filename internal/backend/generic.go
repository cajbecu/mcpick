package backend

import (
	"path/filepath"
	"strings"

	"github.com/cajbecu/mcpick/internal/spec"
)

// generic is not registered: it is what Pick falls back to for a command no
// backend claims.
var generic Backend = genericBackend{}

// genericBackend covers anything mcpick has no dialect for: the command runs
// untouched with MCPICK_CONFIG pointing at a Claude-shaped file.
type genericBackend struct{}

func (genericBackend) Info() Meta {
	return Meta{
		Name:    "generic",
		Glyph:   "?",
		Summary: "MCPICK_CONFIG points at a Claude-shaped config; the command is not modified",
	}
}

func (genericBackend) Dialect() spec.Dialect { return spec.Claude }

func (genericBackend) Preview(_ spec.Selection, argv []string) Preview {
	return Preview{
		Env:  []string{"MCPICK_CONFIG=<rendered config>"},
		Argv: argv,
		Note: "unknown agent: the command is not changed",
	}
}

func (genericBackend) Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error) {
	data, err := spec.Claude.Emit(sel)
	if err != nil {
		return Plan{}, err
	}
	path, err := writeRuntime(ctx, "config.json", data)
	if err != nil {
		return Plan{}, err
	}
	name := strings.TrimSuffix(filepath.Base(argv[0]), ".exe")
	return Plan{
		Argv: argv,
		Env:  []string{"MCPICK_CONFIG=" + path},
		Notes: []string{name + " is not an agent mcpick knows; running it unchanged with MCPICK_CONFIG set. " +
			"If it wraps one, use --agent NAME (mcpick agents lists them)."},
	}, nil
}
