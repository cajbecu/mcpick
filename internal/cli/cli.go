// Package cli is mcpick's command line: argument parsing and one function per
// command, wired to the internal packages that do the work.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/cajbecu/mcpick/internal/backend"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/config"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/oauth"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/proxy"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/trust"
	"github.com/cajbecu/mcpick/internal/tui"
)

const usage = `mcpick — choose which MCP servers a session loads, at the moment you launch it.

  mcpick [opts] run <cmd> [args...]   pick, render a config, launch cmd
  mcpick [opts] list                  print the merged catalog, no picker
  mcpick [opts] doctor                connect to the selected servers and report
  mcpick [opts] measure               refresh the context-cost estimates
  mcpick [opts] export                print the selection in an agent's dialect
  mcpick [opts] serve                 run as one MCP server fronting the selection
  mcpick [opts] import                seed the catalog from ~/.claude.json
  mcpick [opts] move <server> <group> move a server to project, local or user
  mcpick [opts] profile [list]        list your profiles, the catalog's, and default
  mcpick [opts] profile save <name>   save the selection (or --select) as one of yours
  mcpick [opts] profile delete <name> remove one of yours, or a catalog's profile from
                                      its file (the servers stay)
  mcpick [opts] profile rename <a> <b> rename one of your profiles
  mcpick [opts] login <server>        run the OAuth flow for a remote server
  mcpick [opts] logout <server>       forget a stored token
  mcpick        agents                list the agents mcpick knows how to launch
  mcpick        restore               undo project files left behind by a crash

Options:
  --uid ID         session id; keys the saved selection and replaces {UUID}
                   in the catalog (default: hostname)
  --file PATH      catalog file (default: <root>/.mcp.yaml or <root>/.mcp.json)
  --agent NAME     agent to render for (default: from the command name)
  --profile NAME   use a named profile (yours, then the catalog's; default =
                   --all), skip the picker
  --select A,B     use exactly these servers, skip the picker
  --home DIR       where mcpick keeps its files (default: $MCPICK_HOME, else ~/.mcpick)
  --addr HOST:PORT serve over HTTP on a loopback address instead of stdio
  --timeout DUR    per-server connect timeout (default: 15s)
  --redact         move: rewrite secrets as ${VAR} references (import does
                   so by default); not with --yes
  --yes            import: copy credentials as they are; move: write a
                   server's credentials into the catalog as they are (asked
                   on a terminal; refused without one)
  --trust          measure, doctor: approve the commands of stdio servers not
                   yet trusted (what the catalog says, without a prompt; each
                   is printed before it runs) and remember them for this project
  --trust-catalog  measure, doctor: trust this project's catalog, so its remote
                   servers are measured unselected, environment and private
                   hosts included (what it trusts is printed); lapses when a
                   catalog file changes; commands still need --trust
  --json           machine-readable output for list, doctor, measure, agents,
                   profile list
  -y, --last       skip the picker, reuse the saved selection (warns when none
                   was saved)
  --all            select every server (never a hidden one), skip the picker
  --none           select nothing, skip the picker
  -h, --help       this text
  -V, --version    print version

Options go before the command; everything after run belongs to the agent.

Examples:
  mcpick run claude                         pick in the picker, then launch
  mcpick -y run codex --full-auto           reuse the last selection
  mcpick --profile review run claude        launch with a saved profile
  mcpick --select github,sentry run gemini  launch with exactly these servers
  mcpick --select github profile save gh    save a profile of yours
  mcpick measure                            see what each server costs

<root> is the nearest ancestor of the current directory holding a catalog file
or a .git, else the current directory.

Servers are merged from these sources, first match wins, named as Claude
Code names its scopes:
  1. <root>/.mcp.yaml, <root>/.mcp.json          project
  2. ~/.claude.json projects[<root>].mcpServers  local
  3. ~/.claude.json mcpServers                   user
  4. enabled Claude Code plugins                 plugin (read-only)

Everything mcpick writes lives under one directory, ~/.mcpick by default:
  config.yaml    your defaults for the picker (max_rows); never written by mcpick
  profiles.yaml  your profiles: named selections, valid in every project
  selections/  what you picked, per workspace and --uid (names, origins and
               spec hashes; never a value)
  state/       OAuth tokens, measured context costs, trusted commands and
               catalogs, restore records, and hidden/ — the servers hidden
               with h, per workspace
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
	uid string
	// uidGiven says --uid was on the command line, so the picker's header
	// shows it; the hostname it defaults to is not worth a column.
	uidGiven bool
	file     string
	// agent is --agent: the agent to render for, when the command name does
	// not say. --target, its name in 0.1.0, is accepted as an alias.
	agent   string
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
	yes     bool
	trust   bool
	// trustCatalog is --trust-catalog: trust the project's own catalog for
	// measuring (see internal/trust).
	trustCatalog bool
	// late are mcpick's own flags found after `run <cmd>`, where they
	// belong to the agent: passed through as they are, and said (lateFlagNote).
	late []string
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
	// consent is set by selection: the servers whose selection is a check
	// as trust.Gate means it — checked in the picker, named with --select,
	// listed by a personal profile, or saved by an earlier launch and still
	// what was checked (trust.Confirm). --all and a catalog profile select
	// without consenting: they name no server, and a repository can put
	// anything under either, so a remote they select is measured only when
	// harmless, as an unchecked one is (see internal/trust). Launching is
	// unchanged: the selection is the consent to launch.
	consent map[string]bool
	// cfg is ~/.mcpick/config.yaml, the user's defaults for the picker.
	cfg config.Config
	// prof is the user's own profiles, loaded on first use; see profiles().
	prof *profile.Set
}

// warnf prints one line on stderr. It is the one way out for warnings,
// and what they say often comes from the catalog, a server or a state
// file — a name, a URL, an error body — so the line is made safe to print
// here (trust.Safe): an escape sequence in it is spelled out, never sent
// to the terminal.
func (a *app) warnf(format string, args ...any) {
	fmt.Fprintln(a.errw, "mcpick: "+trust.Safe(fmt.Sprintf(format, args...)))
}

// Main runs mcpick with args (without the program name) and returns the exit
// status.
func Main(args []string, linkedVersion string) int {
	a := &app{out: os.Stdout, errw: os.Stderr, version: Version(linkedVersion)}
	mcp.ClientVersion = a.version
	return a.exit(a.run(args))
}

// exit turns run's outcome into the exit status and, for an error of
// mcpick's own, the one line on stderr that says it. The error may quote the
// catalog or a server — a URL, a reason from `${VAR:?reason}`, a body — so
// it is made safe to print (trust.Safe) like every other line.
func (a *app) exit(err error) int {
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
	fmt.Fprintln(a.errw, "mcpick:", trust.Safe(err.Error()))
	return 1
}

func (a *app) run(args []string) error {
	opt, cmd, rest, err := parseArgs(args)
	if err != nil {
		return err
	}
	a.opt = opt
	fsutil.SetMCPickHome(opt.home) // "" falls back to $MCPICK_HOME, then ~/.mcpick
	for _, f := range opt.late {
		a.warnf("%s", lateFlagNote(f, rest[0]))
	}
	// An agent name is checked on every command, not only the ones that
	// render for it: a typo in `--agent` should be an error where it was
	// typed, not a silent no-op on list and a surprise on the next run.
	if opt.agent != "" {
		if _, err := backend.Pick(opt.agent, nil); err != nil {
			return err
		}
	}

	switch cmd {
	case "help":
		fmt.Fprintln(a.out, usage)
		return nil
	case "version":
		fmt.Fprintln(a.out, "mcpick", a.version)
		return nil
	case "agents", "targets": // targets is the 0.1.0 name
		return a.agents() // writes nothing, reads nothing of the user's
	case "restore":
		// restore reads records under the home and writes the user's project
		// files from them: the home has to be the user's own, as below.
		if err := fsutil.EnsureHome(); err != nil {
			return err
		}
		return a.restore()
	}

	// Created private before anything lands in it: tokens go in state/, and a
	// home that first appeared through a 0755 MkdirAll would stay world-listable.
	if err := fsutil.EnsureHome(); err != nil {
		return err
	}
	// A bad preference is a warning, never a reason not to launch.
	var warns []string
	a.cfg, warns = config.Load()
	for _, w := range warns {
		a.warnf("warning: %s", w)
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
	// The built-in profile is --all. Decided only once the catalog is known:
	// a catalog that defines its own "default" from before the name was
	// reserved keeps --profile default from quietly meaning more than it
	// lists (profileSelection refuses it with the way out).
	if a.opt.profile == profile.Default && !a.catalogDefault() {
		a.opt.profile, a.opt.all = "", true
	}

	switch cmd {
	case "list":
		return a.list()
	case "import":
		return a.importClaude()
	case "move":
		return a.move(rest)
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
	case "__complete":
		return a.complete(rest)
	}
	return fmt.Errorf("unknown command %q (try --help)", cmd)
}

// commands are the words parseArgs takes for a command. __complete is the
// shell completions' own and is not in the usage.
var commands = map[string]bool{
	"run": true, "list": true, "import": true, "export": true, "doctor": true,
	"measure": true, "serve": true, "agents": true, "targets": true, "restore": true,
	"login": true, "logout": true, "profile": true, "move": true, "__complete": true,
}

// complete is `mcpick __complete servers`, what the shell completions call:
// the catalog's server names, one per line, nothing else. A name with a
// character a shell or a completion system gives a meaning to — a space, a
// quote, `$`, `:`, `[`, a comma, a control — is left out rather than
// quoted three ways; the user types it.
func (a *app) complete(rest []string) error {
	if len(rest) != 1 || rest[0] != "servers" {
		return fmt.Errorf("usage: mcpick __complete servers")
	}
	for _, s := range a.cat.Servers {
		if completable(s.Name) {
			fmt.Fprintln(a.out, s.Name)
		}
	}
	return nil
}

// completable says whether a server name can go to a shell as it is:
// letters, digits and . _ - @ / +, not starting with -.
func completable(name string) bool {
	if name == "" || name[0] == '-' {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-@/+", r):
		default:
			return false
		}
	}
	return true
}

// ownFlags are mcpick's flags that are unmistakably its own. Found after
// `run <cmd>` they go to the agent, and the user is told (lateFlags).
// `-y`, `--yes` and `--json` are left out: many agents take them.
var ownFlags = []string{
	"--uid", "--file", "--agent", "--target", "--profile", "--select", "--addr", "--home",
	"--timeout", "--redact", "--trust", "--trust-catalog", "--all", "--none", "--last",
}

// ownValueFlags are the ownFlags that take a value.
var ownValueFlags = []string{
	"--uid", "--file", "--agent", "--target", "--profile", "--select", "--addr", "--home", "--timeout",
}

// lateFlags lists mcpick's own flags among the agent's arguments, once
// each, in order. It stops at `--`, after which nothing is a flag; skips
// the value after a flag that takes one, mcpick's or the agent's; and
// leaves alone the agent's own flags of the same name (Meta.ValueFlags:
// claude's --agent, codex's --profile).
func lateFlags(rest, agentFlags []string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			break
		}
		key, _, hasValue := strings.Cut(a, "=")
		switch {
		case slices.Contains(agentFlags, key):
		case slices.Contains(ownFlags, key):
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
			if !slices.Contains(ownValueFlags, key) {
				continue
			}
		default:
			continue
		}
		if !hasValue {
			i++ // the flag's value, whatever it looks like
		}
	}
	return out
}

// lateFlagNote is the warning for one of mcpick's flags found after the
// agent's command: it went to the agent, as everything after run does.
func lateFlagNote(flag, cmd string) string {
	name := strings.TrimSuffix(filepath.Base(cmd), ".exe")
	return fmt.Sprintf("%s after %q goes to %s; mcpick options go before run", flag, name, name)
}

// errExclusive is the refusal of two ways of saying what to select at once.
var errExclusive = errors.New("--all, --none, --select, --profile and -y are exclusive; use one of them")

// errRedactYes is the refusal of both answers to the credentials question
// at once: which one was meant cannot be told.
var errRedactYes = errors.New("--redact and --yes contradict each other: --redact writes ${VAR} references, --yes the credentials as they are; use one of them")

// parseArgs accepts flags on either side of the command, because
// `mcpick import --redact` is how people type it. The exception is `run`, a
// hard separator: everything after it is the agent's own command line, so
// mcpick must not interpret a `--all` meant for someone else; one of its
// own flags found there is passed through and reported (options.late).
func parseArgs(args []string) (options, string, []string, error) {
	opt := options{timeout: 15 * time.Second}
	var cmd string
	var positional []string

	strFlags := map[string]*string{
		"--uid": &opt.uid, "--file": &opt.file, "--agent": &opt.agent, "--target": &opt.agent,
		"--profile": &opt.profile, "--select": &opt.selects, "--addr": &opt.addr,
		"--home": &opt.home,
	}
	// selectors counts the ways the selection was named; more than one is
	// refused rather than resolved by precedence, since the user meant one.
	selectors := func() int {
		n := 0
		for _, set := range []bool{opt.selects != "", opt.profile != "", opt.all, opt.none, opt.last} {
			if set {
				n++
			}
		}
		return n
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
		case "--yes":
			opt.yes = true
			continue
		case "--trust":
			opt.trust = true
			continue
		case "--trust-catalog":
			opt.trustCatalog = true
			continue
		}

		if !strings.HasPrefix(a, "-") {
			switch {
			case cmd == "" && commands[a]:
				cmd = a
				if a == "run" {
					rest := args[i+1:]
					// run takes the agent's command first; a flag there is
					// one of mcpick's typed after run, not a command to look up.
					if len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
						return opt, "", nil, fmt.Errorf("%s after run: mcpick options go before run, the agent's after its command (mcpick [options] run <command> [its arguments])", trust.Safe(rest[0]))
					}
					if len(rest) > 1 {
						var agentFlags []string
						if b, err := backend.Pick(opt.agent, rest); err == nil {
							agentFlags = b.Info().ValueFlags
						}
						opt.late = lateFlags(rest[1:], agentFlags)
					}
					if selectors() > 1 {
						return opt, "", nil, errExclusive
					}
					return opt, cmd, rest, nil
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
			if key == "--uid" {
				opt.uidGiven = true
			}
			continue
		}
		if key == "--timeout" {
			v, err := value()
			if err != nil {
				return opt, "", nil, err
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				if _, bare := strconv.Atoi(v); bare == nil {
					return opt, "", nil, fmt.Errorf("--timeout %s needs a unit, e.g. %ss", v, v)
				}
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
	if selectors() > 1 {
		return opt, "", nil, errExclusive
	}
	if opt.redact && opt.yes && (cmd == "import" || cmd == "move") {
		return opt, "", nil, errRedactYes
	}
	return opt, cmd, positional, nil
}

// --- selection --------------------------------------------------------------

// selection resolves what the session will run with, in order: an explicit
// --select, a --profile, --all/--none, the picker (only when launching — launch
// is nil otherwise — and only on a terminal), the saved selection. It also
// sets a.consent (see app).
func (a *app) selection(launch *tui.Options) (map[string]bool, error) {
	st, err := state.Load(a.root, a.opt.uid)
	if err != nil {
		return nil, err
	}
	// A saved check covers a repository server only while it is the server
	// that was checked: same origin, same spec. The rest of the saved
	// selection stands; the unconfirmed ones are said, on the row in the
	// picker and on stderr otherwise.
	sel, unconfirmed := trust.Confirm(a.cat.Servers, st.Selected, st.Checks)
	a.consent = map[string]bool{}

	switch {
	case a.opt.selects != "":
		sel, err := a.parseSelect(a.opt.selects)
		a.consent = sel
		return sel, err
	case a.opt.profile != "":
		sel, personal, err := a.profileSelection(a.opt.profile)
		if personal {
			a.consent = sel
		}
		return sel, err
	case a.opt.all:
		// "All" never includes a server disabled in Claude Code, as in the
		// picker: turning one back on is an explicit, per-server choice. An
		// agent that does not read Claude's settings has nothing to turn on.
		// Nor a server hidden in the picker (h): hidden means not wanted
		// in this project, and "all" is the one key that would bring every
		// one of them back at once. Built from nothing, not from the saved
		// selection: a server saved before it was hidden or disabled would
		// otherwise ride along.
		names, err := a.defaultServers(launch == nil || launch.Target.Info().ClaudeSettings)
		if err != nil {
			return nil, err
		}
		all := map[string]bool{}
		for _, n := range names {
			all[n] = true
		}
		return all, nil
	case a.opt.none:
		return map[string]bool{}, nil
	case launch != nil && !a.opt.last && interactive():
		launch.Unconfirmed = unconfirmed
		// No selection saved for this project and uid: the picker starts
		// from what the agent would load without mcpick (tui.Options).
		launch.FirstRun = st.Updated == ""
		res, err := tui.Run(a.cat, sel, *launch)
		if err != nil {
			return nil, err
		}
		a.reenable = res.Reenable
		a.consent = res.Selected
		return res.Selected, nil
	}
	for _, u := range unconfirmed {
		a.warnf("warning: %s %s; not selected until it is checked again (in the picker, or --select %s)",
			trust.Safe(u.Name), u.Why, trust.Safe(u.Name))
	}
	a.consent = sel
	// The saved selection is what is left. Reused on purpose (-y) or because
	// there is no terminal to pick in, an empty one launches the agent with
	// no servers at all, which on a fresh box or a new --uid is rarely what
	// was meant; it is said, not refused, since an empty launch is valid.
	if len(sel) == 0 && (a.opt.last || launch != nil) {
		if st.Updated == "" {
			a.warnf("warning: no saved selection for uid %q in %s; nothing is selected (pick once in the picker, or use --select, --profile or --all)",
				a.opt.uid, fsutil.ShortenHome(a.root))
		} else {
			a.warnf("warning: the saved selection for uid %q in %s is empty; nothing is selected", a.opt.uid, fsutil.ShortenHome(a.root))
		}
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
			return nil, fmt.Errorf("no server named %q in the catalog%s", n, didYouMean(n, a.serverNames(), 3))
		}
		sel[n] = true
	}
	return sel, nil
}

// serverNames lists the catalog's names, for suggestions.
func (a *app) serverNames() []string {
	out := make([]string, 0, len(a.cat.Servers))
	for _, s := range a.cat.Servers {
		out = append(out, s.Name)
	}
	return out
}

// interactive reports whether a picker can be drawn: bubbletea reads stdin and
// paints stdout, and a redirected stdout would get a screenful of escapes.
func interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// saveSelection records the selection with, for each server, what was
// checked (trust.Checks), so the next launch can tell a check from a name.
func (a *app) saveSelection(sel map[string]bool, targetName string) error {
	st := state.State{Target: targetName, Checks: trust.Checks(a.cat.Servers, sel)}
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
	refreshed, err := oauth.Attach(r)
	for _, n := range refreshed {
		a.warnf("refreshed the OAuth token for %s", n)
	}
	if err != nil {
		a.warnf("%v", err)
	}
	return r, nil
}

// --- commands ---------------------------------------------------------------

func (a *app) launch(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("run needs a command")
	}
	b, err := backend.Pick(a.opt.agent, argv)
	if err != nil {
		return err
	}
	name := b.Info().Name
	// The command is looked up before anything is picked or written: a
	// typo is answered with the agent it looks like, not with "no adapter"
	// after a screenful of picking.
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("%s: command not found%s", trust.Safe(argv[0]),
			didYouMean(strings.TrimSuffix(filepath.Base(argv[0]), ".exe"), backend.Names(), 1))
	}

	sel, err := a.selection(&tui.Options{UID: a.opt.uid, UIDGiven: a.opt.uidGiven, Version: a.version, Target: b, Argv: argv,
		Root: a.root, Config: a.cfg, Profiles: a.profiles()})
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
	if pr, ok := b.(backend.Preparer); ok {
		notes, err := pr.Prepare(backend.Prep{Catalog: a.cat, Reenable: a.reenable}, &resolved)
		for _, n := range notes {
			a.warnf("%s", n)
		}
		if err != nil {
			return err
		}
	}

	stateDir := fsutil.StateDir()
	for _, n := range backend.RecoverStale(stateDir, false) {
		a.warnf("%s", n)
	}
	rt, err := fsutil.RuntimeDir()
	if err != nil {
		return err
	}
	fsutil.PruneRuntime(rt, 24*time.Hour)

	plan, err := b.Plan(backend.Ctx{
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

// agents is `mcpick agents` (`targets` in 0.1.0): the agents mcpick can
// launch, how each is reached and what else it reads.
func (a *app) agents() error {
	all := backend.All()
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
				paths = append(paths, backend.DisplayPath(s.Path))
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
			r := row{Name: s.Name, Origin: s.Origin, Endpoint: spec.MaskText(s.Endpoint()), Source: s.Source, Disabled: s.Disabled}
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
	// Names, endpoints and profile names are the catalog's text and go
	// through trust.Safe on their way out; the JSON above is encoded, which
	// is its own escaping.
	w := 0
	for _, s := range a.cat.Servers {
		w = max(w, len(trust.Safe(s.Name)))
	}
	for _, s := range a.cat.Servers {
		cost := ""
		if m, ok := cache.Get(s.Name, s.Spec); ok {
			if m.OK {
				cost = mcp.HumanTokens(m.Tokens)
			} else {
				cost = m.ErrorKind()
			}
		}
		flag := ""
		if s.Disabled {
			flag = " (disabled in Claude Code)"
		}
		fmt.Fprintf(a.out, "%-*s  %-9s  %7s  %s%s\n", w, trust.Safe(s.Name), s.Origin, cost, trust.Safe(spec.MaskText(s.Endpoint())), flag)
	}
	fmt.Fprintln(a.out)
	for _, r := range a.profileRows() {
		fmt.Fprintf(a.out, "profile %-12s %-9s %s\n", trust.Safe(r.Name), r.Source, trust.Safe(describeServers(r)))
	}
	return nil
}

// importClaude copies the local and user servers of ~/.claude.json into
// the catalog. Credentials written in a spec are rewritten as
// ${VAR:?export VAR} references (spec.Redact) unless --yes asks for the
// values as they are: the catalog usually sits in git. --redact, the old
// way to ask for the default, is accepted and changes nothing.
func (a *app) importClaude() error {
	n := 0
	var vars []string
	for _, s := range a.cat.Servers {
		if s.Origin != catalog.OriginLocal && s.Origin != catalog.OriginUser {
			continue
		}
		sp, found := spec.Redact(s.Name, s.Spec)
		vars = append(vars, found...)
		if a.opt.yes {
			sp = s.Spec
		}
		if err := catalog.AddServer(a.opt.file, s.Name, sp); err != nil {
			return err
		}
		n++
	}
	fmt.Fprintf(a.out, "imported %d server(s) into %s\n", n, fsutil.ShortenHome(a.opt.file))
	if n > 0 {
		// The copies in ~/.claude.json are now shadowed by the catalog's,
		// which is silent while they are the same server; the way to one
		// definition is said here, whichever tool the user prefers.
		fmt.Fprintf(a.out, "they are still in %s, which the catalog now shadows; remove them there with `claude mcp remove <name>`, or next time `mcpick move <name> project` moves one instead of copying it\n",
			fsutil.ShortenHome(a.cat.ClaudePath))
	}
	if len(vars) == 0 {
		return nil
	}
	sort.Strings(vars)
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		name, _, _ := strings.Cut(v, "=")
		names = append(names, trust.Safe(name))
	}
	if a.opt.yes {
		a.warnf("warning: credentials were copied as they are into %s (--yes); keep the file out of git, or re-run without --yes to write ${VAR} references (%s)",
			fsutil.ShortenHome(a.opt.file), strings.Join(names, ", "))
		return nil
	}
	fmt.Fprintln(a.out, "\nexport these before launching:")
	for _, name := range names {
		fmt.Fprintf(a.out, "  export %s=...\n", name)
	}
	a.warnf("the values were left out of the catalog on purpose; they are still in ~/.claude.json")
	return nil
}

func (a *app) export() error {
	want := a.opt.agent
	if want == "" {
		want = "claude"
	}
	b, err := backend.Pick(want, nil)
	if err != nil {
		return err
	}
	sel, err := a.selection(nil)
	if err != nil {
		return err
	}
	if len(sel) == 0 {
		a.warnf("warning: nothing is selected; the export holds no servers (use --all, --select or --profile)")
	}
	resolved, err := a.resolved(sel)
	if err != nil {
		return err
	}
	data, err := b.Dialect().Emit(resolved)
	if err != nil {
		return err
	}
	// The output is the rendered config: what the catalog wrote as
	// `${TOKEN}` is the token here. Said once on stderr, since stdout is
	// about to be redirected into a file.
	for _, n := range resolved.Names {
		if _, found := spec.Redact(n, resolved.Specs[n]); len(found) > 0 {
			a.warnf("warning: export wrote expanded credentials to stdout; keep it out of files you commit")
			break
		}
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
	sel, err := a.selection(nil)
	if err != nil {
		return err
	}
	// Doctor sees selected servers only, so of the two measuring rules only
	// trust applies: a stdio server whose command is not trusted is reported
	// as skipped, not run. The gate reads the catalog as written and comes
	// before expansion, so an untrusted entry is a SKIP whatever its
	// ${VAR:?} would have said, and no credential is looked up for it.
	var servers []catalog.Server
	for _, s := range a.cat.Servers {
		if sel[s.Name] {
			servers = append(servers, s)
		}
	}
	if len(servers) == 0 {
		return fmt.Errorf("nothing selected (use --all, --select, --profile, or launch once to save a selection)")
	}
	g := a.gate(servers, a.consent)
	probe, err := a.resolved(g.run)
	if err != nil {
		return err
	}
	byName := map[string]mcp.Result{}
	for _, r := range g.skipped {
		byName[r.Name] = r
	}
	// The gate looked at the spec as written; a URL that expanded to
	// nothing would run the entry's command instead, unseen.
	kept := spec.Selection{Specs: map[string]map[string]any{}}
	for _, n := range probe.Names {
		if err := trust.Effective(a.cat.SpecOf(n), probe.Specs[n]); err != nil {
			byName[n] = mcp.Result{Name: n, Err: err.Error()}
			continue
		}
		kept.Names = append(kept.Names, n)
		kept.Specs[n] = probe.Specs[n]
	}
	notes := oauth.AttachClaude(kept, a.cat.ClaudeNames())
	probed := withAuth(mcp.ProbeAllWith(context.Background(), kept, a.opt.timeout, 8, a.written(g.opts, kept.Names)), notes)

	cache := mcp.LoadCache()
	for _, r := range probed {
		cache.Put(r.Name, a.cat.SpecOf(r.Name), r)
		byName[r.Name] = r
	}
	if err := cache.Save(); err != nil {
		a.warnf("could not save measurements: %v", err)
	}

	// Back in selection order, skipped rows in their place.
	results := make([]mcp.Result, 0, len(servers))
	for _, s := range servers {
		results = append(results, byName[s.Name])
	}
	bad, skipped := 0, 0
	for _, r := range results {
		switch {
		case r.Skipped:
			skipped++
		case !r.OK:
			bad++
		}
	}
	if a.opt.jsonOut {
		if err := a.printJSON(results); err != nil {
			return err
		}
	} else {
		// The name is the catalog's, the error and the server's name are
		// the server's: each goes through trust.Safe on its way out.
		w := 0
		for _, r := range results {
			w = max(w, len(trust.Safe(r.Name)))
		}
		total := 0
		for _, r := range results {
			name := trust.Safe(r.Name)
			if r.OK {
				total += r.Tokens
				auth := ""
				if r.Auth != "" {
					auth = "  (auth: " + trust.Safe(r.Auth) + ")"
				}
				fmt.Fprintf(a.out, "ok    %-*s  %9s  ~%-6s %4dms  %s%s\n",
					w, name, mcp.Tools(r.Tools), mcp.HumanTokens(r.Tokens), r.Millis, trust.Safe(r.Server.Name), auth)
				continue
			}
			if r.Skipped {
				fmt.Fprintf(a.out, "SKIP  %-*s  %s\n", w, name, trust.Safe(strings.TrimPrefix(r.Err, "skipped: ")))
				continue
			}
			fmt.Fprintf(a.out, "FAIL  %-*s  %s\n", w, name, trust.Safe(r.Err))
			if mcp.NeedsLogin(r.Err) {
				fmt.Fprintf(a.out, "      %-*s  try: mcpick login %s\n", w, "", name)
			}
		}
		note := ""
		if skipped > 0 {
			note = fmt.Sprintf(", %d skipped", skipped)
		}
		fmt.Fprintf(a.out, "\n%d/%d reachable%s, ~%s of context\n", len(results)-bad-skipped, len(results), note, mcp.HumanTokens(total))
	}
	// Doctor's promise is that every selected server was reached; a skipped
	// one was not, and the line above says how to change that.
	if bad > 0 || skipped > 0 {
		return proc.ExitCode(1)
	}
	return nil
}

func (a *app) measure() error {
	// Measuring is about the catalog, not one selection — but a stdio
	// server's command runs only once trusted (--trust), and a remote server
	// this repository defines is contacted unchecked only when its spec
	// exposes nothing (then onto a public address only) or the catalog is
	// trusted (--trust-catalog), because the environment is expanded into
	// its URL and headers. Checked means a.consent, not the selection:
	// --all and a catalog profile are not a check. See internal/trust.
	if _, err := a.selection(nil); err != nil {
		return err
	}
	servers := a.cat.Servers
	if a.opt.trust {
		// --trust approves commands in bulk, without a prompt; a hidden
		// server is one the user does not want in this project, and its
		// command must not be approved and run on the strength of a flag
		// meant for the visible ones. They are left out whole, as the
		// picker's M leaves them out, and named.
		hidden, err := state.LoadHidden(a.root)
		if err != nil {
			return err
		}
		var kept []catalog.Server
		var left []string
		for _, s := range servers {
			if hidden[s.Name] {
				left = append(left, trust.Safe(s.Name))
				continue
			}
			kept = append(kept, s)
		}
		if len(left) > 0 {
			a.warnf("--trust leaves hidden servers out: %s (unhide them in the picker to measure them)", strings.Join(left, ", "))
		}
		servers = kept
	}
	g := a.gate(servers, a.consent)
	all := spec.Selection{Specs: map[string]map[string]any{}}
	var unresolved []mcp.Result
	for _, s := range servers {
		if !g.run[s.Name] {
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
		// The gate looked at the spec as written; a URL that expanded to
		// nothing would run the entry's command instead, unseen.
		if err := trust.Effective(s.Spec, m); err != nil {
			unresolved = append(unresolved, mcp.Result{Name: s.Name, Err: err.Error()})
			continue
		}
		all.Names = append(all.Names, s.Name)
		all.Specs[s.Name] = m
	}
	if _, err := oauth.Attach(all); err != nil {
		a.warnf("%v", err)
	}
	notes := oauth.AttachClaude(all, a.cat.ClaudeNames())
	results := append(withAuth(mcp.ProbeAllWith(context.Background(), all, a.opt.timeout, 8, a.written(g.opts, all.Names)), notes), unresolved...)

	cache := mcp.LoadCache()
	for _, r := range results {
		cache.Put(r.Name, a.cat.SpecOf(r.Name), r)
	}
	if err := cache.Save(); err != nil {
		return err
	}
	g.warn(a)
	if a.opt.jsonOut {
		return a.printJSON(append(results, g.skipped...))
	}
	mcp.SortByCost(results)
	for _, r := range results {
		if !r.OK {
			fmt.Fprintf(a.out, "%-24s  %s\n", trust.Safe(r.Name), trust.Safe(r.Err))
			continue
		}
		fmt.Fprintf(a.out, "%-24s  %9s  ~%s\n", trust.Safe(r.Name), mcp.Tools(r.Tools), mcp.HumanTokens(r.Tokens))
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
	if err := oauth.Put(oauth.Key(name, v.URL), tok); err != nil {
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
	n, err := oauth.Forget(name)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no stored token for %s", name)
	}
	fmt.Fprintf(a.out, "forgot %d token(s) for %s\n", n, name)
	return nil
}

// restore puts back every project file a session left rewritten, including
// ones claimed from another host: this is an explicit request, and the user
// is the one who knows that session is gone.
func (a *app) restore() error {
	notes := backend.RecoverStale(fsutil.StateDir(), true)
	if len(notes) == 0 {
		fmt.Fprintln(a.out, "nothing to restore")
	}
	for _, n := range notes {
		fmt.Fprintln(a.out, n)
	}
	return nil
}

// printJSON is every --json output: indented, with the characters a
// terminal would act on escaped (trust.SafeJSON).
func (a *app) printJSON(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := a.out.Write(trust.SafeJSON(buf.Bytes()))
	return err
}

// written gives each server's connection Options the spec and URL as the
// catalog wrote them, so an error names `?key=${K}` rather than the
// expanded value, and has any expanded value echoed back in it replaced by
// its reference (mcp.Options.Written, Raw).
func (a *app) written(opts map[string]mcp.Options, names []string) map[string]mcp.Options {
	out := make(map[string]mcp.Options, len(names))
	for _, n := range names {
		o := opts[n]
		raw := a.cat.SpecOf(n)
		o.Written, o.Raw = spec.ViewOf(raw).URL, raw
		out[n] = o
	}
	return out
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
