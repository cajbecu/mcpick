package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/state"
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
	opt, cmd, _, err := parseArgs([]string{"--uid=box-2", "--target=codex", "--profile=review", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "list" {
		t.Fatalf("cmd = %q", cmd)
	}
	if opt.uid != "box-2" || opt.target != "codex" || opt.profile != "review" {
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
		Path:     "/workspace/.mcp.yaml",
		Profiles: map[string][]string{"review": {"ahrefs", "freshdesk"}},
		Servers: []catalog.Server{
			{Name: "ahrefs", Origin: catalog.OriginWorkspace, Spec: http("https://x/ahrefs")},
			{Name: "freshdesk", Origin: catalog.OriginGlobal, Spec: http("https://x/freshdesk")},
			{Name: "neo", Origin: catalog.OriginGlobal, Spec: http("http://127.0.0.1:9010/mcp")},
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
	if err := state.Save(a.root, "uid", state.State{Selected: []string{"neo"}}); err != nil {
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
	check(options{selects: "ahrefs", profile: "review", all: true}, "ahrefs") // --select beats all
	check(options{profile: "review", all: true}, "ahrefs", "freshdesk")       // then --profile
	check(options{all: true}, "ahrefs", "freshdesk", "neo")                   // then --all
	check(options{none: true})                                                // --none clears
	check(options{last: true}, "neo")                                         // the saved selection
	check(options{}, "neo")                                                   // no terminal: saved too
}

func TestSelectionRejectsUnknownNames(t *testing.T) {
	a := testApp(t, options{uid: "uid", selects: "ahrefs,ghost"})
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

	if out, code := run("list"); code != 0 || !strings.Contains(out, "workspace") || !strings.Contains(out, "uvx thing") {
		t.Fatalf("list = %d %q", code, out)
	}
	if out, code := run("--select", "a", "profile", "save", "one"); code != 0 {
		t.Fatalf("profile save = %d %q", code, out)
	}
	if out, _ := run("profile"); !strings.Contains(out, "one") {
		t.Errorf("profile list = %q", out)
	}
	if out, code := run("--profile", "one", "--target", "codex", "export"); code != 0 || !strings.Contains(out, "[mcp_servers.a]") {
		t.Errorf("export = %d %q", code, out)
	}
	if out, code := run("profile", "delete", "one"); code != 0 {
		t.Errorf("profile delete = %d %q", code, out)
	}
	if _, code := run("profile", "delete", "one"); code == 0 {
		t.Error("deleting a missing profile must fail")
	}
	if _, code := run("frobnicate"); code == 0 {
		t.Error("an unknown command must fail")
	}
}

// [~]: a server disabled in Claude Code runs for the session under a name
// Claude's disabled list does not contain; a plugin server keeps its name,
// because Claude disables it under the namespaced one.
func TestSessionAliasesForDisabledServers(t *testing.T) {
	a := testApp(t, options{uid: "u"})
	a.cat.Servers = append(a.cat.Servers,
		catalog.Server{Name: "design", Disabled: true, DisabledAs: "design", Spec: map[string]any{"url": "https://d"}},
		catalog.Server{Name: "design_mcpick", Spec: map[string]any{"url": "https://taken"}},
		catalog.Server{Name: "cf-api", Origin: catalog.OriginPlugin, Disabled: true, DisabledAs: "plugin:cloudflare:cf-api", Spec: map[string]any{"url": "https://c"}},
	)
	sel := spec.Selection{Names: []string{"design", "cf-api", "ahrefs"}, Specs: map[string]map[string]any{
		"design": {"url": "https://d"}, "cf-api": {"url": "https://c"}, "ahrefs": {"url": "https://a"},
	}}
	a.sessionAliases(&sel)
	if strings.Join(sel.Names, ",") != "design_mcpick2,cf-api,ahrefs" {
		t.Errorf("names = %v; the alias must avoid a name already in the catalog", sel.Names)
	}
	if sel.Specs["design_mcpick2"]["url"] != "https://d" || sel.Specs["design"] != nil {
		t.Errorf("specs = %v", sel.Specs)
	}
}

// [x]: the entry is removed from Claude Code's disabled list for good, and the
// server is then launched under its own name.
func TestEnableInClaudeFromPicker(t *testing.T) {
	a := testApp(t, options{uid: "u"})
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"projects":{"/w":{"disabledMcpServers":["design","other"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cat.ClaudePath, a.cat.ProjectKey = path, "/w"
	a.cat.Servers = append(a.cat.Servers, catalog.Server{Name: "design", Disabled: true, DisabledAs: "design"})
	a.reenable = map[string]bool{"design": true}
	if err := a.enableInClaude(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), `"design"`) || !strings.Contains(string(body), `"other"`) {
		t.Errorf(".claude.json = %s", body)
	}
	sel := spec.Selection{Names: []string{"design"}, Specs: map[string]map[string]any{"design": {}}}
	a.sessionAliases(&sel)
	if sel.Names[0] != "design" {
		t.Error("a re-enabled server needs no alias")
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
	if sel["off"] || !sel["ahrefs"] {
		t.Errorf("sel = %v", sel)
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
