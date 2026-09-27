// e2e-runner drives mcpick end to end inside the mcpick-e2e container. It
// writes a catalog of five fake MCP servers, picks three of them in the real
// picker under tmux (level a), then launches every agent mcpick supports with
// that saved selection and checks — from the agent's own listing, or from
// what the fake servers logged — that it loaded exactly those three (level
// b). No model is called and no credential exists in the container: every
// agent stops at its login prompt, after it has read its MCP configuration.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	home    = "/e2e/mcpick" // --home: mcpick's files, away from anything real
	project = "/e2e/project"
	logDir  = "/e2e/logs"
	sock    = "mcpick-e2e" // tmux -L: a server of our own

	probeTimeout = 3 * time.Minute
)

// A fake server: stdio ones are started by the agent from the catalog entry,
// HTTP ones by the runner, once, on a loopback port.
type fake struct {
	name string
	addr string // empty for stdio
}

var fakes = []fake{
	{"srv-alpha", ""},
	{"srv-bravo", "127.0.0.1:18081"},
	{"srv-charlie", ""},
	{"srv-delta", "127.0.0.1:18082"},
	{"srv-echo", ""},
}

// chosen is what the picker run selects: rows 1, 3 and 4 — two stdio, one
// HTTP — so both transports and both kinds of "not chosen" are covered.
var chosen = []string{"srv-alpha", "srv-charlie", "srv-delta"}

func unchosen() []string {
	var out []string
	for _, f := range fakes {
		if !contains(chosen, f.name) {
			out = append(out, f.name)
		}
	}
	return out
}

type proof int

const (
	// byListing: the agent's own non-LLM listing names the chosen servers
	// and none of the others.
	byListing proof = 1 << iota
	// byConnect: the fake servers' logs show the agent sent initialize to
	// the chosen ones and to none of the others.
	byConnect
)

type agent struct {
	name  string   // as mcpick names it
	cmd   string   // the executable, when it differs from the name
	probe []string // argv after `run <cmd>` that makes the agent read its MCP config without a model
	proof proof
	// dirs are directories mcpick creates in the project for this agent
	// (the project-file mechanism); they must be gone when the run ends.
	dirs []string
	// gap names a known gap in mcpick: level (b) is expected to fail for
	// this reason, and passing would mean the note is stale. gapSays is
	// what the probe prints when it fails that way; a failure that does not
	// say it, exits non-zero, runs late or leaves files behind is a new one.
	gap, gapSays string
	// unsupported says the agent cannot take a selection at all, and why:
	// mcpick launches it unchanged and says so. Level (b) then checks that
	// launch — the probe exits 0, mcpick's notice is on stderr, nothing is
	// left in the project — and records "not supported", never a pass or
	// a FAIL on the listing.
	unsupported string
}

var agents = []agent{
	// `claude mcp list` ignores --mcp-config (anthropics/claude-code#15388);
	// a headless prompt connects to every configured server before it
	// checks for a login, which is the proof.
	{name: "claude", probe: []string{"-p", "hi"}, proof: byConnect},
	{name: "codex", probe: []string{"mcp", "list"}, proof: byListing},
	// gemini's listing health-checks the servers, once the folder is trusted.
	{name: "gemini", probe: []string{"mcp", "list"}, proof: byListing | byConnect, dirs: []string{".gemini"}},
	{name: "copilot", probe: []string{"mcp", "list"}, proof: byListing},
	{name: "opencode", probe: []string{"mcp", "list"}, proof: byListing | byConnect},
	// pi's MCP support is the pi-mcp-adapter extension; it has no listing
	// outside a session, but connects before the model is needed.
	{name: "pi", probe: []string{"-p", "--no-session", "hi"}, proof: byConnect},
	// muse's `mcp` subcommand only logs in and out; its echo provider runs a
	// headless turn without any model or key.
	{name: "muse", probe: []string{"exec", "--provider", "echo", "hi"}, proof: byConnect},
	{name: "grok", probe: []string{"mcp", "list"}, proof: byListing, dirs: []string{".grok"}},
	{name: "devin", probe: []string{"mcp", "list"}, proof: byListing, dirs: []string{".devin"}},
	// The antigravity CLI reads ~/.gemini/config/mcp_config.json and
	// plugins only; .agents/mcp_config.json is the IDE's workspace file.
	// mcpick launches agy unchanged and says so; .agents must not appear.
	{name: "antigravity", cmd: "agy", probe: []string{"mcp", "list"}, dirs: []string{".agents"},
		unsupported: "the agy CLI reads ~/.gemini/config/mcp_config.json and plugins only"},
}

type result struct {
	agent, version, level, verdict, detail string
}

var verbose bool

func main() {
	only := flag.String("only", "", "run these agents only (comma-separated)")
	flag.BoolVar(&verbose, "v", false, "print picker frames and agent output")
	flag.Parse()

	selected, err := selectAgents(*only, agents)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(2)
	}
	if err := setup(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: setup:", err)
		os.Exit(2)
	}
	defer tmux("kill-server") //nolint:errcheck // best effort

	var results []result
	failed := 0
	for _, a := range selected {
		v := version(a)
		ra, saved := levelA(a)
		rb := levelB(a, saved)
		for _, r := range []result{ra, rb} {
			r.agent, r.version = a.name, v
			if a.cmd != "" {
				r.agent += " (" + a.cmd + ")"
			}
			if r.verdict == "FAIL" {
				failed++
			}
			results = append(results, r)
			fmt.Fprintf(os.Stderr, "e2e: %-18s %s %-4s %s\n", r.agent, r.level, r.verdict, firstLine(r.detail))
		}
	}
	printTable(results)
	if failed > 0 {
		fmt.Printf("\n%d check(s) FAILED\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}

// selectAgents resolves -only. Every name must be an agent, as mcpick names
// it or by its executable, and a filter that was given must select at least
// one: a typo that silently ran nothing would look like a passing run.
func selectAgents(only string, all []agent) ([]agent, error) {
	if only == "" {
		return all, nil
	}
	var unknown []string
	want := map[string]bool{}
	for _, n := range strings.Split(only, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		known := false
		for _, a := range all {
			if a.name == n || a.command() == n {
				want[a.name], known = true, true
			}
		}
		if !known {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		var names []string
		for _, a := range all {
			names = append(names, a.name)
		}
		return nil, fmt.Errorf("-only: unknown agent(s) %s; known: %s",
			strings.Join(unknown, ", "), strings.Join(names, ", "))
	}
	if len(want) == 0 {
		return nil, errors.New("-only: no agent named")
	}
	var out []agent
	for _, a := range all {
		if want[a.name] {
			out = append(out, a)
		}
	}
	return out, nil
}

// --- setup ------------------------------------------------------------------

func setup() error {
	for _, d := range []string{home, project, logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(project, ".mcp.yaml"), []byte(catalog()), 0o644); err != nil {
		return err
	}
	// Some agents look for a repository root; grok reads .grok/config.toml
	// from the current directory up to it.
	if _, err := os.Stat(filepath.Join(project, ".git")); err != nil {
		if out, err := command(project, "git", "init", "-q", ".").CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %v: %s", err, out)
		}
	}
	// gemini disables every server in an untrusted folder; the trust file is
	// gemini's own and lives in the container's throwaway home.
	gem := filepath.Join(os.Getenv("HOME"), ".gemini")
	if err := os.MkdirAll(gem, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(gem, "trustedFolders.json"),
		[]byte(fmt.Sprintf("{%q: \"TRUST_FOLDER\"}\n", project)), 0o644); err != nil {
		return err
	}
	for _, f := range fakes {
		if f.addr == "" {
			continue
		}
		cmd := command("/", "fakemcp", "--name", f.name, "--log", logPath(f.name), "--http", f.addr)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting %s: %w", f.name, err)
		}
		if err := waitPort(f.addr, 10*time.Second); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}
	return nil
}

// catalog is the workspace file the picker reads: stdio entries run the
// fake, HTTP entries point at the ones setup started.
func catalog() string {
	var b strings.Builder
	b.WriteString("# generated by e2e-runner\nservers:\n")
	for _, f := range fakes {
		if f.addr != "" {
			fmt.Fprintf(&b, "  %s:\n    type: http\n    url: http://%s/mcp\n", f.name, f.addr)
			continue
		}
		fmt.Fprintf(&b, "  %s:\n    type: stdio\n    command: fakemcp\n    args: [--name, %s, --log, %s]\n",
			f.name, f.name, logPath(f.name))
	}
	return b.String()
}

func logPath(name string) string { return filepath.Join(logDir, name+".log") }

func waitPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(context.Background(), "tcp", addr)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("nothing listening on %s after %s", addr, timeout)
}

func version(a agent) string {
	out, _ := run(project, 30*time.Second, nil, a.command(), "--version")
	line := firstLine(strings.TrimSpace(out.stdout + out.stderr))
	if line == "" {
		return "?"
	}
	return line
}

func (a agent) command() string {
	if a.cmd != "" {
		return a.cmd
	}
	return a.name
}

func (a agent) uid() string { return "e2e-" + a.name }

// --- level (a): the picker -------------------------------------------------

var rowRe = regexp.MustCompile(`\[( |x)\] (srv-[a-z]+)`)

// checked reads the picker frame: which server rows show as selected.
func checked(frame string) map[string]bool {
	out := map[string]bool{}
	for _, m := range rowRe.FindAllStringSubmatch(frame, -1) {
		out[m[2]] = m[1] == "x"
	}
	return out
}

// levelA opens the picker under tmux for `mcpick run <agent> --version`,
// checks that every fake renders with the agent's badge, selects the chosen
// three and launches. The saved selection is what level (b) then reuses with
// -y, so the chain from keypress to agent is real.
func levelA(a agent) (result, bool) {
	r := result{level: "a"}
	sess := "a-" + a.name
	tmux("kill-session", "-t", sess) //nolint:errcheck // may not exist
	// The shell echoes mcpick's exit status when it returns. tmux itself
	// only reports a pane dead once every holder of its tty is gone, and
	// several CLIs leave a background child (an update check) behind for
	// a while after --version; the launch is over when mcpick is.
	launch := fmt.Sprintf("mcpick --home %s --uid %s run %s --version; echo %s=$?", home, a.uid(), a.command(), exitMark)
	if _, err := tmux("new-session", "-d", "-s", sess, "-x", "160", "-y", "45", "-c", project, launch,
		";", "set-option", "-t", sess, "remain-on-exit", "on"); err != nil {
		return fail(r, "tmux: "+err.Error()), false
	}
	defer tmux("kill-session", "-t", sess) //nolint:errcheck // best effort

	frame, err := waitFrame(sess, func(f string) bool { return strings.Contains(f, "srv-echo") }, 20*time.Second)
	if err != nil {
		return fail(r, "the picker did not render: "+err.Error()+"\n"+frame), false
	}
	if verbose {
		fmt.Printf("--- picker frame for %s ---\n%s\n", a.name, strings.TrimRight(frame, "\n"))
	}
	var missing []string
	for _, want := range append([]string{"mcpick", a.name, "Project", "→"}, names()...) {
		if !strings.Contains(frame, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return fail(r, fmt.Sprintf("frame is missing %v:\n%s", missing, frame)), false
	}
	for _, m := range rowRe.FindAllStringSubmatch(frame, -1) {
		if m[1] == "x" {
			return fail(r, "a fresh uid opened with "+m[2]+" already checked:\n"+frame), false
		}
	}

	// n clears whatever was there; the cursor starts on the first row.
	for _, k := range []string{"n", "Space", "j", "j", "Space", "j", "Space"} {
		if _, err := tmux("send-keys", "-t", sess, k); err != nil {
			return fail(r, "send-keys: "+err.Error()), false
		}
		time.Sleep(120 * time.Millisecond)
	}
	frame, err = waitFrame(sess, func(f string) bool { return strings.Contains(f, "3/5 selected") }, 10*time.Second)
	if err != nil {
		return fail(r, "the selection did not reach 3/5:\n"+frame), false
	}
	if got := checked(frame); !sameSet(keysWhere(got, true), chosen) {
		return fail(r, fmt.Sprintf("checked %v, want %v:\n%s", keysWhere(got, true), chosen, frame)), false
	}
	if _, err := tmux("send-keys", "-t", sess, "Enter"); err != nil {
		return fail(r, "send-keys: "+err.Error()), false
	}
	full, err := waitPane(sess, func(f string) bool { return strings.Contains(f, exitMark+"=") }, 2*time.Minute)
	if err != nil {
		return fail(r, "the launch did not finish: "+err.Error()+"\n"+full), false
	}
	status := exitStatus(full)
	if verbose {
		fmt.Printf("--- pane after launch for %s ---\n%s\n", a.name, strings.TrimSpace(full))
	}

	// The selection the picker saved is the one -y will reuse.
	out, err := run(project, 30*time.Second, nil, "mcpick", "--home", home, "--uid", a.uid(), "-y", "export")
	if err != nil {
		return fail(r, "export of the saved selection: "+err.Error()+"\n"+out.stderr), false
	}
	saved := names2(out.stdout)
	if !sameSet(saved, chosen) {
		return fail(r, fmt.Sprintf("saved selection %v, want %v", saved, chosen)), false
	}
	detail := fmt.Sprintf("5 rendered with badge %q; picked %s; `%s --version` exited %d",
		a.name, strings.Join(chosen, ", "), a.command(), status)
	if status != 0 {
		return fail(r, detail+"\n"+full), true
	}
	r.verdict, r.detail = "pass", detail
	return r, true
}

// names2 lists the server names in a Claude-shaped config on stdout.
func names2(config string) []string {
	var out []string
	for _, f := range fakes {
		if strings.Contains(config, `"`+f.name+`"`) {
			out = append(out, f.name)
		}
	}
	return out
}

// tmux runs one tmux command against the runner's own server. None of them
// should take long; a hung tmux is a bug worth a clear timeout.
func tmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-L", sock}, args...)...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// waitFrame polls the visible screen; waitPane the whole scrollback, for
// output that has scrolled past the picker.
func waitFrame(sess string, ok func(string) bool, timeout time.Duration) (string, error) {
	return poll(sess, ok, timeout)
}

func waitPane(sess string, ok func(string) bool, timeout time.Duration) (string, error) {
	return poll(sess, ok, timeout, "-S", "-")
}

func poll(sess string, ok func(string) bool, timeout time.Duration, extra ...string) (string, error) {
	deadline := time.Now().Add(timeout)
	var frame string
	for time.Now().Before(deadline) {
		f, err := tmux(append([]string{"capture-pane", "-p", "-t", sess}, extra...)...)
		if err != nil {
			return frame, err
		}
		frame = f
		if ok(frame) {
			return frame, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return frame, errors.New("timed out after " + timeout.String())
}

// exitMark is what the pane's shell prints, with mcpick's exit status,
// once the launch has returned.
const exitMark = "E2E_EXIT"

var exitRe = regexp.MustCompile(exitMark + `=(\d+)`)

func exitStatus(pane string) int {
	m := exitRe.FindStringSubmatch(pane)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// --- level (b): the agent --------------------------------------------------

// levelB launches the agent's probe through mcpick with the selection level
// (a) saved (or --select when that failed, so the agent is still exercised)
// and judges it by the agent's listing, the fakes' logs, or both. It also
// checks that mcpick left the project as it found it.
func levelB(a agent, saved bool) result {
	r := result{level: "b"}
	if err := truncateLogs(); err != nil {
		return fail(r, err.Error())
	}
	for _, d := range a.dirs {
		os.RemoveAll(filepath.Join(project, d))
	}

	args := []string{"--home", home, "--uid", a.uid()}
	if saved {
		args = append(args, "-y")
	} else {
		args = append(args, "--select", strings.Join(chosen, ","))
	}
	args = append(append(args, "run", a.command()), a.probe...)
	out, runErr := run(project, probeTimeout, []string{"MCPICK_DEBUG=1"}, "mcpick", args...)
	if verbose {
		fmt.Printf("--- %s: mcpick %s ---\n[stdout]\n%s\n[stderr]\n%s\n", a.name, strings.Join(args, " "),
			strings.TrimSpace(out.stdout), strings.TrimSpace(out.stderr))
	}

	f := findings{probe: out.stdout + "\n" + out.stderr, runErr: runErr}
	if a.unsupported != "" {
		f.notice = strings.Contains(out.stderr, "not supported")
	}
	if a.proof&byListing != 0 {
		// gemini writes its listing to stderr; mcpick's own lines are
		// dropped so that its debug command line cannot count as one.
		listed := names2quoted(withoutMcpick(f.probe))
		if sameSet(listed, chosen) {
			f.evidence = append(f.evidence, "listing names exactly "+strings.Join(chosen, ", "))
		} else {
			f.mismatches = append(f.mismatches, fmt.Sprintf("listing names %v, want exactly %v", listed, chosen))
		}
	}
	if a.proof&byConnect != 0 {
		conns, err := connections()
		if err != nil {
			f.broken = append(f.broken, err.Error())
		}
		var got []string
		for n := range conns {
			got = append(got, n)
		}
		if sameSet(got, chosen) {
			clients := map[string]bool{}
			for _, cs := range conns {
				for _, c := range cs {
					clients[c] = true
				}
			}
			f.evidence = append(f.evidence, fmt.Sprintf("initialize from %s reached exactly %s",
				strings.Join(sortedKeys(clients), ", "), strings.Join(chosen, ", ")))
		} else {
			f.mismatches = append(f.mismatches, fmt.Sprintf("servers that saw initialize: %v, want exactly %v", sorted(got), chosen))
		}
	}
	for _, d := range a.dirs {
		if _, err := os.Stat(filepath.Join(project, d)); err == nil {
			f.broken = append(f.broken, d+" was left in the project after the run")
		}
	}
	if runErr != nil && errors.Is(runErr, context.DeadlineExceeded) {
		f.broken = append(f.broken, "the probe ran past "+probeTimeout.String())
	}
	if !saved {
		f.evidence = append(f.evidence, "(level a failed: --select used instead of the saved selection)")
	}
	exit := "exited 0"
	if runErr != nil {
		exit = runErr.Error()
	}
	f.evidence = append(f.evidence, exit)

	verdict, detail := judge(a, f)
	if a.unsupported != "" {
		verdict, detail = judgeUnsupported(a, f)
	}
	if verdict == "FAIL" {
		return fail(r, detail+"\n[stdout]\n"+out.stdout+"\n[stderr]\n"+out.stderr)
	}
	r.verdict, r.detail = verdict, detail
	return r
}

// findings is what levelB collected before it decides, kept apart by what
// a known gap may excuse.
type findings struct {
	evidence   []string // what the proof confirmed
	mismatches []string // what the proof found wrong: the listing or the connections
	broken     []string // no gap excuses these: a late probe, files left behind, an unreadable log
	probe      string   // the probe's stdout and stderr
	runErr     error
	// notice says mcpick's stderr said the agent is not supported (for
	// an unsupported agent only).
	notice bool
}

// judge turns the findings into a verdict. A known gap is accepted only
// when the run was otherwise sound — nothing broken, the probe exited 0 —
// and the probe said what the gap says; any other failure of a gap agent
// is a new one and fails the run like anybody else's.
func judge(a agent, f findings) (verdict, detail string) {
	switch {
	case len(f.broken) > 0:
		return "FAIL", strings.Join(append(f.broken, f.mismatches...), "; ")
	case len(f.mismatches) == 0 && a.gap != "":
		return "FAIL", "known gap no longer fails, drop the note: " + a.gap
	case len(f.mismatches) == 0:
		return "pass", strings.Join(f.evidence, "; ")
	case a.gap == "":
		return "FAIL", strings.Join(f.mismatches, "; ")
	case f.runErr != nil:
		return "FAIL", "not the known gap, the probe " + f.runErr.Error() + ": " + strings.Join(f.mismatches, "; ")
	case a.gapSays == "" || !strings.Contains(f.probe, a.gapSays):
		return "FAIL", fmt.Sprintf("not the known gap, the probe did not say %q: %s", a.gapSays, strings.Join(f.mismatches, "; "))
	default:
		return "gap", a.gap + " — " + strings.Join(f.mismatches, "; ")
	}
}

// judgeUnsupported is the verdict for an agent mcpick does not support:
// not the listing, which no selection can reach, but the launch itself —
// the probe ran to completion and exited 0, mcpick said the agent is not
// supported, and nothing was left in the project. Anything else is a
// FAIL; a sound launch is recorded as "not supported".
func judgeUnsupported(a agent, f findings) (verdict, detail string) {
	var wrong []string
	wrong = append(wrong, f.broken...)
	if f.runErr != nil {
		wrong = append(wrong, "the probe "+f.runErr.Error())
	}
	if !f.notice {
		wrong = append(wrong, "mcpick did not say the agent is not supported")
	}
	if len(wrong) > 0 {
		return "FAIL", strings.Join(wrong, "; ")
	}
	return "not supported", a.unsupported + "; launched unchanged, exited 0"
}

// withoutMcpick drops the lines mcpick itself prints on stderr.
func withoutMcpick(s string) string {
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "mcpick:") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// names2quoted lists the fake servers an agent's listing mentions. Names
// are matched as words so that "srv-alpha" does not also count for
// "srv-alphabet" in some future catalog.
func names2quoted(listing string) []string {
	var out []string
	for _, f := range fakes {
		if regexp.MustCompile(`(^|[^a-z-])` + regexp.QuoteMeta(f.name) + `($|[^a-z-])`).MatchString(listing) {
			out = append(out, f.name)
		}
	}
	return out
}

// connections reads the fakes' logs: for each server that received an
// initialize, the clients that sent it.
func connections() (map[string][]string, error) {
	out := map[string][]string{}
	for _, f := range fakes {
		data, err := os.ReadFile(logPath(f.name))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if name, client, ok := parseInitialize(line); ok {
				if name != f.name {
					return nil, fmt.Errorf("%s logged a line for %s", f.name, name)
				}
				out[name] = appendUnique(out[name], client)
			}
		}
	}
	return out, nil
}

// parseInitialize reads one fake log line, "<time> <server> <transport>
// initialize client=<name>/<version> ...", and returns the server and client.
func parseInitialize(line string) (server, client string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[3] != "initialize" {
		return "", "", false
	}
	return fields[1], strings.TrimPrefix(fields[4], "client="), true
}

func truncateLogs() error {
	for _, f := range fakes {
		// The HTTP fakes hold their log open in append mode, so truncating
		// in place is enough; the stdio ones open theirs on each start.
		if err := os.Truncate(logPath(f.name), 0); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// --- processes ---------------------------------------------------------------

type output struct{ stdout, stderr string }

func command(dir string, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...) //nolint:noctx // lifetime managed by the caller
	cmd.Dir = dir
	return cmd
}

// run executes a command with a deadline, killing its whole process group
// when it is late: mcpick's children (the agent, its stdio servers) must not
// outlive a probe and pollute the next agent's logs.
func run(dir string, timeout time.Duration, env []string, name string, args ...string) (output, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	inOwnGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return output{stdout.String(), stderr.String()}, err
}

// --- reporting -----------------------------------------------------------------

func fail(r result, detail string) result {
	r.verdict, r.detail = "FAIL", detail
	return r
}

func printTable(results []result) {
	fmt.Println("\n| agent | version | level | result | proof / reason |")
	fmt.Println("|---|---|---|---|---|")
	for _, r := range results {
		fmt.Printf("| %s | %s | %s | %s | %s |\n", r.agent, cell(r.version), r.level, r.verdict, cell(firstLine(r.detail)))
	}
	for _, r := range results {
		if r.verdict == "FAIL" && strings.Contains(r.detail, "\n") {
			fmt.Printf("\n--- %s, level %s ---\n%s\n", r.agent, r.level, strings.TrimSpace(r.detail))
		}
	}
}

func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- small helpers -----------------------------------------------------------------

func names() []string {
	out := make([]string, 0, len(fakes))
	for _, f := range fakes {
		out = append(out, f.name)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	if contains(list, s) {
		return list
	}
	return append(list, s)
}

func sorted(list []string) []string {
	out := append([]string(nil), list...)
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysWhere(m map[string]bool, v bool) []string {
	var out []string
	for k, x := range m {
		if x == v {
			out = append(out, k)
		}
	}
	return sorted(out)
}

func sameSet(a, b []string) bool {
	a, b = sorted(a), sorted(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
