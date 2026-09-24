// Package cli is mcpick's command line: argument parsing and one function per
// command, wired to the internal packages that do the work.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/oauth"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/proxy"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/target"
	"github.com/cajbecu/mcpick/internal/tui"
)

const usage = `mcpick — choose which MCP servers a session loads, at the moment you launch it.

  mcpick [opts] run <cmd> [args...]   pick, render a config, launch cmd
  mcpick [opts] list                  print the merged catalog, no TUI
  mcpick [opts] doctor                connect to the selected servers and report
  mcpick [opts] measure               refresh the context-cost estimates
  mcpick [opts] export                print the selection in an agent's dialect
  mcpick [opts] serve                 run as one MCP server fronting the selection
  mcpick [opts] import                seed the catalog from ~/.claude.json
  mcpick [opts] profile [list]        list the profiles in the catalog
  mcpick [opts] profile save <name>   save the selection (or --select) as a profile
  mcpick [opts] profile delete <name> remove a profile
  mcpick [opts] login <server>        run the OAuth flow for a remote server
  mcpick [opts] logout <server>       forget a stored token
  mcpick        targets               list the agents mcpick knows how to launch
  mcpick        restore               undo project files left behind by a crash

Options:
  --uid ID         session id; keys the saved selection and replaces {UUID}
                   in the catalog (default: hostname)
  --file PATH      catalog file (default: <root>/.mcp.yaml or <root>/.mcp.json)
  --target NAME    agent dialect to render for (default: from the command name)
  --profile NAME   use a named profile from the catalog, skip the TUI
  --select A,B     use exactly these servers, skip the TUI
  --home DIR       where mcpick keeps its files (default: $MCPICK_HOME, else ~/.mcpick)
  --addr HOST:PORT serve over HTTP on a loopback address instead of stdio
  --timeout DUR    per-server connect timeout (default: 15s)
  --redact         import: rewrite secrets as ${VAR} references
  --json           machine-readable output for list, doctor, measure, targets
  -y, --last       skip the TUI, reuse the saved selection
  --all            select every server, skip the TUI
  --none           select nothing, skip the TUI
  -h, --help       this text
  --version        print version

Options go before the command; everything after run belongs to the agent.

Examples:
  mcpick run claude                         pick in the TUI, then launch
  mcpick -y run codex --full-auto           reuse the last selection
  mcpick --profile review run claude        launch with a saved profile
  mcpick --select github,sentry run gemini  launch with exactly these servers
  mcpick --select github profile save gh    save a profile
  mcpick measure                            see what each server costs

<root> is the nearest ancestor of the current directory holding a catalog file
or a .git, else the current directory.

Servers are merged from these sources, first match wins:
  1. <root>/.mcp.yaml, <root>/.mcp.json          workspace
  2. ~/.claude.json projects[<root>].mcpServers  project
  3. ~/.claude.json mcpServers                   global
  4. enabled Claude Code plugins                 plugin (read-only)

Everything mcpick writes lives under one directory, ~/.mcpick by default:
  selections/  what you picked, per workspace and --uid (server names only)
  state/       OAuth tokens, measured context costs, restore records
  run/         rendered configs and overlays; they hold expanded secrets, are
               0600 inside a 0700 directory, and go when their session ends`

// Version returns the version this binary was built as: the -X ldflag when a
// release build set one, else the module version `go install pkg@v1.2.3`
// records, else "dev".
func Version(linked string) string {
	if linked != "" && linked != "dev" {
		return linked
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

type options struct {
	uid     string
	file    string
	target  string
	profile string
	selects string
	addr    string
	home    string
	timeout time.Duration
	last    bool
	all     bool
	none    bool
	jsonOut bool
	redact  bool
}

// app is one invocation: parsed options, where output goes, and the workspace.
type app struct {
	opt     options
	out     io.Writer
	errw    io.Writer
	version string
	root    string
	cwd     string
	cat     *catalog.Catalog
	// reenable holds the servers the user switched back on in Claude Code
	// from the picker ([x] on a disabled row).
	reenable map[string]bool
}

func (a *app) warnf(format string, args ...any) {
	fmt.Fprintf(a.errw, "mcpick: "+format+"\n", args...)
}

// Main runs mcpick with args (without the program name) and returns the exit
// status.
func Main(args []string, linkedVersion string) int {
	a := &app{out: os.Stdout, errw: os.Stderr, version: Version(linkedVersion)}
	mcp.ClientVersion = a.version
	err := a.run(args)
	if err == nil {
		return 0
	}
	var code proc.ExitCode
	if errors.As(err, &code) {
		return int(code)
	}
	if errors.Is(err, tui.ErrAborted) {
		return 130
	}
	fmt.Fprintln(a.errw, "mcpick:", err)
	return 1
}

func (a *app) run(args []string) error {
	opt, cmd, rest, err := parseArgs(args)
	if err != nil {
		return err
	}
	a.opt = opt
	fsutil.SetMCPickHome(opt.home) // "" falls back to $MCPICK_HOME, then ~/.mcpick

	switch cmd {
	case "help":
		fmt.Fprintln(a.out, usage)
		return nil
	case "version":
		fmt.Fprintln(a.out, "mcpick", a.version)
		return nil
	case "targets":
		return a.targets()
	case "restore":
		return a.restore()
	}

	// Created private before anything lands in it: tokens go in state/, and a
	// home that first appeared through a 0755 MkdirAll would stay world-listable.
	if err := fsutil.EnsureHome(); err != nil {
		return err
	}
	if a.cwd, err = os.Getwd(); err != nil {
		return err
	}
	a.root = catalog.WorkspaceRoot(a.cwd)
	if a.opt.file == "" {
		a.opt.file = catalog.Path(a.root)
	}
	if a.opt.uid == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "default"
		}
		a.opt.uid = host
	}

	if a.cat, err = catalog.Load(a.opt.file, a.root, a.cwd); err != nil {
		return err
	}
	for _, w := range a.cat.Warnings {
		a.warnf("warning: %s", w)
	}

	switch cmd {
	case "list":
		return a.list()
	case "import":
		return a.importClaude()
	case "run":
		return a.launch(rest)
	case "export":
		return a.export()
	case "doctor":
		return a.doctor()
	case "measure":
		return a.measure()
	case "serve":
		return a.serve()
	case "profile":
		return a.profileCmd(rest)
	case "login":
		return a.login(rest)
	case "logout":
		return a.logout(rest)
	}
	return fmt.Errorf("unknown command %q (try --help)", cmd)
}

var commands = map[string]bool{
	"run": true, "list": true, "import": true, "export": true, "doctor": true,
	"measure": true, "serve": true, "targets": true, "restore": true,
	"login": true, "logout": true, "profile": true,
}

// parseArgs accepts flags on either side of the command, because
// `mcpick import --redact` is how people type it. The exception is `run`, a
// hard separator: everything after it is the agent's own command line, so
// mcpick must not interpret a `--all` meant for someone else.
func parseArgs(args []string) (options, string, []string, error) {
	opt := options{timeout: 15 * time.Second}
	var cmd string
	var positional []string

	strFlags := map[string]*string{
		"--uid": &opt.uid, "--file": &opt.file, "--target": &opt.target,
		"--profile": &opt.profile, "--select": &opt.selects, "--addr": &opt.addr,
		"--home": &opt.home,
	}

	for i := 0; i < len(args); i++ {
		a := args[i]

		switch a {
		case "help", "-h", "--help":
			return opt, "help", nil, nil
		case "--version", "-V":
			return opt, "version", nil, nil
		case "-y", "--last":
			opt.last = true
			continue
		case "--all":
			opt.all = true
			continue
		case "--none":
			opt.none = true
			continue
		case "--json":
			opt.jsonOut = true
			continue
		case "--redact":
			opt.redact = true
			continue
		}

		if !strings.HasPrefix(a, "-") {
			switch {
			case cmd == "" && commands[a]:
				cmd = a
				if a == "run" {
					return opt, cmd, args[i+1:], nil
				}
			case cmd == "":
				return opt, "", nil, fmt.Errorf("unknown command %q (try --help)", a)
			default:
				positional = append(positional, a)
			}
			continue
		}

		key, raw, hasValue := strings.Cut(a, "=")
		value := func() (string, error) {
			if hasValue {
				return raw, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", key)
			}
			i++
			return args[i], nil
		}

		if dst, ok := strFlags[key]; ok {
			v, err := value()
			if err != nil {
				return opt, "", nil, err
			}
			*dst = v
			continue
		}
		if key == "--timeout" {
			v, err := value()
			if err != nil {
				return opt, "", nil, err
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return opt, "", nil, fmt.Errorf("--timeout: %w", err)
			}
			if d <= 0 {
				return opt, "", nil, fmt.Errorf("--timeout must be positive")
			}
			opt.timeout = d
			continue
		}
		return opt, "", nil, fmt.Errorf("unknown flag %q (try --help)", a)
	}

	if cmd == "" {
		return opt, "", nil, fmt.Errorf("missing command (try --help)")
	}
	return opt, cmd, positional, nil
}

// --- selection --------------------------------------------------------------

// selection resolves what the session will run with, in order: an explicit
// --select, a --profile, --all/--none, the picker (only when launching — launch
// is nil otherwise — and only on a terminal), the saved selection.
func (a *app) selection(launch *tui.Options) (map[string]bool, error) {
	st, err := state.Load(a.root, a.opt.uid)
	if err != nil {
		return nil, err
	}
	sel := map[string]bool{}
	for _, n := range st.Selected {
		sel[n] = true
	}

	switch {
	case a.opt.selects != "":
		return a.parseSelect(a.opt.selects)
	case a.opt.profile != "":
		names, ok := a.cat.Profiles[a.opt.profile]
		if !ok {
			have := strings.Join(a.cat.ProfileNames(), ", ")
			if have == "" {
				have = "none"
			}
			return nil, fmt.Errorf("no profile %q in %s (have: %s)", a.opt.profile,
				fsutil.ShortenHome(a.opt.file), have)
		}
		sel = map[string]bool{}
		for _, n := range names {
			sel[n] = true
		}
	case a.opt.all:
		// "All" never includes a server disabled in Claude Code, as in the
		// picker: turning one back on is an explicit, per-server choice.
		for _, s := range a.cat.Servers {
			if !s.Disabled {
				sel[s.Name] = true
			}
		}
	case a.opt.none:
		sel = map[string]bool{}
	case launch != nil && !a.opt.last && interactive():
		res, err := tui.Run(a.cat, sel, *launch)
		if err != nil {
			return nil, err
		}
		a.reenable = res.Reenable
		return res.Selected, nil
	}
	return sel, nil
}

func (a *app) parseSelect(list string) (map[string]bool, error) {
	sel := map[string]bool{}
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := a.cat.Find(n); !ok {
			return nil, fmt.Errorf("no server named %q in the catalog", n)
		}
		sel[n] = true
	}
	return sel, nil
}

// interactive reports whether a picker can be drawn: bubbletea reads stdin and
// paints stdout, and a redirected stdout would get a screenful of escapes.
func interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func (a *app) saveSelection(sel map[string]bool, targetName string) error {
	st := state.State{Target: targetName}
	for _, s := range a.cat.Servers {
		if sel[s.Name] {
			st.Selected = append(st.Selected, s.Name)
		}
	}
	return state.Save(a.root, a.opt.uid, st)
}

// resolved expands the selection and attaches stored OAuth tokens.
func (a *app) resolved(sel map[string]bool) (spec.Selection, error) {
	r, err := catalog.Resolve(a.cat, sel, a.opt.uid)
	if err != nil {
		return r, err
	}
	for _, n := range oauth.Attach(r) {
		a.warnf("refreshed the OAuth token for %s", n)
	}
	return r, nil
}

// --- commands ---------------------------------------------------------------

func (a *app) launch(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("run needs a command")
	}
	tgt, err := target.Pick(a.opt.target, argv)
	if err != nil {
		return err
	}
	name := tgt.Info().Name

	sel, err := a.selection(&tui.Options{UID: a.opt.uid, Version: a.version, Target: tgt, Argv: argv})
	if err != nil {
		return err
	}
	if err := a.saveSelection(sel, name); err != nil {
		return err
	}
	resolved, err := a.resolved(sel)
	if err != nil {
		return err
	}
	if err := a.enableInClaude(); err != nil {
		return err
	}
	if name == "claude" {
		a.sessionAliases(&resolved)
	}

	stateDir := fsutil.StateDir()
	for _, n := range target.RecoverStale(stateDir, false) {
		a.warnf("%s", n)
	}
	rt, err := fsutil.RuntimeDir()
	if err != nil {
		return err
	}
	fsutil.PruneRuntime(rt, 24*time.Hour)

	plan, err := tgt.Plan(target.Ctx{
		UID: a.opt.uid, Root: a.root, Runtime: rt, State: stateDir, PID: os.Getpid(),
	}, resolved, argv)
	if err != nil {
		return err
	}
	for _, n := range plan.Notes {
		a.warnf("%s", n)
	}
	// MCPICK_DEBUG shows the exact command, with real paths, for when a
	// wrapper hides how mcpick was started.
	if os.Getenv("MCPICK_DEBUG") != "" {
		a.warnf("exec: %s", strings.Join(append(append([]string{}, plan.Env...), proc.ShellQuote(plan.Argv)...), " "))
	}
	return proc.Launch(plan.Argv, plan.Env, plan.Cleanup)
}

// enableInClaude switches back on, in Claude Code itself, the disabled
// servers the user marked [x] in the picker. From then on they are ordinary
// servers again, in every session.
func (a *app) enableInClaude() error {
	var entries, names []string
	for i := range a.cat.Servers {
		s := &a.cat.Servers[i]
		if s.Disabled && a.reenable[s.Name] {
			entries = append(entries, s.DisabledAs)
			names = append(names, s.Name)
			s.Disabled, s.DisabledAs = false, ""
		}
	}
	if len(entries) == 0 {
		return nil
	}
	if err := catalog.EnableInClaude(a.cat.ClaudePath, a.cat.ProjectKey, entries); err != nil {
		return fmt.Errorf("re-enabling %s in Claude Code: %w", strings.Join(names, ", "), err)
	}
	a.warnf("re-enabled in Claude Code: %s", strings.Join(names, ", "))
	return nil
}

// sessionAliases makes a selected server that is disabled in Claude Code run
// for this session without touching Claude's settings. The picker never gets
// here — checking such a server there re-enables it — so this is the path for
// --select, --profile and -y, which must not change Claude's settings on
// their own.
// disabledMcpServers outranks --mcp-config by name (anthropics/claude-code#14490),
// so the server is handed over under a name that list does not contain.
// A plugin's server needs no alias: Claude disables it under its namespaced
// name, which the plain name already differs from.
func (a *app) sessionAliases(sel *spec.Selection) {
	taken := map[string]bool{}
	for _, s := range a.cat.Servers {
		taken[s.Name] = true
		if s.DisabledAs != "" {
			taken[s.DisabledAs] = true
		}
	}
	for i, name := range sel.Names {
		s, ok := a.cat.Find(name)
		if !ok || !s.Disabled {
			continue
		}
		if s.DisabledAs != s.Name {
			a.warnf("%s is disabled in Claude Code; running it for this session", name)
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
		a.warnf("%s is disabled in Claude Code; running it for this session as %s "+
			"(permission rules for mcp__%s__* do not apply to it)", name, alias, name)
	}
}

func (a *app) targets() error {
	all := target.All()
	if a.opt.jsonOut {
		type row struct {
			Name      string   `json:"name"`
			Aliases   []string `json:"aliases,omitempty"`
			Summary   string   `json:"summary"`
			Docs      string   `json:"docs,omitempty"`
			AlsoReads []string `json:"also_reads,omitempty"`
		}
		var rows []row
		for _, t := range all {
			in := t.Info()
			r := row{Name: in.Name, Aliases: in.Aliases, Summary: in.Summary, Docs: in.Docs}
			for _, s := range in.AlsoReads {
				r.AlsoReads = append(r.AlsoReads, s.Path)
			}
			rows = append(rows, r)
		}
		return a.printJSON(rows)
	}
	for _, t := range all {
		in := t.Info()
		fmt.Fprintf(a.out, "%-12s %s\n", in.Name, in.Summary)
		if len(in.AlsoReads) > 0 {
			var paths []string
			for _, s := range in.AlsoReads {
				paths = append(paths, s.Path)
			}
			fmt.Fprintf(a.out, "%-12s also loads servers from %s\n", "", strings.Join(paths, ", "))
		}
		if in.Docs != "" {
			fmt.Fprintf(a.out, "%-12s %s\n", "", in.Docs)
		}
	}
	fmt.Fprintf(a.out, "%-12s %s\n", "(other)", "MCPICK_CONFIG points at a Claude-shaped config; the command is not modified")
	return nil
}

func (a *app) list() error {
	cache := mcp.LoadCache()
	if a.opt.jsonOut {
		type row struct {
			Name     string `json:"name"`
			Origin   string `json:"origin"`
			Endpoint string `json:"endpoint"`
			Source   string `json:"source"`
			Disabled bool   `json:"disabled"`
			Tokens   int    `json:"tokens,omitempty"`
			Error    string `json:"error,omitempty"`
		}
		out := make([]row, 0, len(a.cat.Servers))
		for _, s := range a.cat.Servers {
			r := row{Name: s.Name, Origin: s.Origin, Endpoint: s.Endpoint(), Source: s.Source, Disabled: s.Disabled}
			if m, ok := cache.Get(s.Name, s.Spec); ok {
				r.Tokens, r.Error = m.Tokens, m.Err
			}
			out = append(out, r)
		}
		return a.printJSON(out)
	}

	if len(a.cat.Servers) == 0 {
		fmt.Fprintln(a.out, "no MCP servers found")
		return nil
	}
	w := 0
	for _, s := range a.cat.Servers {
		w = max(w, len(s.Name))
	}
	for _, s := range a.cat.Servers {
		cost := ""
		if m, ok := cache.Get(s.Name, s.Spec); ok {
			if m.OK {
				cost = mcp.HumanTokens(m.Tokens)
			} else {
				cost = mcp.ErrorKind(m.Err)
			}
		}
		flag := ""
		if s.Disabled {
			flag = " (disabled in .claude.json)"
		}
		fmt.Fprintf(a.out, "%-*s  %-9s  %7s  %s%s\n", w, s.Name, s.Origin, cost, s.Endpoint(), flag)
	}
	if len(a.cat.Profiles) > 0 {
		fmt.Fprintln(a.out)
		for _, n := range a.cat.ProfileNames() {
			fmt.Fprintf(a.out, "profile %-12s %s\n", n, strings.Join(a.cat.Profiles[n], ", "))
		}
	}
	return nil
}

func (a *app) importClaude() error {
	n := 0
	var vars []string
	for _, s := range a.cat.Servers {
		if s.Origin != catalog.OriginProject && s.Origin != catalog.OriginGlobal {
			continue
		}
		sp := s.Spec
		if a.opt.redact {
			var found []string
			sp, found = spec.Redact(s.Name, s.Spec)
			vars = append(vars, found...)
		}
		if err := catalog.AddServer(a.opt.file, s.Name, sp); err != nil {
			return err
		}
		n++
	}
	fmt.Fprintf(a.out, "imported %d server(s) into %s\n", n, fsutil.ShortenHome(a.opt.file))
	if !a.opt.redact {
		a.warnf("warning: specs were copied verbatim; re-run with --redact to keep credentials out of the catalog")
		return nil
	}
	if len(vars) == 0 {
		return nil
	}
	sort.Strings(vars)
	fmt.Fprintln(a.out, "\nexport these before launching:")
	for _, v := range vars {
		name, _, _ := strings.Cut(v, "=")
		fmt.Fprintf(a.out, "  export %s=...\n", name)
	}
	a.warnf("the values were left out of the catalog on purpose; they are still in ~/.claude.json")
	return nil
}

func (a *app) export() error {
	var tgt target.Target
	if a.opt.target == "" {
		tgt, _ = target.Pick("claude", nil)
	} else {
		var err error
		if tgt, err = target.Pick(a.opt.target, nil); err != nil {
			return err
		}
	}
	sel, err := a.selection(nil)
	if err != nil {
		return err
	}
	resolved, err := a.resolved(sel)
	if err != nil {
		return err
	}
	data, err := tgt.Emit(resolved)
	if err != nil {
		return err
	}
	_, err = a.out.Write(data)
	return err
}

func (a *app) selected() (spec.Selection, error) {
	sel, err := a.selection(nil)
	if err != nil {
		return spec.Selection{}, err
	}
	return a.resolved(sel)
}

func (a *app) doctor() error {
	resolved, err := a.selected()
	if err != nil {
		return err
	}
	if resolved.Empty() {
		return fmt.Errorf("nothing selected (use --all, --select, --profile, or launch once to save a selection)")
	}
	notes := oauth.AttachClaude(resolved, a.cat.ClaudeNames())
	results := withAuth(mcp.ProbeAll(context.Background(), resolved, a.opt.timeout, 8), notes)

	cache := mcp.LoadCache()
	for _, r := range results {
		cache.Put(r.Name, a.cat.SpecOf(r.Name), r)
	}
	if err := cache.Save(); err != nil {
		a.warnf("could not save measurements: %v", err)
	}

	bad := 0
	for _, r := range results {
		if !r.OK {
			bad++
		}
	}
	if a.opt.jsonOut {
		if err := a.printJSON(results); err != nil {
			return err
		}
	} else {
		w := 0
		for _, r := range results {
			w = max(w, len(r.Name))
		}
		total := 0
		for _, r := range results {
			if r.OK {
				total += r.Tokens
				auth := ""
				if r.Auth != "" {
					auth = "  (auth: " + r.Auth + ")"
				}
				fmt.Fprintf(a.out, "ok    %-*s  %2d tools  ~%-6s %4dms  %s%s\n",
					w, r.Name, r.Tools, mcp.HumanTokens(r.Tokens), r.Millis, r.Server.Name, auth)
				continue
			}
			fmt.Fprintf(a.out, "FAIL  %-*s  %s\n", w, r.Name, r.Err)
			if mcp.NeedsLogin(r.Err) {
				fmt.Fprintf(a.out, "      %-*s  try: mcpick login %s\n", w, "", r.Name)
			}
		}
		fmt.Fprintf(a.out, "\n%d/%d reachable, ~%s of context\n", len(results)-bad, len(results), mcp.HumanTokens(total))
	}
	if bad > 0 {
		return proc.ExitCode(1)
	}
	return nil
}

func (a *app) measure() error {
	// Measuring is about the catalog, not one selection — except for the
	// servers this repository defines. Those run a command or contact a host
	// its author chose, with the environment expanded into them, so they are
	// measured only once selected, the same consent a launch asks for.
	selected, err := a.selection(nil)
	if err != nil {
		return err
	}
	all := spec.Selection{Specs: map[string]map[string]any{}}
	var unresolved []mcp.Result
	var skipped []string
	for _, s := range a.cat.Servers {
		if s.Origin == catalog.OriginWorkspace && !selected[s.Name] {
			skipped = append(skipped, s.Name)
			continue
		}
		e, err := spec.Expand(s.Spec, a.opt.uid)
		if err != nil {
			// A missing ${VAR:?} is a result, not a reason to leave the
			// server out: it is exactly what the user needs to see.
			unresolved = append(unresolved, mcp.Result{Name: s.Name, Err: err.Error()})
			continue
		}
		m, _ := e.(map[string]any)
		all.Names = append(all.Names, s.Name)
		all.Specs[s.Name] = m
	}
	oauth.Attach(all)
	notes := oauth.AttachClaude(all, a.cat.ClaudeNames())
	results := append(withAuth(mcp.ProbeAll(context.Background(), all, a.opt.timeout, 8), notes), unresolved...)

	cache := mcp.LoadCache()
	for _, r := range results {
		cache.Put(r.Name, a.cat.SpecOf(r.Name), r)
	}
	if err := cache.Save(); err != nil {
		return err
	}
	if a.opt.jsonOut {
		return a.printJSON(results)
	}
	if len(skipped) > 0 {
		a.warnf("not measured, defined by this repository and not selected: %s "+
			"(select them, e.g. --select, to measure)", strings.Join(skipped, ", "))
	}
	mcp.SortByCost(results)
	for _, r := range results {
		if !r.OK {
			fmt.Fprintf(a.out, "%-24s  %s\n", r.Name, r.Err)
			continue
		}
		fmt.Fprintf(a.out, "%-24s  %2d tools  ~%s\n", r.Name, r.Tools, mcp.HumanTokens(r.Tokens))
	}
	return nil
}

func (a *app) serve() error {
	resolved, err := a.selected()
	if err != nil {
		return err
	}
	if resolved.Empty() {
		return fmt.Errorf("nothing selected (use --all, --select or --profile)")
	}
	if a.opt.addr != "" {
		if err := proxy.CheckAddr(a.opt.addr); err != nil {
			return err
		}
	}
	p := proxy.New(resolved, a.opt.timeout)
	defer p.Close()

	ctx, stop := signal.NotifyContext(context.Background(),
		append(proc.TerminalSignals(), proc.ForwardedSignals()...)...)
	defer stop()

	if a.opt.addr != "" {
		return p.ServeHTTP(ctx, a.opt.addr)
	}
	a.warnf("serving %d server(s) over stdio", len(resolved.Names))
	return p.ServeStdio(ctx, os.Stdin, a.out)
}

func (a *app) profileCmd(rest []string) error {
	sub := "list"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	switch sub {
	case "list":
		if len(a.cat.Profiles) == 0 {
			fmt.Fprintf(a.out, "no profiles in %s\n", fsutil.ShortenHome(a.opt.file))
			return nil
		}
		if a.opt.jsonOut {
			return a.printJSON(a.cat.Profiles)
		}
		for _, n := range a.cat.ProfileNames() {
			fmt.Fprintf(a.out, "%-12s %s\n", n, strings.Join(a.cat.Profiles[n], ", "))
		}
		return nil
	case "save":
		if len(rest) != 1 {
			return fmt.Errorf("profile save needs a name")
		}
		sel, err := a.selection(nil)
		if err != nil {
			return err
		}
		var names []string
		for _, s := range a.cat.Servers {
			if sel[s.Name] {
				names = append(names, s.Name)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("nothing to save: the selection is empty (use --select a,b)")
		}
		if err := catalog.SaveProfile(a.opt.file, rest[0], names); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "profile %s = %s\n", rest[0], strings.Join(names, ", "))
		return nil
	case "delete", "rm":
		if len(rest) != 1 {
			return fmt.Errorf("profile delete needs a name")
		}
		if err := catalog.DeleteProfile(a.opt.file, rest[0]); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "deleted profile %s\n", rest[0])
		return nil
	}
	return fmt.Errorf("unknown profile command %q (list, save, delete)", sub)
}

func (a *app) login(rest []string) error {
	if len(rest) != 1 {
		return fmt.Errorf("login needs exactly one server name")
	}
	name := rest[0]
	s, ok := a.cat.Find(name)
	if !ok {
		return fmt.Errorf("no server named %q in the catalog", name)
	}
	e, err := spec.Expand(s.Spec, a.opt.uid)
	if err != nil {
		return err
	}
	m, _ := e.(map[string]any)
	v := spec.ViewOf(m)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tok, err := oauth.Login(ctx, name, v, oauth.PrintURL)
	if err != nil {
		return err
	}
	store := oauth.LoadStore()
	store.Tokens[oauth.Key(name, v.URL)] = tok
	if err := store.Save(); err != nil {
		return err
	}
	when := "no expiry reported"
	if !tok.ExpiresAt.IsZero() {
		when = "expires " + tok.ExpiresAt.Local().Format(time.RFC1123)
	}
	fmt.Fprintf(a.out, "authorized %s (%s)\n", name, when)
	return nil
}

func (a *app) logout(rest []string) error {
	if len(rest) != 1 {
		return fmt.Errorf("logout needs exactly one server name")
	}
	name := rest[0]
	store := oauth.LoadStore()
	n := 0
	for key := range store.Tokens {
		if strings.HasPrefix(key, name+"@") {
			delete(store.Tokens, key)
			n++
		}
	}
	if n == 0 {
		fmt.Fprintf(a.out, "no stored token for %s\n", name)
		return nil
	}
	if err := store.Save(); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "forgot %d token(s) for %s\n", n, name)
	return nil
}

// restore puts back every project file a session left rewritten, including
// ones claimed from another host: this is an explicit request, and the user
// is the one who knows that session is gone.
func (a *app) restore() error {
	notes := target.RecoverStale(fsutil.StateDir(), true)
	if len(notes) == 0 {
		fmt.Fprintln(a.out, "nothing to restore")
	}
	for _, n := range notes {
		fmt.Fprintln(a.out, n)
	}
	return nil
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// withAuth records where each measurement's credentials came from, and turns
// a 401 caused by an expired Claude Code token into a message that says so.
func withAuth(results []mcp.Result, notes map[string]string) []mcp.Result {
	for i := range results {
		r := &results[i]
		r.Auth = notes[r.Name]
		if r.Auth == oauth.AuthClaudeExpired && !r.OK {
			r.Err = oauth.ExplainExpired(r.Err)
		}
	}
	return results
}
