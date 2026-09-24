package target

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/spec"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func oneSel(name string, sp map[string]any) spec.Selection {
	return spec.Selection{Names: []string{name}, Specs: map[string]map[string]any{name: sp}}
}

func testCtx(t *testing.T) Ctx {
	t.Helper()
	return Ctx{
		UID:     "uid-1",
		Root:    t.TempDir(),
		Runtime: t.TempDir(),
		State:   t.TempDir(),
		PID:     os.Getpid(),
	}
}

func remoteSel() spec.Selection {
	return oneSel("srv", map[string]any{"type": "http", "url": "https://example.com/mcp"})
}

func TestPickTargetFromCommandName(t *testing.T) {
	for _, tc := range []struct{ argv0, want string }{
		{"claude", "claude"},
		{"/usr/local/bin/codex", "codex"},
		{"codex.exe", "codex"},
		{"agy", "antigravity"},
		{"something-else", "generic"},
	} {
		got, err := Pick("", []string{tc.argv0})
		if err != nil {
			t.Fatal(err)
		}
		if got.Info().Name != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.argv0, got.Info().Name, tc.want)
		}
	}
}

func TestPickTargetExplicitUnknown(t *testing.T) {
	if _, err := Pick("nope", []string{"claude"}); err == nil {
		t.Fatal("an unknown --target must be an error, not a silent fallback")
	}
}

func TestClaudePlanInjectsFlags(t *testing.T) {
	ctx := testCtx(t)
	plan, err := claudeTarget{}.Plan(ctx, remoteSel(), []string{"claude", "--dangerously-skip-permissions"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mutates() {
		t.Error("the claude target writes nothing it has to undo")
	}
	joined := strings.Join(plan.Argv, " ")
	if !strings.Contains(joined, "--mcp-config") || !strings.Contains(joined, "--strict-mcp-config") {
		t.Fatalf("argv = %v", plan.Argv)
	}
	if plan.Argv[len(plan.Argv)-1] != "--dangerously-skip-permissions" {
		t.Errorf("user flags must stay after mcpick's: %v", plan.Argv)
	}
}

// `claude mcp list` ignores --mcp-config anyway, and injecting flags before a
// subcommand changes what runs.
func TestClaudePlanSkipsSubcommands(t *testing.T) {
	ctx := testCtx(t)
	plan, err := claudeTarget{}.Plan(ctx, remoteSel(), []string{"claude", "mcp", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(plan.Argv, " "), "--mcp-config") {
		t.Errorf("flags must not be injected in front of a subcommand: %v", plan.Argv)
	}
	if len(plan.Notes) == 0 {
		t.Error("the user should be told why")
	}
}

func TestRenderedConfigIsPrivate(t *testing.T) {
	ctx := testCtx(t)
	plan, err := claudeTarget{}.Plan(ctx, remoteSel(), []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, e := range plan.Env {
		if p, ok := strings.CutPrefix(e, "MCPICK_CONFIG="); ok {
			path = p
		}
	}
	if path == "" {
		t.Fatal("MCPICK_CONFIG was not set")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file holds expanded credentials.
	if unixPerms && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

// The overlay is the mechanism that lets mcpick redirect CODEX_HOME without the
// child losing its credentials: everything but the config file is a symlink
// back to the real directory.
func TestOverlayKeepsSiblingsReachable(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "auth.json"), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config.toml"), []byte("model = \"a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "overlay")
	if err := overlay(src, dst, "config.toml", []byte("model = \"b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	auth, err := os.ReadFile(filepath.Join(dst, "auth.json"))
	if err != nil {
		t.Fatalf("credentials are not reachable through the overlay: %v", err)
	}
	if string(auth) != `{"token":"x"}` {
		t.Errorf("auth.json = %s", auth)
	}
	if fi, err := os.Lstat(filepath.Join(dst, "sessions")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("sibling directories should be symlinked, not copied")
	}
	cfg, err := os.ReadFile(filepath.Join(dst, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg) != "model = \"b\"\n" {
		t.Errorf("config.toml = %s, want the generated one", cfg)
	}
	// The real file is untouched.
	orig, err := os.ReadFile(filepath.Join(src, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != "model = \"a\"\n" {
		t.Errorf("the source config was modified: %s", orig)
	}
}

func TestOverlayNestedPath(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "opencode", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "overlay")
	if err := overlay(src, dst, "opencode/opencode.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(dst, "gh")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("unrelated XDG config dirs must be symlinked through")
	}
	if _, err := os.Stat(filepath.Join(dst, "opencode", "auth.json")); err != nil {
		t.Errorf("the tool's own credentials must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "opencode", "opencode.json")); err != nil {
		t.Errorf("the generated config is missing: %v", err)
	}
}

func TestHomeTargetPlanMergesRealConfig(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "config.toml"),
		[]byte("model = \"gpt-5\"\n\n[mcp_servers.stale]\ncommand = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := homeTarget{Meta: Meta{Name: "codex"}, env: "CODEX_HOME", src: src, rel: "config.toml", toml: true, emitFn: spec.EmitCodex}

	ctx := testCtx(t)
	plan, err := h.Plan(ctx, remoteSel(), []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Mutates() {
		t.Error("an overlay needs a cleanup: files the agent writes into it must be carried back")
	}
	defer plan.Cleanup()

	var dir string
	for _, e := range plan.Env {
		if p, ok := strings.CutPrefix(e, "CODEX_HOME="); ok {
			dir = p
		}
	}
	if dir == "" {
		t.Fatal("CODEX_HOME was not set")
	}
	body, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `model = "gpt-5"`) {
		t.Errorf("the user's settings were dropped:\n%s", body)
	}
	if strings.Contains(string(body), "stale") {
		t.Errorf("the old server block should be replaced:\n%s", body)
	}
	if !strings.Contains(string(body), "[mcp_servers.srv]") {
		t.Errorf("the selection is missing:\n%s", body)
	}
}

func TestProjectTargetRewritesAndRestores(t *testing.T) {
	ctx := testCtx(t)
	path := filepath.Join(ctx.Root, ".gemini", "settings.json")
	write(t, path, `{"theme":"dark","mcpServers":{"old":{"httpUrl":"https://x/old"}}}`)

	p := projectTarget{Meta: Meta{Name: "gemini"}, rel: ".gemini/settings.json", topKey: "mcpServers", emitFn: spec.EmitGemini}
	plan, err := p.Plan(ctx, remoteSel(), []string{"gemini"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Mutates() {
		t.Fatal("rewriting a project file must come with an undo")
	}

	during, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(during), "srv") || !strings.Contains(string(during), "dark") {
		t.Errorf("during the run the file should hold the selection and the user's settings:\n%s", during)
	}

	plan.Cleanup()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "srv") || !strings.Contains(string(after), "old") {
		t.Errorf("the original was not restored:\n%s", after)
	}
}

func TestProjectTargetRemovesFileItCreated(t *testing.T) {
	ctx := testCtx(t)
	p := projectTarget{Meta: Meta{Name: "devin"}, rel: ".devin/config.json", topKey: "mcpServers", emitFn: spec.EmitClaude}
	plan, err := p.Plan(ctx, remoteSel(), []string{"devin"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ctx.Root, ".devin", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the config should exist during the run: %v", err)
	}
	plan.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a file mcpick created must not be left behind")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("the .devin directory mcpick created must not be left behind either")
	}
}

func TestGeminiPlanPassesAllowList(t *testing.T) {
	ctx := testCtx(t)
	var gemini projectTarget
	for _, tgt := range All() {
		if p, ok := tgt.(projectTarget); ok && p.Name == "gemini" {
			gemini = p
		}
	}
	plan, err := gemini.Plan(ctx, remoteSel(), []string{"gemini", "--yolo"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	joined := strings.Join(plan.Argv, " ")
	if !strings.Contains(joined, "--allowed-mcp-server-names srv") {
		t.Errorf("argv = %v", plan.Argv)
	}
	if plan.Argv[len(plan.Argv)-1] != "--yolo" {
		t.Errorf("user flags must survive: %v", plan.Argv)
	}
}

func TestGenericTargetLeavesCommandAlone(t *testing.T) {
	ctx := testCtx(t)
	plan, err := genericTarget{}.Plan(ctx, remoteSel(), []string{"whatever", "--flag"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 2 || plan.Argv[1] != "--flag" {
		t.Errorf("argv = %v, want it untouched", plan.Argv)
	}
}

func TestEveryTargetHasASummary(t *testing.T) {
	for _, tgt := range All() {
		if tgt.Info().Summary == "" {
			t.Errorf("%q has no summary", tgt.Info().Name)
		}
	}
}

func overlayEnv(t *testing.T, plan Plan, key string) string {
	t.Helper()
	for _, e := range plan.Env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v
		}
	}
	t.Fatalf("%s not set", key)
	return ""
}

// Agents replace files atomically: write a temp file, rename it over the old
// one. Inside the overlay that rename replaces the symlink, so without a sync
// back a refreshed login would vanish with the overlay and the user would be
// logged out on the next run.
func TestOverlaySyncsBackReplacedAndNewFiles(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "auth.json"), `{"token":"old"}`)
	write(t, filepath.Join(src, "config.toml"), "model = \"a\"\n")
	h := homeTarget{Meta: Meta{Name: "codex"}, env: "CODEX_HOME", src: src, rel: "config.toml", toml: true, emitFn: spec.EmitCodex}

	plan, err := h.Plan(testCtx(t), remoteSel(), []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	dir := overlayEnv(t, plan, "CODEX_HOME")

	// What an agent does during a session:
	tmp := filepath.Join(dir, "auth.json.tmp")
	write(t, tmp, `{"token":"refreshed"}`)
	if err := os.Rename(tmp, filepath.Join(dir, "auth.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "history.jsonl"), "{}\n")

	plan.Cleanup()

	if got, _ := os.ReadFile(filepath.Join(src, "auth.json")); string(got) != `{"token":"refreshed"}` {
		t.Errorf("auth.json = %s, want the refreshed token carried back", got)
	}
	if _, err := os.Stat(filepath.Join(src, "history.jsonl")); err != nil {
		t.Errorf("a file the agent created was lost: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the overlay should be removed once the session is over")
	}
	if got, _ := os.ReadFile(filepath.Join(src, "config.toml")); string(got) != "model = \"a\"\n" {
		t.Errorf("config.toml = %q, want it untouched when the agent did not edit it", got)
	}
}

// When the agent edits its own config — Codex records trusted projects there
// — the edit is kept, and only the server block returns to the user's.
func TestOverlayKeepsAgentEditsToConfig(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "config.toml"), "model = \"a\"\n\n[mcp_servers.mine]\ncommand = \"m\"\n")
	h := homeTarget{Meta: Meta{Name: "codex"}, env: "CODEX_HOME", src: src, rel: "config.toml", toml: true, emitFn: spec.EmitCodex}

	plan, err := h.Plan(testCtx(t), remoteSel(), []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(overlayEnv(t, plan, "CODEX_HOME"), "config.toml")
	body, _ := os.ReadFile(cfg)
	write(t, cfg, string(body)+"\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n")

	plan.Cleanup()

	got, _ := os.ReadFile(filepath.Join(src, "config.toml"))
	for _, want := range []string{`[projects."/w"]`, `[mcp_servers.mine]`, `model = "a"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("config.toml is missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "mcp_servers.srv") {
		t.Errorf("the generated server leaked into the real config:\n%s", got)
	}
}

// Two sessions rewriting the same project file would each restore the other's
// generated config as "the original".
func TestProjectTargetRefusesConcurrentSession(t *testing.T) {
	ctx := testCtx(t)
	p := projectTarget{Meta: Meta{Name: "gemini"}, rel: ".gemini/settings.json", topKey: "mcpServers", emitFn: spec.EmitGemini}
	first, err := p.Plan(ctx, remoteSel(), []string{"gemini"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cleanup()

	other := ctx
	other.PID = os.Getppid() // alive, and not us
	if _, err := p.Plan(other, remoteSel(), []string{"gemini"}); err == nil ||
		!strings.Contains(err.Error(), "in use") {
		t.Fatalf("err = %v, want the file reported as in use", err)
	}
}

// A session killed with SIGKILL never runs its cleanup; the next launch must
// put the user's file back before doing anything else.
func TestRecoverStaleRestoresAfterCrash(t *testing.T) {
	ctx := testCtx(t)
	ctx.PID = deadPID(t)
	path := filepath.Join(ctx.Root, ".gemini", "settings.json")
	write(t, path, `{"theme":"dark"}`)
	p := projectTarget{Meta: Meta{Name: "gemini"}, rel: ".gemini/settings.json", topKey: "mcpServers", emitFn: spec.EmitGemini}
	if _, err := p.Plan(ctx, remoteSel(), []string{"gemini"}); err != nil {
		t.Fatal(err)
	}
	// ...and the process dies here without calling Cleanup.

	notes := RecoverStale(ctx.State, false)
	if len(notes) == 0 {
		t.Fatal("nothing was recovered")
	}
	if got, _ := os.ReadFile(path); string(got) != `{"theme":"dark"}` {
		t.Errorf("settings.json = %s, want the original back", got)
	}
}

// Workspaces and home directories are shared between containers, whose pids
// mean nothing to each other. A launch must not "recover" a file another box
// is using right now.
func TestRecoverStaleLeavesOtherHostsAlone(t *testing.T) {
	ctx := testCtx(t)
	path := filepath.Join(ctx.Root, ".devin", "config.json")
	rec := fileRecord{Path: path, PID: 1, Host: "some-other-box", Ready: true, Written: "x"}
	if err := saveRecord(ctx.State, rec, nil); err != nil {
		t.Fatal(err)
	}
	write(t, path, `{"mcpServers":{"theirs":{}}}`)

	if notes := RecoverStale(ctx.State, false); len(notes) != 0 {
		t.Errorf("a launch touched another host's record: %v", notes)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "theirs") {
		t.Error("another box's live config was rewritten")
	}
	// An explicit `mcpick restore` is the user saying that session is gone.
	if notes := RecoverStale(ctx.State, true); len(notes) == 0 {
		t.Error("mcpick restore must act on other hosts' records")
	}
}

// A session that died before the original was stored never touched the file;
// recovering it must not either.
func TestRecordNotReadyLeavesFileAlone(t *testing.T) {
	ctx := testCtx(t)
	path := filepath.Join(ctx.Root, ".gemini", "settings.json")
	write(t, path, `{"mcpServers":{"mine":{}}}`)
	meta, _ := recordPaths(ctx.State, path)
	write(t, meta, `{"path":`+jsonStr(path)+`,"pid":1,"host":"`+hostname()+`","ready":false}`)

	if _, err := restoreProjectFile(ctx.State, path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != `{"mcpServers":{"mine":{}}}` {
		t.Errorf("an untouched file was rewritten: %s", got)
	}
}

func TestProjectRestoreKeepsEditsMadeDuringRun(t *testing.T) {
	ctx := testCtx(t)
	path := filepath.Join(ctx.Root, ".gemini", "settings.json")
	write(t, path, `{"theme":"dark","mcpServers":{"mine":{"httpUrl":"https://x/mine"}}}`)
	p := projectTarget{Meta: Meta{Name: "gemini"}, rel: ".gemini/settings.json", topKey: "mcpServers", emitFn: spec.EmitGemini}
	plan, err := p.Plan(ctx, remoteSel(), []string{"gemini"})
	if err != nil {
		t.Fatal(err)
	}
	// The user switches theme from inside gemini.
	body, _ := os.ReadFile(path)
	write(t, path, strings.Replace(string(body), `"dark"`, `"light"`, 1))

	plan.Cleanup()
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"light"`) || !strings.Contains(string(got), "mine") || strings.Contains(string(got), `"srv"`) {
		t.Errorf("settings.json = %s, want the edit kept and the servers restored", got)
	}
}

func TestLeaksReportsServersTheAgentLoadsAnyway(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"srv":{},"extra":{}}}`)
	in := Meta{Name: "grok", AlsoReads: []Source{{Path: "{root}/.mcp.json", Keys: []string{"mcpServers"}}}}
	got := Leaks(in, root, remoteSel())
	if len(got) != 1 || !strings.Contains(got[0], "extra") || strings.Contains(got[0], "srv,") {
		t.Errorf("leaks = %v, want one note naming extra only", got)
	}
}

// deadPID returns a pid that is certainly not running: a child that has
// already been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn a child: %v", err)
	}
	return cmd.Process.Pid
}

// Between one session creating its record and filling it in, a second session
// must not mistake the empty file for debris and delete the first one's claim.
func TestHalfWrittenRecordIsALiveClaim(t *testing.T) {
	ctx := testCtx(t)
	path := filepath.Join(ctx.Root, ".gemini", "settings.json")
	meta, _ := recordPaths(ctx.State, path)
	write(t, meta, "")
	p := projectTarget{Meta: Meta{Name: "gemini"}, rel: ".gemini/settings.json", topKey: "mcpServers", emitFn: spec.EmitGemini}
	if _, err := p.Plan(ctx, remoteSel(), []string{"gemini"}); err == nil {
		t.Fatal("a fresh, half-written record must be treated as someone else's claim")
	}
	if _, err := os.Stat(meta); err != nil {
		t.Error("the other session's record was deleted")
	}
}

// jsonStr quotes s as a JSON string. Test fixtures splice paths into JSON, and
// a Windows path's backslashes are escape sequences until they are quoted.
func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// unixPerms says whether mode bits mean anything here. Windows reports 0666 or
// 0777 whatever was asked for; access there is governed by per-user ACLs.
var unixPerms = runtime.GOOS != "windows"

// Every agent mcpick supports is recognisable in the picker, and the preview
// never touches the disk.
func TestEveryTargetHasABadgeAndAPreview(t *testing.T) {
	ctxRoot := t.TempDir()
	for _, tgt := range All() {
		in := tgt.Info()
		if in.Glyph == "" || in.Color == "" {
			t.Errorf("%s has no glyph or colour", in.Name)
		}
		pv := tgt.Preview(remoteSel(), []string{in.Name, "--flag"})
		if len(pv.Argv) == 0 || pv.Argv[len(pv.Argv)-1] != "--flag" {
			t.Errorf("%s preview lost the user's arguments: %v", in.Name, pv.Argv)
		}
	}
	if entries, _ := os.ReadDir(ctxRoot); len(entries) != 0 {
		t.Error("a preview wrote to disk")
	}
}
