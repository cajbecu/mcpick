package backend

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cajbecu/mcpick/internal/spec"
)

// registry holds every backend, by name and by alias.
var (
	registered []Backend
	byName     = map[string]Backend{}
)

// Register adds a backend. Every backend calls it from an init function in its
// own file. A name or alias used twice is a programming error and stops the
// program at start-up, not at some launch later.
func Register(b Backend) {
	in := b.Info()
	if in.Name == "" {
		panic("backend: registered without a name")
	}
	for _, n := range append([]string{in.Name}, in.Aliases...) {
		if prev, dup := byName[n]; dup {
			panic(fmt.Sprintf("backend: %q is claimed by both %s and %s", n, prev.Info().Name, in.Name))
		}
		byName[n] = b
	}
	spec.Own(in.Name, in.Owns...)
	registered = append(registered, b)
}

// All returns every registered backend, by name.
func All() []Backend {
	out := append([]Backend(nil), registered...)
	sort.Slice(out, func(i, j int) bool { return out[i].Info().Name < out[j].Info().Name })
	return out
}

// Names lists every name and alias a backend is known by, by name, for
// suggestions.
func Names() []string {
	var out []string
	for _, b := range All() {
		in := b.Info()
		out = append(append(out, in.Name), in.Aliases...)
	}
	return out
}

// Pick resolves --agent, else the basename of the command. A command no
// backend claims gets the generic one.
func Pick(explicit string, argv []string) (Backend, error) {
	want := explicit
	if want == "" && len(argv) > 0 {
		want = strings.TrimSuffix(filepath.Base(argv[0]), ".exe")
	}
	if b, ok := byName[want]; ok {
		return b, nil
	}
	if explicit != "" && explicit != generic.Info().Name {
		return nil, fmt.Errorf("unknown agent %q (mcpick agents lists them)", explicit)
	}
	return generic, nil
}
