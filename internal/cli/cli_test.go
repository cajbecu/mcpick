package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/backend"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/oauth"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/tui"
)

func TestParseArgsCommandActsAsSeparator(t *testing.T) {
	opt, cmd, rest, err := parseArgs([]string{"--uid", "box-1", "run", "claude", "--all", "-y"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "run" {
		t.Fatalf("cmd = %q", cmd)
	}
	if opt.uid != "box-1" {
		t.Errorf("uid = %q", opt.uid)
	}
	// Everything after the command belongs to the child, including flags that
	// happen to share a name with mcpick's own.
	if strings.Join(rest, " ") != "claude --all -y" {
		t.Errorf("rest = %v", rest)
	}
	if opt.all || opt.last {
		t.Error("flags after the command must not be consumed by mcpick")
	}
}

// `mcpick import --redact` is how people type it; the flag used to be handed to
// the command as an argument and silently ignored.
func TestParseArgsFlagsAfterNonRunCommand(t *testing.T) {
	opt, cmd, rest, err := parseArgs([]string{"import", "--redact", "--file", "out.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "import" {
		t.Fatalf("cmd = %q", cmd)
	}
	if !opt.redact {
		t.Error("--redact after the command was not applied")
	}
	if opt.file != "out.yaml" {
		t.Errorf("file = %q", opt.file)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want nothing positional", rest)
	}
}

func TestParseArgsPositionalForLogin(t *testing.T) {
	opt, cmd, rest, err := parseArgs([]string{"login", "sentry", "--uid", "box"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "login" || len(rest) != 1 || rest[0] != "sentry" {
		t.Fatalf("cmd = %q, rest = %v", cmd, rest)
	}
	if opt.uid != "box" {
		t.Errorf("uid = %q", opt.uid)
	}
}

func TestParseArgsRejectsUnknownCommand(t *testing.T) {
	if _, _, _, err := parseArgs([]string{"frobnicate"}); err == nil {
		t.Fatal("a bare unknown word must be reported as an unknown command")
	}
}

func TestParseArgsEqualsForm(t *testing.T) {
	opt, cmd, _, err := parseArgs([]string{"--uid=box-2", "--agent=codex", "--profile=review", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "list" {
		t.Fatalf("cmd = %q", cmd)
	}
	if opt.uid != "box-2" || opt.agent != "codex" || opt.profile != "review" {
		t.Errorf("opt = %+v", opt)
	}
}

func TestParseArgsTimeout(t *testing.T) {
	opt, _, _, err := parseArgs([]string{"--timeout", "3s", "doctor"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.timeout != 3*time.Second {
		t.Errorf("timeout = %v", opt.timeout)
	}
	if _, _, _, err := parseArgs([]string{"--timeout", "soon", "doctor"}); err == nil {
		t.Error("an unparseable duration must be rejected")
	}
	// A bare number is the likeliest mistake; the error says what to write.
	if _, _, _, err := parseArgs([]string{"--timeout", "5", "doctor"}); err == nil || !strings.Contains(err.Error(), "needs a unit, e.g. 5s") {
		t.Errorf("--timeout 5 = %v, want it to ask for a unit", err)
	}
}

// runMain runs a whole command line as Main does, with its output captured,
// in a throwaway project and home.
func runMain(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test"}
	code = a.exit(a.run(args))
	return out.String(), errw.String(), code
}

func throwawayWorkspace(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
}

// Forgetting a token that is not there is a mistake worth a status: a
// typo in the server name must not look like a successful logout.
func TestLogoutOfAnUnknownServerFails(t *testing.T) {
	throwawayWorkspace(t)
	if out, errw, code := runMain(t, "logout", "ghost"); code != 1 || !strings.Contains(errw, "no stored token for ghost") {
		t.Errorf("logout ghost = %d %q %q, want exit 1 naming the server", code, out, errw)
	}
	if err := oauth.Put(oauth.Key("a", "https://x/a"), oauth.Token{AccessToken: "t"}); err != nil {
		t.Fatal(err)
	}
	if out, errw, code := runMain(t, "logout", "a"); code != 0 || !strings.Contains(out, "forgot 1 token(s) for a") {
		t.Errorf("logout a = %d %q %q, want the token forgotten", code, out, errw)
	}
}

// `mcpick agents` names the files an agent also reads as paths, not as the
// {root}/{home}/{xdg} templates they are stored as.
func TestAgentsPrintsPathsNotTemplates(t *testing.T) {
	throwawayWorkspace(t)
	out, errw, code := runMain(t, "agents")
	if code != 0 {
		t.Fatalf("agents = %d %q", code, errw)
	}
	for _, want := range []string{"./.mcp.json", "~/.claude.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("agents lacks %q:\n%s", want, out)
		}
	}
	for _, tmpl := range []string{"{root}", "{home}", "{xdg}"} {
		if strings.Contains(out, tmpl) {
			t.Errorf("agents prints the template %s:\n%s", tmpl, out)
		}
	}
}

func TestParseArgsRejectsUnknownFlagAndMissingValue(t *testing.T) {
	if _, _, _, err := parseArgs([]string{"--nope", "list"}); err == nil {
		t.Error("unknown flags must be rejected")
	}
	if _, _, _, err := parseArgs([]string{"--uid"}); err == nil {
		t.Error("a flag without its value must be rejected")
	}
	if _, _, _, err := parseArgs(nil); err == nil {
		t.Error("no command at all must be rejected")
	}
}

func TestParseArgsDefaultTimeout(t *testing.T) {
	opt, _, _, err := parseArgs([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.timeout != 15*time.Second {
		t.Errorf("timeout = %v, want the 15s default", opt.timeout)
	}
}

func sampleCatalog() *catalog.Catalog {
	http := func(u string) map[string]any { return map[string]any{"type": "http", "url": u} }
	return &catalog.Catalog{
		Path:     "/src/app/.mcp.yaml",
		Profiles: map[string][]string{"review": {"github", "sentry"}},
		Servers: []catalog.Server{
			{Name: "github", Origin: catalog.OriginProject, Spec: http("https://x/github")},
			{Name: "sentry", Origin: catalog.OriginUser, Spec: http("https://x/sentry")},
			{Name: "browser", Origin: catalog.OriginUser, Spec: http("http://127.0.0.1:9010/mcp")},
		},
	}
}

func testApp(t *testing.T, opt options) *app {
	t.Helper()
	t.Setenv("MCPICK_HOME", t.TempDir())
	return &app{opt: opt, out: io.Discard, errw: io.Discard, root: t.TempDir(), cat: sampleCatalog()}
}

func TestSelectionPrecedence(t *testing.T) {
	a := testApp(t, options{uid: "uid"})
	if err := state.Save(a.root, "uid", state.State{Selected: []string{"browser"}}); err != nil {
		t.Fatal(err)
	}
	check := func(opt options, want ...string) {
		t.Helper()
		opt.uid = "uid"
		a.opt = opt
		sel, err := a.selection(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(sel) != len(want) {
			t.Errorf("%+v selected %v, want %v", opt, sel, want)
		}
		for _, w := range want {
			if !sel[w] {
				t.Errorf("%+v selected %v, want %v", opt, sel, want)
			}
		}
	}
	check(options{selects: "github", profile: "review", all: true}, "github") // --select beats all
	check(options{profile: "review", all: true}, "github", "sentry")          // then --profile
	check(options{all: true}, "github", "sentry", "browser")                  // then --all
	check(options{none: true})                                                // --none clears
	check(options{last: true}, "browser")                                     // the saved selection
	check(options{}, "browser")                                               // no terminal: saved too
}

func TestSelectionRejectsUnknownNames(t *testing.T) {
	a := testApp(t, options{uid: "uid", selects: "github,ghost"})
	if _, err := a.selection(nil); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("a typo in --select must be reported, got %v", err)
	}
}

func TestSelectionRejectsUnknownProfile(t *testing.T) {
	a := testApp(t, options{uid: "uid", profile: "nope"})
	if _, err := a.selection(nil); err == nil || !strings.Contains(err.Error(), "review") {
		t.Fatalf("the error should list the profiles that do exist, got %v", err)
	}
}

func TestVersionPrefersLinkedValue(t *testing.T) {
	if got := Version("1.2.3"); got != "1.2.3" {
		t.Errorf("Version = %s", got)
	}
	// Under `go test` there is no module version, so dev is the honest answer.
	if got := Version("dev"); got == "" {
		t.Error("Version must never be empty")
	}
}

// The whole command line, end to end, against a real workspace on disk.
func TestMainEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte(`servers:
  a: {type: http, url: "https://x/a"}
  b: {type: stdio, command: uvx, args: [thing]}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, int) {
		t.Helper()
		var out, errw strings.Builder
		a := &app{out: &out, errw: &errw, version: "test"}
		err := a.run(args)
		code := 0
		if err != nil {
			code = 1
			out.WriteString("ERR " + err.Error())
		}
		return out.String(), code
	}

	if out, code := run("list"); code != 0 || !strings.Contains(out, "project") || !strings.Contains(out, "uvx thing") {
		t.Fatalf("list = %d %q", code, out)
	}
	catalogBefore, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml"))
	if out, code := run("--select", "a", "profile", "save", "one"); code != 0 {
		t.Fatalf("profile save = %d %q", code, out)
	}
	// Profiles are personal: the repository's catalog is never written.
	if catalogAfter, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml")); string(catalogAfter) != string(catalogBefore) {
		t.Errorf("profile save changed the catalog:\n%s", catalogAfter)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("MCPICK_HOME"), "profiles.yaml")); err != nil {
		t.Errorf("profile save should write under MCPICK_HOME: %v", err)
	}
	if out, _ := run("profile"); !strings.Contains(out, "one") || !strings.Contains(out, "personal") {
		t.Errorf("profile list = %q", out)
	}
	if out, code := run("--profile", "one", "--agent", "codex", "export"); code != 0 || !strings.Contains(out, "[mcp_servers.a]") || strings.Contains(out, "[mcp_servers.b]") {
		t.Errorf("export = %d %q", code, out)
	}
	if out, code := run("profile", "rename", "one", "two"); code != 0 {
		t.Errorf("profile rename = %d %q", code, out)
	}
	if out, code := run("--profile", "two", "--agent", "codex", "export"); code != 0 || !strings.Contains(out, "[mcp_servers.a]") {
		t.Errorf("export after rename = %d %q", code, out)
	}
	if out, code := run("--profile", "default", "--agent", "codex", "export"); code != 0 || !strings.Contains(out, "[mcp_servers.a]") || !strings.Contains(out, "[mcp_servers.b]") {
		t.Errorf("--profile default should be everything: %d %q", code, out)
	}
	if out, code := run("profile", "delete", "two"); code != 0 {
		t.Errorf("profile delete = %d %q", code, out)
	}
	if _, code := run("profile", "delete", "two"); code == 0 {
		t.Error("deleting a missing profile must fail")
	}
	if _, code := run("frobnicate"); code == 0 {
		t.Error("an unknown command must fail")
	}
}

// --all, like the picker's "a", leaves servers disabled in Claude Code alone.
func TestAllSkipsDisabled(t *testing.T) {
	a := testApp(t, options{uid: "u", all: true})
	a.cat.Servers = append(a.cat.Servers, catalog.Server{Name: "off", Disabled: true, DisabledAs: "off"})
	sel, err := a.selection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel["off"] || !sel["github"] {
		t.Errorf("sel = %v", sel)
	}
}

// For an agent that does not read Claude's settings, Claude's disabled list is
// irrelevant, so --all takes every server.
func TestAllIncludesClaudeDisabledForOtherAgents(t *testing.T) {
	a := testApp(t, options{uid: "u", all: true})
	a.cat.Servers = append(a.cat.Servers, catalog.Server{Name: "off", Disabled: true, DisabledAs: "off"})
	codex, err := backend.Pick("codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := a.selection(&tui.Options{Target: codex})
	if err != nil {
		t.Fatal(err)
	}
	if !sel["off"] {
		t.Errorf("sel = %v", sel)
	}
}

// --target, the 0.1.0 name of --agent, is still accepted; --backend, an
// unreleased alias, is not a flag.
func TestTargetIsAnAliasOfAgent(t *testing.T) {
	opt, _, _, err := parseArgs([]string{"--target", "codex", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.agent != "codex" {
		t.Errorf("agent = %q", opt.agent)
	}
	if _, _, _, err := parseArgs([]string{"--backend", "codex", "list"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("--backend = %v, want it rejected as an unknown flag", err)
	}
}

// An agent name is checked on every command that takes options, so a typo
// in --agent is an error where it was typed: on list, which renders
// nothing, as on export, which does.
func TestUnknownAgentIsRejectedOnEveryCommand(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte("servers:\n  a:\n    type: http\n    url: https://x/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = `unknown agent "foo" (mcpick agents lists them)`
	for _, args := range [][]string{
		{"--agent", "foo", "list"},
		{"--agent", "foo", "--all", "export"},
		{"--target", "foo", "list"},
	} {
		var out, errw strings.Builder
		a := &app{out: &out, errw: &errw, version: "test"}
		if err := a.run(args); err == nil || err.Error() != want {
			t.Errorf("%v: err = %v, want %s", args, err, want)
		}
		if out.Len() != 0 {
			t.Errorf("%v printed %q", args, out.String())
		}
	}
	// A known one passes on both.
	for _, args := range [][]string{{"--agent", "codex", "list"}, {"--agent", "codex", "--all", "export"}} {
		var out, errw strings.Builder
		a := &app{out: &out, errw: &errw, version: "test"}
		if err := a.run(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// `mcpick measure` in a repository whose catalog runs a command: nothing runs
// until the server is selected.
func TestMeasureDoesNotRunUnselectedRepositoryServers(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("MCPICK_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	marker := filepath.Join(t.TempDir(), "ran")
	cat := "servers:\n  evil:\n    type: stdio\n    command: sh\n    args: [\"-c\", \"touch " + filepath.ToSlash(marker) + "\"]\n"
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test"}
	if err := a.run([]string{"--timeout", "2s", "measure"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("measure ran a command from the repository's catalog without it being selected")
	}
	if !strings.Contains(errw.String(), "not measured") {
		t.Errorf("stderr = %q; the skip should be explained", errw.String())
	}
}

// personalProfiles gives the app a profiles file of its own, in a temp dir.
func personalProfiles(t *testing.T, a *app, add map[string][]string) {
	t.Helper()
	a.prof = profile.New(filepath.Join(t.TempDir(), "profiles.yaml"))
	for n, srv := range add {
		if err := a.prof.Add(n, srv); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.prof.Save(); err != nil { // edits reload from disk first
		t.Fatal(err)
	}
}

// A personal profile with the same name as a catalog one must win: it is the
// one the user edits in the picker. Catalog-only names still resolve.
func TestProfileFlagPrefersPersonalThenCatalog(t *testing.T) {
	a := testApp(t, options{uid: "u", profile: "review"})
	personalProfiles(t, a, map[string][]string{"review": {"browser"}})
	sel, err := a.selection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 1 || !sel["browser"] {
		t.Errorf("sel = %v, want the personal review", sel)
	}
	personalProfiles(t, a, nil)
	sel, err = a.selection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 2 || !sel["github"] || !sel["sentry"] {
		t.Errorf("sel = %v, want the catalog review", sel)
	}
}

// `--profile default` is spelled out as --all, so whatever --all skips
// (servers disabled in Claude Code, hidden ones later) applies to it too.
func TestProfileDefaultIsAll(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte("servers:\n  a: {url: https://x/a}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{out: io.Discard, errw: io.Discard, version: "test"}
	if err := a.run([]string{"--profile", "default", "list"}); err != nil {
		t.Fatal(err)
	}
	if a.opt.profile != "" || !a.opt.all {
		t.Errorf("opt = %+v, want --profile default rewritten to --all", a.opt)
	}
}

// A catalog written before "default" was reserved may define one that lists a
// few safe servers. Turning `--profile default` into --all there would launch
// every server in the project, including stdio commands the profile never
// named; it is refused with the way out instead, and nothing is emitted.
func TestCatalogDefaultIsRefusedNotBroadened(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte(`servers:
  safe: {type: http, url: "https://x/safe"}
  danger: {type: stdio, command: rm, args: [-rf, /]}
profiles:
  default: [safe]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test"}
	err := a.run([]string{"--profile", "default", "--agent", "codex", "export"})
	if err == nil || !strings.Contains(err.Error(), ".mcp.yaml") || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("export with the catalog's default: %v", err)
	}
	if a.opt.all {
		t.Error("--profile default must not become --all while the catalog defines default")
	}
	if strings.Contains(out.String(), "danger") || strings.Contains(out.String(), "safe") {
		t.Errorf("nothing should be exported:\n%s", out.String())
	}
	// The listing still shows both, told apart by source, and says what to do.
	out.Reset()
	if err := a.run([]string{"profile"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "catalog, rename it") || !strings.Contains(out.String(), "built-in") {
		t.Errorf("profile list:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "defines a profile named \"default\"") {
		t.Errorf("stderr should warn once about the collision:\n%s", errw.String())
	}
}

// A global profile may name a server another project has; here it is
// skipped, and said so once, rather than failing the launch.
func TestProfileFlagWarnsAboutMissing(t *testing.T) {
	var errw strings.Builder
	a := testApp(t, options{uid: "u", profile: "p"})
	a.errw = &errw
	personalProfiles(t, a, map[string][]string{"p": {"github", "ghost", "phantom"}})
	sel, err := a.selection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 1 || !sel["github"] {
		t.Errorf("sel = %v", sel)
	}
	if n := strings.Count(errw.String(), "not in this project"); n != 1 || !strings.Contains(errw.String(), "ghost, phantom") {
		t.Errorf("stderr = %q; want one warning naming both", errw.String())
	}
}

// Only personal profiles can be deleted or renamed from here; the error has
// to say where a catalog profile lives, and that default is built-in.
func TestProfileDeleteRefusesCatalogAndDefault(t *testing.T) {
	a := testApp(t, options{uid: "u", file: "/src/app/.mcp.yaml"})
	personalProfiles(t, a, nil)
	if err := a.profileCmd([]string{"delete", "review"}); err == nil || !strings.Contains(err.Error(), ".mcp.yaml") {
		t.Errorf("deleting a catalog profile: %v", err)
	}
	if err := a.profileCmd([]string{"delete", "default"}); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Errorf("deleting default: %v", err)
	}
	if err := a.profileCmd([]string{"rename", "review", "x"}); err == nil {
		t.Error("renaming a catalog profile must fail")
	}
	if err := a.profileCmd([]string{"save", "default"}); err == nil {
		t.Error("saving over default must fail")
	}
	if len(a.cat.Profiles["review"]) != 2 {
		t.Error("the catalog profile was touched")
	}
}

// `profile list` shows where each profile comes from, in the order the picker
// uses, and --json carries the same order and provenance.
func TestProfileListShowsSources(t *testing.T) {
	var out strings.Builder
	a := testApp(t, options{uid: "u"})
	a.out = &out
	personalProfiles(t, a, map[string][]string{"zeta": {"browser", "ghost"}, "review": {"github"}})
	if err := a.profileCmd(nil); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"zeta", "personal", "ghost (missing)", "catalog, shadowed", "default", "built-in"} {
		if !strings.Contains(text, want) {
			t.Errorf("list lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "zeta") > strings.Index(text, "default") {
		t.Errorf("personal profiles should come before default:\n%s", text)
	}

	out.Reset()
	a.opt.jsonOut = true
	if err := a.profileCmd(nil); err != nil {
		t.Fatal(err)
	}
	js := out.String()
	if !strings.HasPrefix(strings.TrimSpace(js), "[") {
		t.Errorf("--json should be an ordered list:\n%s", js)
	}
	for _, want := range []string{`"source": "personal"`, `"source": "catalog"`, `"source": "built-in"`, `"shadowed": true`, `"missing": [`} {
		if !strings.Contains(js, want) {
			t.Errorf("json lacks %s:\n%s", want, js)
		}
	}
}

// While the personal file cannot be read, --profile NAME must not quietly
// resolve to the catalog's profile of that name: a personal one hiding
// behind the error would have taken precedence, possibly with other servers.
func TestProfileFlagRefusesUnreadablePersonalFile(t *testing.T) {
	a := testApp(t, options{uid: "u", profile: "review"})
	if err := os.WriteFile(profile.Path(), []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sel, err := a.selection(nil)
	if err == nil || !strings.Contains(err.Error(), "profiles.yaml") || !strings.Contains(err.Error(), "review") {
		t.Fatalf("selection = %v, err = %v; want a refusal naming the file and the profile", sel, err)
	}
	if sel != nil {
		t.Errorf("nothing should be selected, got %v", sel)
	}
	// The listing still works, with the catalog's profile and default.
	if rows := a.profileRows(); len(rows) != 2 || rows[0].Name != "review" || rows[0].Source != sourceCatalog {
		t.Errorf("rows = %+v", rows)
	}
}

// --all, like the picker's "a", leaves servers hidden with h alone: hidden
// means not wanted in this project, and a launch with --all would otherwise
// bring every one of them back with no list on screen to show it.
func TestAllSkipsHidden(t *testing.T) {
	a := testApp(t, options{uid: "u", all: true})
	if err := state.SaveHidden(a.root, map[string]bool{"browser": true}); err != nil {
		t.Fatal(err)
	}
	sel, err := a.selection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel["browser"] || !sel["github"] || !sel["sentry"] {
		t.Errorf("sel = %v", sel)
	}
}

// --all is built from the catalog, not from the saved selection: a server
// picked in an earlier session and hidden (or disabled in Claude Code) since
// must not ride along, or "never hidden" would hold only on a fresh uid.
// --profile default is --all and must behave the same through run().
func TestAllDropsSavedServersHiddenSince(t *testing.T) {
	a := testApp(t, options{uid: "u", all: true})
	if err := state.Save(a.root, "u", state.State{Selected: []string{"browser", "sentry", "github"}}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveHidden(a.root, map[string]bool{"browser": true}); err != nil {
		t.Fatal(err)
	}
	a.cat.Servers[1].Disabled = true // sentry, since the selection was saved
	sel, err := a.selection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel["browser"] || sel["sentry"] || !sel["github"] {
		t.Errorf("--all = %v; saved-then-hidden and saved-then-disabled servers must not stay selected", sel)
	}

	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte("servers:\n  a: {url: https://x/a}\n  b: {url: https://x/b}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := catalog.WorkspaceRoot(root)
	if err := state.Save(ws, "u", state.State{Selected: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveHidden(ws, map[string]bool{"b": true}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	app := &app{out: &out, errw: io.Discard, version: "test"}
	if err := app.run([]string{"--uid", "u", "--profile", "default", "--agent", "codex", "export"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[mcp_servers.a]") || strings.Contains(out.String(), "[mcp_servers.b]") {
		t.Errorf("--profile default exported a saved-then-hidden server:\n%s", out.String())
	}
}

// `profile list` (text and --json) and the `list` footer describe the
// built-in default with the rule --all launches by: not disabled in Claude
// Code, not hidden with h.
func TestProfileListDefaultSkipsHidden(t *testing.T) {
	a := testApp(t, options{uid: "u"})
	if err := state.SaveHidden(a.root, map[string]bool{"browser": true}); err != nil {
		t.Fatal(err)
	}
	a.cat.Servers[1].Disabled = true // sentry
	rows := a.profileRows()
	def := rows[len(rows)-1]
	if def.Name != profile.Default || def.Source != sourceBuiltin {
		t.Fatalf("last row = %+v, want the built-in default", def)
	}
	if strings.Join(def.Servers, ",") != "github" {
		t.Errorf("default = %v; a hidden or disabled server is not what --all launches", def.Servers)
	}
	var out strings.Builder
	a.out = &out
	if err := a.profileCmd([]string{"list"}); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, sourceBuiltin) && (strings.Contains(line, "browser") || strings.Contains(line, "sentry")) {
			t.Errorf("profile list describes default with a hidden or disabled server: %q", line)
		}
	}
	out.Reset()
	a.opt.jsonOut = true
	if err := a.profileCmd([]string{"list"}); err != nil {
		t.Fatal(err)
	}
	var got []profileInfo
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if def := got[len(got)-1]; def.Name != profile.Default || strings.Join(def.Servers, ",") != "github" {
		t.Errorf("--json describes default as %+v; want github alone", def)
	}
}

// A wrong value in config.yaml is said once on stderr and the command goes
// on with the default: a preference must never keep the agent from starting,
// and a silent default would leave the setting looking broken.
func TestConfigWarningIsPrintedNotFatal(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("MCPICK_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("max_rows: ten\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test"}
	if err := a.run([]string{"list"}); err != nil {
		t.Fatalf("list failed over a bad preference: %v", err)
	}
	if !strings.Contains(errw.String(), "config.yaml") || !strings.Contains(errw.String(), "max_rows") {
		t.Errorf("stderr = %q; the bad key should be named", errw.String())
	}
	if a.cfg.MaxRows != 10 {
		t.Errorf("max_rows = %d, want the default", a.cfg.MaxRows)
	}
}

// -y on a box or --uid that never saved a selection launches with no
// servers; that is allowed, but never silent. A selection saved empty on
// purpose is told apart from one that was never saved.
func TestLastWithNoSavedSelectionWarns(t *testing.T) {
	var errw strings.Builder
	a := testApp(t, options{uid: "fresh", last: true})
	a.errw = &errw
	sel, err := a.selection(&tui.Options{UID: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 0 {
		t.Errorf("sel = %v, want nothing", sel)
	}
	if !strings.Contains(errw.String(), "no saved selection") || !strings.Contains(errw.String(), `"fresh"`) {
		t.Errorf("stderr = %q; want a warning naming the uid", errw.String())
	}

	errw.Reset()
	if err := state.Save(a.root, "fresh", state.State{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.selection(&tui.Options{UID: "fresh"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "is empty") {
		t.Errorf("stderr = %q; an empty saved selection should be named as such", errw.String())
	}

	// With something saved, or with an explicit choice, nothing is said.
	errw.Reset()
	if err := state.Save(a.root, "fresh", state.State{Selected: []string{"browser"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.selection(&tui.Options{UID: "fresh"}); err != nil {
		t.Fatal(err)
	}
	a.opt = options{uid: "other", none: true}
	if _, err := a.selection(&tui.Options{UID: "other"}); err != nil {
		t.Fatal(err)
	}
	if errw.String() != "" {
		t.Errorf("unexpected warning: %q", errw.String())
	}
}

// `profile save` and `profile rename` apply the name rule: a pasted sentence
// is refused with the rule and nothing is written.
func TestProfileSaveAndRenameRefuseBadNames(t *testing.T) {
	a := testApp(t, options{uid: "u", file: "/src/app/.mcp.yaml", selects: "github"})
	personalProfiles(t, a, map[string][]string{"ok": {"github"}})
	before, err := os.ReadFile(a.prof.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"tigate Jira ticket https://x", "a/b", strings.Repeat("a", profile.MaxNameLen+1)} {
		err := a.profileCmd([]string{"save", bad})
		if err == nil || !strings.Contains(err.Error(), profile.NameRule) {
			t.Errorf("save %q: %v", bad, err)
		}
		err = a.profileCmd([]string{"rename", "ok", bad})
		if err == nil || !strings.Contains(err.Error(), profile.NameRule) {
			t.Errorf("rename to %q: %v", bad, err)
		}
	}
	if after, _ := os.ReadFile(a.prof.Path()); string(after) != string(before) {
		t.Errorf("the file changed:\n%s", after)
	}
	if err := a.profileCmd([]string{"save", "dev-2.x_y"}); err != nil {
		t.Errorf("a name within the rule: %v", err)
	}
}

// `mcpick profile delete NAME` removes a profile only a catalog carries from
// that catalog file; the typed name is the confirmation.
func TestProfileDeleteRemovesACatalogProfile(t *testing.T) {
	a := testApp(t, options{uid: "u"})
	path := filepath.Join(t.TempDir(), ".mcp.yaml")
	if err := os.WriteFile(path, []byte("servers:\n  a: {type: http, url: \"https://a\"}\nprofiles:\n  stray: []\n  keep: [a]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cat.Profiles = map[string][]string{"stray": {}, "keep": {"a"}}
	a.cat.ProfileFiles = map[string]string{"stray": path, "keep": path}
	if err := a.profileCmd([]string{"delete", "stray"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "stray") || !strings.Contains(string(data), "keep: [a]") {
		t.Errorf("catalog after delete:\n%s", data)
	}
}

// restore reads records from the home and writes project files from them,
// so it runs behind the same check on the home as every other command: a
// home that is a symlink — one planted to point mcpick at records of
// someone's choosing — is refused before anything is read.
func TestRestoreRefusesAPlantedHome(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	record := filepath.Join(real, "state", "restore")
	if err := os.MkdirAll(record, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCPICK_HOME", link)
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test"}
	err := a.run([]string{"restore"})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("restore = %v, out %q; a symlinked home must be refused", err, out.String())
	}
	if out.Len() != 0 {
		t.Errorf("restore printed %q before the check", out.String())
	}
	// agents (and targets, its 0.1.0 name) writes nothing and stays available.
	for _, cmd := range []string{"agents", "targets"} {
		if err := a.run([]string{cmd}); err != nil {
			t.Errorf("%s = %v", cmd, err)
		}
	}
}

// One of mcpick's own flags after `run <cmd>` belongs to the agent, as
// everything after run does; it is passed through as it is and said, so
// `mcpick run claude --select x` does not look like a selection that was
// silently ignored. -y, --yes and --json are many agents' own and are not
// reported.
func TestParseArgsReportsMcpickFlagsAfterRun(t *testing.T) {
	opt, cmd, rest, err := parseArgs([]string{"run", "claude", "--select", "x", "--all", "--select=y", "--json", "-y"})
	if err != nil || cmd != "run" {
		t.Fatalf("cmd = %q, err = %v", cmd, err)
	}
	if strings.Join(rest, " ") != "claude --select x --all --select=y --json -y" {
		t.Errorf("rest = %v; the flags must still go to the agent", rest)
	}
	if strings.Join(opt.late, " ") != "--select --all" {
		t.Errorf("late = %v, want --select and --all once each", opt.late)
	}
	if opt.selects != "" || opt.all || opt.last {
		t.Error("flags after the command were consumed")
	}
	if got := lateFlagNote("--select", "/usr/local/bin/claude"); got != `--select after "claude" goes to claude; mcpick options go before run` {
		t.Errorf("note = %q", got)
	}
	if opt, _, _, err := parseArgs([]string{"run", "claude"}); err != nil || len(opt.late) != 0 {
		t.Errorf("run with no agent arguments: late = %v, err = %v", opt.late, err)
	}
}

// What is not a misplaced mcpick option is not reported: anything after
// `--`, the value of a flag that takes one (a prompt that reads --all),
// and the agent's own flags of the same name — claude's --agent, codex's
// --profile.
func TestLateFlagsKnowsTheAgentsOwn(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "claude", "--agent", "reviewer", "-p", "--all of it", "--", "--select", "x"}, ""},
		{[]string{"run", "claude", "--select", "--all", "--none"}, "--select --none"},
		{[]string{"run", "codex", "--profile", "work", "-p", "--none"}, ""},
		{[]string{"run", "codex", "exec", "--select=a", "--timeout", "--home"}, "--select --timeout"},
		{[]string{"run", "gemini", "--profile", "x"}, "--profile"},
		{[]string{"--agent", "codex", "run", "my-codex", "--profile", "x"}, ""},
	} {
		opt, _, _, err := parseArgs(tc.args)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := strings.Join(opt.late, " "); got != tc.want {
			t.Errorf("%v: late = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// --all, --none, --select, --profile and -y each say what to select; two of
// them at once are refused rather than ranked.
func TestParseArgsRefusesConflictingSelectors(t *testing.T) {
	for _, args := range [][]string{
		{"--all", "--select", "a", "list"},
		{"-y", "--profile", "x", "run", "claude"},
		{"--none", "--all", "export"},
		{"--profile", "x", "--last", "doctor"},
	} {
		if _, _, _, err := parseArgs(args); !errors.Is(err, errExclusive) {
			t.Errorf("%v: err = %v, want the exclusive-flags error", args, err)
		}
	}
	for _, args := range [][]string{{"--all", "list"}, {"--select", "a", "run", "claude", "--all"}, {"-y", "run", "claude"}} {
		if _, _, _, err := parseArgs(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// --select with a name the catalog does not have suggests the closest
// names, so the fix is in the error.
func TestSelectSuggestsClosestNames(t *testing.T) {
	a := testApp(t, options{uid: "uid"})
	_, err := a.parseSelect("githb")
	if err == nil || err.Error() != `no server named "githb" in the catalog (did you mean github?)` {
		t.Errorf("err = %v", err)
	}
	_, err = a.parseSelect("sen")
	if err == nil || !strings.Contains(err.Error(), "did you mean sentry?") {
		t.Errorf("a prefix should be suggested: %v", err)
	}
	_, err = a.parseSelect("zzzzzz")
	if err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("nothing is close to zzzzzz: %v", err)
	}
}

// `mcpick run cladue` names the agent it looks like, before the picker
// opens or anything is written; the old answer was "no adapter" after a
// screenful of picking. A flag of mcpick's after the command is reported
// on the way.
func TestRunRefusesAMistypedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup differs on Windows")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var errw strings.Builder
	a := &app{out: io.Discard, errw: &errw, version: "test"}
	err := a.run([]string{"--none", "run", "cladue", "--select", "x"})
	if err == nil || err.Error() != "cladue: command not found (did you mean claude?)" {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(errw.String(), `mcpick: --select after "cladue" goes to cladue; mcpick options go before run`) {
		t.Errorf("stderr = %q; the late flag should be reported", errw.String())
	}
	if err := a.run([]string{"--none", "run", "nothing-like-it"}); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("err = %v; nothing is close", err)
	}
}

// --redact and --yes answer the credentials question of import and move in
// opposite ways; both at once is refused, not resolved by precedence.
func TestParseArgsRefusesRedactWithYes(t *testing.T) {
	for _, args := range [][]string{
		{"import", "--redact", "--yes"},
		{"--yes", "move", "gh", "project", "--redact"},
	} {
		if _, _, _, err := parseArgs(args); !errors.Is(err, errRedactYes) {
			t.Errorf("%v: err = %v, want the contradiction refused", args, err)
		}
	}
	for _, args := range [][]string{{"import", "--yes"}, {"move", "gh", "project", "--redact"}} {
		if _, _, _, err := parseArgs(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// The shell completions ask `mcpick __complete servers` for names: one per
// line, nothing else — not `list`'s headings and columns — and no name a
// shell would read as something else. The command is not in the usage.
func TestCompleteServers(t *testing.T) {
	throwawayWorkspace(t)
	cat := "servers:\n  github: {type: http, url: https://x/gh}\n  my.srv-2: {command: echo}\n" +
		"  \"two words\": {command: echo}\n  \"a;rm\": {command: echo}\n  \"-flag\": {command: echo}\n  \"esc\\x1b\": {command: echo}\n"
	if err := os.WriteFile(".mcp.yaml", []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errw, code := runMain(t, "__complete", "servers")
	if code != 0 {
		t.Fatalf("__complete = %d %q", code, errw)
	}
	if out != "github\nmy.srv-2\n" {
		t.Errorf("__complete servers = %q, want the plain names only", out)
	}
	if strings.Contains(usage, "__complete") {
		t.Error("__complete is the completions' own; it has no place in the usage")
	}
}

// Each shell's completion takes server names from __complete, never from
// `list`, whose first column is headings and group labels.
func TestCompletionsAskForServerNames(t *testing.T) {
	for _, f := range []string{"mcpick.bash", "_mcpick", "mcpick.fish"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "completions", f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "mcpick __complete servers") || strings.Contains(string(data), "mcpick list") {
			t.Errorf("%s does not complete server names from `mcpick __complete servers`", f)
		}
	}
}

// `mcpick run --all claude` is mcpick's option typed after run: said so,
// not looked up as a command named --all.
func TestRunWithAnOptionFirst(t *testing.T) {
	_, _, _, err := parseArgs([]string{"run", "--all", "claude"})
	if err == nil || !strings.Contains(err.Error(), "--all after run: mcpick options go before run") {
		t.Errorf("err = %v", err)
	}
}

// list's endpoint column and the --trust notice go through spec.MaskText:
// a URL password, a secret query value, a Bearer token written in the
// catalog are not printed.
func TestListAndTrustNoticeMaskCredentials(t *testing.T) {
	throwawayWorkspace(t)
	cat := "servers:\n  r: {type: http, url: \"https://u:urlpw999@x.example.com/mcp?token=qtok999\"}\n" +
		"  s: {command: mcpick-test-no-such-command, args: [\"postgres://u:dbpw999@db/x\", \"Bearer argtok99999\"]}\n"
	if err := os.WriteFile(".mcp.yaml", []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"list"}, {"--json", "list"}, {"--select", "s", "--trust", "--timeout", "2s", "measure"}} {
		out, errw, _ := runMain(t, args...)
		for _, secret := range []string{"urlpw999", "qtok999", "dbpw999", "argtok99999"} {
			if strings.Contains(out+errw, secret) {
				t.Errorf("%v printed %s:\n%s%s", args, secret, out, errw)
			}
		}
	}
}
