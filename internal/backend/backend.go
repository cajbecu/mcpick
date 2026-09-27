// Package backend holds the agents mcpick can launch. Each agent is a
// backend in a file of its own (claude.go, codex.go, ...) that registers
// itself; most are built from two shared mechanisms, an overlay of the
// agent's config directory (overlay.go) and a project file rewritten for the
// run (projectfile.go). Adding an agent means adding a file.
package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajbecu/mcpick/internal/catalog"
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

// Backend is one agent mcpick knows how to launch. Each lives in its own
// file and registers itself (see Register); the shared mechanisms most of
// them are built from are overlayBackend and projectBackend.
type Backend interface {
	Info() Meta
	// Dialect writes a selection in the agent's config format; it is also
	// what `mcpick export` prints.
	Dialect() spec.Dialect
	// Plan prepares a launch: the command, its environment, and the undo.
	Plan(ctx Ctx, sel spec.Selection, argv []string) (Plan, error)
	// Preview describes what Plan would do, without doing any of it.
	Preview(sel spec.Selection, argv []string) Preview
}

// Preparer is implemented by a backend that has work to do before Plan that
// goes beyond its own files — Claude Code's settings, for Claude. Notes are
// shown to the user; an error stops the launch.
type Preparer interface {
	Prepare(p Prep, sel *spec.Selection) (notes []string, err error)
}

// Prep is what Prepare gets to work with.
type Prep struct {
	Catalog *catalog.Catalog
	// Reenable holds servers disabled in Claude Code that the user checked
	// in the picker, asking for them to be switched back on.
	Reenable map[string]bool
}

// Meta is what every backend shares: how it is named, how it is reached,
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
	// Owns are catalog keys only this agent understands. They are written
	// for it and kept out of every other agent's config.
	Owns []string
	// ClaudeSettings marks the agent that honours Claude Code's list of
	// disabled servers, so the picker treats those servers specially.
	ClaudeSettings bool
	// ValueFlags are the agent's own flags that take a value and that
	// could be mistaken for mcpick's, or be followed by text that looks
	// like one (claude's --agent and -p with its prompt, codex's --profile
	// and -p). After `run <cmd>` such a flag is the agent's and its value
	// is skipped, so neither is reported as a misplaced mcpick option.
	ValueFlags []string
}

// Source is one config file an agent reads MCP servers from.
type Source struct {
	Path string // {root}, {home} and {xdg} are expanded
	TOML bool
	Keys []string // JSON keys holding the server map, tried in order
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

// DisplayPath is a Source path as `mcpick agents` prints it: `{home}` as
// `~`, `{root}` as `.` and `{xdg}` as the XDG config directory shortened
// the same way, so the reader sees a path and not a template. It is not
// cleaned, which would turn `./.mcp.json` into `.mcp.json`, and it keeps
// the templates' forward slashes on every platform.
func DisplayPath(p string) string {
	p = strings.ReplaceAll(p, "{root}", ".")
	p = strings.ReplaceAll(p, "{home}", "~")
	return strings.ReplaceAll(p, "{xdg}", filepath.ToSlash(fsutil.ShortenHome(fsutil.XDGConfigHome())))
}

func sourceServers(path string, src Source) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if src.TOML {
		return spec.TOMLServerNames(data)
	}
	// The top level is decoded loosely: a scalar key beside the server
	// map — opencode.json's "$schema" — used to fail the whole decode, and
	// the servers the agent loads anyway went unreported.
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return nil
	}
	for _, k := range src.Keys {
		var m map[string]any
		if raw, ok := top[k]; ok && json.Unmarshal(raw, &m) == nil && len(m) > 0 {
			return spec.SortedKeys(m)
		}
	}
	return nil
}

// lossy asks a dialect what it cannot carry.
func lossy(d spec.Dialect, sel spec.Selection) []string {
	if l, ok := d.(spec.Lossy); ok {
		return l.Lossy(sel)
	}
	return nil
}

// home defers a home-relative path until it is needed, so a backend
// registered at start-up follows HOME as the process sees it at launch.
func home(parts ...string) func() string {
	return func() string { return fsutil.Home(parts...) }
}
