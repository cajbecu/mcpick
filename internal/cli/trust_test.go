package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/trust"
)

// trustWorkspace is a repository whose catalog names this test binary as a
// stdio server (see TestMain): running it leaves a marker file, so a test can
// tell whether a command ran. The env carries a secret that must never reach
// trust.json. It returns the marker path and a runner.
func trustWorkspace(t *testing.T) (marker string, run func(args ...string) (out, errw string, err error)) {
	t.Helper()
	return trustWorkspaceWith(t, "")
}

// trustWorkspaceWith is trustWorkspace with more catalog entries appended;
// %s in extra is the test binary's path, MARKER its marker file.
func trustWorkspaceWith(t *testing.T, extra string) (marker string, run func(args ...string) (out, errw string, err error)) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker = filepath.Join(t.TempDir(), "ran")
	cat := "servers:\n  evil:\n    type: stdio\n    command: \"" + filepath.ToSlash(exe) + "\"\n" +
		"    env: {MCPICK_TEST_TOUCH: \"" + filepath.ToSlash(marker) + "\", SECRET: hunter2}\n"
	cat += strings.ReplaceAll(strings.ReplaceAll(extra, "%s", filepath.ToSlash(exe)), "MARKER", filepath.ToSlash(marker))
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	run = func(args ...string) (string, string, error) {
		t.Helper()
		var out, errw strings.Builder
		a := &app{out: &out, errw: &errw, version: "test"}
		err := a.run(append([]string{"--timeout", "5s"}, args...))
		return out.String(), errw.String(), err
	}
	return marker, run
}

func ran(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

// Selecting a stdio server is consent to launch it, not to run it from a
// measurement: without --trust nothing runs, and the message says what would.
func TestMeasureDoesNotRunUntrustedCommandEvenWhenSelected(t *testing.T) {
	marker, run := trustWorkspace(t)
	_, errw, err := run("--select", "evil", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if ran(marker) {
		t.Fatal("measure ran an untrusted command")
	}
	if !strings.Contains(errw, "not trusted") || !strings.Contains(errw, "--trust") || !strings.Contains(errw, "evil") {
		t.Errorf("stderr = %q; the skip should name the server and the flag", errw)
	}
	out, _, err := run("--json", "measure")
	if err != nil || !strings.Contains(out, `"skipped": true`) || !strings.Contains(out, `"name": "evil"`) {
		t.Errorf("--json = %q, %v; a skipped server must be in the output", out, err)
	}
	if ran(marker) {
		t.Fatal("--json ran an untrusted command")
	}
}

// --trust runs the command, with its env, and remembers it: the next plain
// measure runs it too. The record holds the program, never the env value.
func TestMeasureTrustRunsAndRemembers(t *testing.T) {
	marker, run := trustWorkspace(t)
	_, errw, err := run("--trust", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if !ran(marker) {
		t.Fatal("--trust did not run the command")
	}
	if !strings.Contains(errw, "trusted 1 command(s)") {
		t.Errorf("stderr = %q", errw)
	}
	path := filepath.Join(os.Getenv("MCPICK_HOME"), "state", "trust.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"evil"`) || strings.Contains(string(data), "hunter2") {
		t.Errorf("trust.json = %s", data)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("trust.json is %v, want 0600", fi.Mode().Perm())
		}
	}

	os.Remove(marker)
	if _, errw, err := run("measure"); err != nil || !ran(marker) {
		t.Fatalf("a trusted command was not run by a plain measure: %v, stderr %q", err, errw)
	}
	if _, errw, _ := run("--json", "measure"); strings.Contains(errw, "not trusted") {
		t.Errorf("stderr = %q after trusting", errw)
	}
}

// The user's own servers are not trusted either: a stdio entry in
// ~/.claude.json is written by other programs and asks like any other.
func TestMeasureSkipsOwnUserStdioServer(t *testing.T) {
	marker, run := trustWorkspace(t)
	exe, _ := os.Executable()
	mine := filepath.Join(t.TempDir(), "mine-ran")
	claudeJSON := `{"mcpServers":{"mine":{"type":"stdio","command":` + jsonString(filepath.ToSlash(exe)) +
		`,"env":{"MCPICK_TEST_TOUCH":` + jsonString(filepath.ToSlash(mine)) + `}}}}`
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json"), []byte(claudeJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errw, err := run("--all", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if ran(mine) || ran(marker) {
		t.Fatal("a stdio server ran without being trusted")
	}
	if !strings.Contains(errw, "mine") {
		t.Errorf("stderr = %q; the user server should be named", errw)
	}
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

// doctor promises every selected server was reached: an untrusted one is
// reported as SKIP, in --json as skipped, and the exit status is 1.
func TestDoctorSkipsUntrustedAndFails(t *testing.T) {
	marker, run := trustWorkspace(t)
	out, _, err := run("--select", "evil", "doctor")
	var code proc.ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("doctor = %v, want exit 1", err)
	}
	if ran(marker) {
		t.Fatal("doctor ran an untrusted command")
	}
	if !strings.Contains(out, "SKIP  evil") || !strings.Contains(out, "--trust") || !strings.Contains(out, "1 skipped") {
		t.Errorf("doctor output = %q", out)
	}
	out, _, _ = run("--select", "evil", "--json", "doctor")
	if !strings.Contains(out, `"skipped": true`) {
		t.Errorf("--json = %q", out)
	}
	// With --trust the command runs (and, being a test binary, exits at
	// once: a FAIL, not a SKIP).
	out, _, _ = run("--select", "evil", "--trust", "doctor")
	if !ran(marker) {
		t.Fatal("doctor --trust did not run the command")
	}
	if strings.Contains(out, "SKIP") {
		t.Errorf("output = %q; nothing should be skipped once trusted", out)
	}
}

// --trust is a measuring flag: accepted anywhere, meaningless elsewhere, and
// never a reason for list to run anything.
func TestTrustFlagIsInertOutsideMeasuring(t *testing.T) {
	opt, cmd, _, err := parseArgs([]string{"--trust", "list"})
	if err != nil || cmd != "list" || !opt.trust {
		t.Fatalf("parseArgs = %+v %q %v", opt, cmd, err)
	}
	marker, run := trustWorkspace(t)
	if out, _, err := run("--trust", "list"); err != nil || !strings.Contains(out, "evil") {
		t.Fatalf("list = %q, %v", out, err)
	}
	if ran(marker) {
		t.Fatal("list ran a command")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("MCPICK_HOME"), "state", "trust.json")); err == nil {
		t.Fatal("list granted trust")
	}
}

// `url: ${VAR}` is a remote entry as written and needs no trust; with the
// variable unset it expands to no URL, and the probe would run the entry's
// command instead. measure and doctor refuse it before anything runs.
func TestEmptyExpandedURLDoesNotRunCommand(t *testing.T) {
	t.Setenv("MCPICK_REVIEW_EMPTY", "")
	marker, run := trustWorkspaceWith(t, "  sneaky:\n    url: \"${MCPICK_REVIEW_EMPTY}\"\n    command: \"%s\"\n"+
		"    env: {MCPICK_TEST_TOUCH: \"MARKER\"}\n")
	out, _, err := run("--select", "sneaky", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if ran(marker) {
		t.Fatal("measure ran the command of an entry whose url expanded to nothing")
	}
	if !strings.Contains(out, "sneaky") || !strings.Contains(out, "expands to nothing") {
		t.Errorf("measure output = %q; the row should say why it was not run", out)
	}
	out, _, err = run("--select", "sneaky", "doctor")
	var code proc.ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("doctor = %v, want exit 1", err)
	}
	if ran(marker) {
		t.Fatal("doctor ran the command of an entry whose url expanded to nothing")
	}
	if !strings.Contains(out, "FAIL  sneaky") || !strings.Contains(out, "expands to nothing") {
		t.Errorf("doctor output = %q", out)
	}
}

// A credential written as an argument must not be readable in what a
// skipped measurement prints — stdout, stderr or --json — while the command
// itself, and the flag, stay visible.
func TestSkippedOutputMasksCredentialArguments(t *testing.T) {
	marker, run := trustWorkspaceWith(t, "  keyed:\n    type: stdio\n    command: \"%s\"\n"+
		"    args: [--api-key, review-fake-secret, --token=review-fake-token]\n"+
		"    env: {MCPICK_TEST_TOUCH: \"MARKER\"}\n")
	for _, args := range [][]string{{"measure"}, {"--json", "measure"}, {"--select", "keyed", "doctor"}, {"--select", "keyed", "--json", "doctor"}} {
		out, errw, _ := run(args...)
		all := out + errw
		for _, leak := range []string{"review-fake-secret", "review-fake-token"} {
			if strings.Contains(all, leak) {
				t.Errorf("%v printed %q:\n%s", args, leak, all)
			}
		}
		if !strings.Contains(all, "--api-key ****") || !strings.Contains(all, "--token=****") {
			t.Errorf("%v should show the flags with their values masked:\n%s", args, all)
		}
	}
	if ran(marker) {
		t.Fatal("something ran the command")
	}
}

// doctor gates the catalog as written, before expanding anything: an
// untrusted entry whose ${VAR:?} is unset is reported as SKIP, and the other
// selected servers are still probed, instead of the whole run aborting on
// the variable.
func TestDoctorGatesBeforeExpansion(t *testing.T) {
	marker, run := trustWorkspaceWith(t, "  needy:\n    type: stdio\n    command: \"%s\"\n"+
		"    args: [\"${MCPICK_REVIEW_UNSET_VAR:?set me}\"]\n    env: {MCPICK_TEST_TOUCH: \"MARKER\"}\n"+
		"  far:\n    type: http\n    url: http://127.0.0.1:1/mcp\n")
	out, _, err := run("--select", "needy,far", "doctor")
	var code proc.ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("doctor = %v, want exit 1 (a skip), not the variable error", err)
	}
	if ran(marker) {
		t.Fatal("doctor ran an untrusted command")
	}
	if !strings.Contains(out, "SKIP  needy") || !strings.Contains(out, "FAIL  far") || !strings.Contains(out, "1 skipped") {
		t.Errorf("doctor output = %q; needy should be skipped and far probed", out)
	}
	// Trusted, the same entry is resolved, and the missing variable is the
	// error it always was.
	_, _, err = run("--select", "needy", "--trust", "doctor")
	if err == nil || !strings.Contains(err.Error(), "MCPICK_REVIEW_UNSET_VAR") {
		t.Errorf("doctor --trust = %v, want the variable error", err)
	}
}

// Review round 2, finding 6: --trust approves commands the user never saw,
// so before anything runs stderr says what is being approved — each command
// as it executes, with env values that look secret masked — and only then
// that they were trusted.
func TestTrustFlagPrintsWhatItApproves(t *testing.T) {
	marker, run := trustWorkspace(t)
	_, errw, err := run("--trust", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if !ran(marker) {
		t.Fatal("--trust did not run the command")
	}
	exe, _ := os.Executable()
	approve := strings.Index(errw, "approves evil: ")
	done := strings.Index(errw, "trusted 1 command(s)")
	if approve < 0 || done < 0 || approve > done {
		t.Fatalf("stderr = %q; what --trust approves must be printed, before it says so", errw)
	}
	line := errw[approve : strings.Index(errw[approve:], "\n")+approve]
	for _, want := range []string{filepath.Base(exe), "MCPICK_TEST_TOUCH=", "SECRET=****"} {
		if !strings.Contains(line, want) {
			t.Errorf("approval line = %q, want %q", line, want)
		}
	}
	if strings.Contains(errw, "hunter2") {
		t.Errorf("stderr = %q; an env value that looks secret was printed", errw)
	}
}

// A saved selection is a list of checks, each bound to the server it was
// given to: its origin and its spec as written. The check on a user server
// must not carry to a repository server that has since taken its name, a
// checked repository server that changed its URL or headers asks again, and
// a selection saved before checks were recorded confirms none of its
// repository servers. The rest of the selection stands, and stderr names
// each dropped server and why.
func TestSavedCheckIsBoundToOriginAndSpec(t *testing.T) {
	var errw strings.Builder
	a := testApp(t, options{uid: "uid", last: true})
	a.errw = &errw
	http := func(u string) map[string]any { return map[string]any{"type": "http", "url": u} }
	user := []catalog.Server{
		{Name: "github", Origin: catalog.OriginUser, Spec: http("https://x/github")},
		{Name: "sentry", Origin: catalog.OriginUser, Spec: http("https://x/sentry")},
	}
	a.cat.Servers = user
	if err := a.saveSelection(map[string]bool{"github": true, "sentry": true}, "claude"); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(a.root, "uid")
	if err != nil || st.Checks["github"].Origin != catalog.OriginUser || st.Checks["github"].Spec != trust.SpecFingerprint(user[0].Spec) {
		t.Fatalf("saved %+v, %v; the selection should record origin and spec per check", st, err)
	}

	// The repository now defines github, with the user's spec word for word.
	a.cat.Servers = []catalog.Server{{Name: "github", Origin: catalog.OriginProject, Spec: http("https://x/github")}, user[1]}
	sel, err := a.selection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel["github"] || !sel["sentry"] || a.consent["github"] || !a.consent["sentry"] {
		t.Errorf("sel=%v consent=%v; the shadowing server must not inherit the check", sel, a.consent)
	}
	if !strings.Contains(errw.String(), "github now comes from this repository's catalog") || !strings.Contains(errw.String(), "--select github") {
		t.Errorf("stderr = %q", errw.String())
	}

	// Checked as a repository server, then its URL changes.
	errw.Reset()
	if err := a.saveSelection(map[string]bool{"github": true}, "claude"); err != nil {
		t.Fatal(err)
	}
	if sel, _ = a.selection(nil); !sel["github"] || errw.String() != "" {
		t.Fatalf("a repository server as it was checked should stay selected: sel=%v stderr=%q", sel, errw.String())
	}
	a.cat.Servers[0].Spec = http("https://evil/github")
	if sel, _ = a.selection(nil); sel["github"] || !strings.Contains(errw.String(), "github changed in this repository's catalog since it was checked") {
		t.Errorf("sel=%v stderr=%q; a changed spec must ask again", sel, errw.String())
	}

	// A selection saved before checks were recorded.
	errw.Reset()
	if err := state.Save(a.root, "uid", state.State{Selected: []string{"github", "sentry"}}); err != nil {
		t.Fatal(err)
	}
	if sel, _ = a.selection(nil); sel["github"] || !sel["sentry"] {
		t.Errorf("legacy selection: sel=%v", sel)
	}
	if !strings.Contains(errw.String(), "github was checked before mcpick recorded what it checked") {
		t.Errorf("stderr = %q", errw.String())
	}
}

// End to end: a user server with a `${VAR}` header is checked and saved;
// then the repository's .mcp.yaml defines a server of the same name. A bare
// measure must not send the variable to the repository's host, and the
// saved selection must not hand the repository's server to the agent.
func TestShadowingRepositoryServerInheritsNoCheck(t *testing.T) {
	_, root, run := catalogWorkspace(t)
	t.Setenv("MCPICK_REVIEW_TOKEN", "review-fake-token")
	claudeJSON := `{"mcpServers":{"github":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"Authorization":"Bearer ${MCPICK_REVIEW_TOKEN}"}}}}`
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json"), []byte(claudeJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	// The launch that checked the user's github and saved the selection.
	cat, err := catalog.Load(catalog.Path(root), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := cat.Find("github"); s.Origin != catalog.OriginUser {
		t.Fatalf("github should be the user's own here, got %+v", s)
	}
	host, _ := os.Hostname()
	if err := state.Save(root, host, state.State{Selected: []string{"github"}, Checks: trust.Checks(cat.Servers, map[string]bool{"github": true})}); err != nil {
		t.Fatal(err)
	}
	// Checked, the user's own server is measured (tried: connection refused).
	out, errw, err := run("--json", "measure")
	if err != nil || !strings.Contains(out, `"name": "github"`) || strings.Contains(out, `"name": "github",
    "ok": false,
    "error": "skipped`) {
		t.Fatalf("the user's checked server should be tried: %v\n%s%s", err, out, errw)
	}

	// The repository takes the name.
	file := filepath.Join(root, ".mcp.yaml")
	repo := "  github:\n    type: http\n    url: http://127.0.0.1:1/mcp\n    headers: {Authorization: \"Bearer ${MCPICK_REVIEW_TOKEN}\"}\n"
	if err := os.WriteFile(file, append(mustRead(t, file), []byte(repo)...), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errw, err = run("--json", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name": "github",
    "ok": false,
    "error": "skipped`) || !strings.Contains(out, "sends ${MCPICK_REVIEW_TOKEN} to its host") {
		t.Errorf("the repository's github must be skipped, not measured with the token:\n%s", out)
	}
	if !strings.Contains(errw, "github now comes from this repository's catalog") {
		t.Errorf("stderr lacks the unconfirmed check:\n%s", errw)
	}
	// The repository's copy is the user's word for word, so the shadow
	// line stays silent: the check is what says the name changed hands.
	if strings.Contains(errw, "shadow") {
		t.Errorf("an identical definition is not worth a shadow line:\n%s", errw)
	}
	// Nor is it handed to the agent on the strength of the saved selection.
	out, errw, err = run("-y", "--agent", "codex", "export")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "github") || !strings.Contains(errw, "github now comes from this repository's catalog") {
		t.Errorf("export = %q, stderr = %q; the swapped server must not be handed over", out, errw)
	}
	// --select is explicit consent for the server as it is now.
	out, _, _ = run("--select", "github", "--json", "measure")
	if strings.Contains(out, `"name": "github",
    "ok": false,
    "error": "skipped`) {
		t.Errorf("--select github should measure the repository's github:\n%s", out)
	}
}

// The --trust notice goes to stderr, which ends up in logs: a credential
// written in args is masked there, as env values already were.
func TestTrustFlagNoticeMasksCredentialArguments(t *testing.T) {
	marker, run := trustWorkspaceWith(t, "  keyed:\n    type: stdio\n    command: \"%s\"\n"+
		"    args: [--api-key, review-fake-secret, --token=review-fake-token]\n"+
		"    env: {MCPICK_TEST_TOUCH: \"MARKER\"}\n")
	_, errw, err := run("--trust", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if !ran(marker) {
		t.Fatal("--trust did not run the commands")
	}
	for _, leak := range []string{"review-fake-secret", "review-fake-token", "hunter2"} {
		if strings.Contains(errw, leak) {
			t.Errorf("the --trust notice printed %q:\n%s", leak, errw)
		}
	}
	if !strings.Contains(errw, "approves keyed: ") || !strings.Contains(errw, "--api-key ****") || !strings.Contains(errw, "--token=****") {
		t.Errorf("the notice should show the flags with their values masked:\n%s", errw)
	}
}

// --trust approves commands in bulk; a hidden server is one the user does
// not want here, so its command is neither approved nor run, and the run
// says which were left out.
func TestMeasureTrustLeavesHiddenServersOut(t *testing.T) {
	marker, run := trustWorkspace(t)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SaveHidden(root, map[string]bool{"evil": true}); err != nil {
		t.Fatal(err)
	}
	_, errw, err := run("--trust", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if ran(marker) {
		t.Fatal("--trust ran a hidden server's command")
	}
	if !strings.Contains(errw, "hidden") || !strings.Contains(errw, "evil") {
		t.Errorf("stderr = %q; the hidden server left out must be named", errw)
	}
	if strings.Contains(errw, "approves evil") || strings.Contains(errw, "trusted 1 command") {
		t.Errorf("stderr = %q; a hidden server must not be trusted", errw)
	}
}
