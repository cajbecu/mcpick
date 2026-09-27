package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/spec"
)

// A project file mcpick creates holds the selection expanded, secrets
// included, in a directory that is usually a git checkout: it is created
// 0600, and a directory created for it 0700, like a rendered config.
func TestProjectFileCreatedPrivate(t *testing.T) {
	if !unixPerms {
		t.Skip("mode bits mean nothing here")
	}
	ctx := testCtx(t)
	p := projectBackend{Meta: Meta{Name: "devin"}, file: ".devin/mcp_config.json", topKey: "mcpServers", dialect: spec.Claude}
	plan, err := p.Plan(ctx, remoteSel(), []string{"devin"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	path := filepath.Join(ctx.Root, ".devin", "mcp_config.json")
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v (%v), want 0600", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v (%v), want 0700", fi.Mode().Perm(), err)
	}
	for _, n := range plan.Notes {
		if strings.Contains(n, "readable by others") {
			t.Errorf("a file mcpick created 0600 should not be warned about: %s", n)
		}
	}
}

// secretSel is a selection whose catalog text carries a ${TOKEN} reference
// in a header, expanded for the run.
func secretSel() spec.Selection {
	sel := oneSel("srv", map[string]any{"type": "http", "url": "https://example.com/mcp",
		"headers": map[string]any{"Authorization": "Bearer hunter2"}})
	sel.Raw = map[string]map[string]any{"srv": {"type": "http", "url": "https://example.com/mcp",
		"headers": map[string]any{"Authorization": "Bearer ${TOKEN}"}}}
	return sel
}

// A file that exists and that others can read is 0600 while it holds the
// run's servers — they may carry expanded secrets — and the user is told;
// its own mode comes back with its contents on exit. A file nobody else
// can read is left as it is, without a word.
func TestProjectFileIsPrivateForTheRun(t *testing.T) {
	if !unixPerms {
		t.Skip("mode bits mean nothing here")
	}
	p := projectBackend{Meta: Meta{Name: "devin"}, file: ".devin/mcp_config.json", topKey: "mcpServers", dialect: spec.Claude}
	warned := func(plan Plan) bool {
		for _, n := range plan.Notes {
			if strings.Contains(n, "readable by others") {
				return true
			}
		}
		return false
	}

	ctx := testCtx(t)
	path := filepath.Join(ctx.Root, ".devin", "mcp_config.json")
	write(t, path, `{"mcpServers":{}}`) // 0644
	plan, err := p.Plan(ctx, secretSel(), []string{"devin"})
	if err != nil {
		t.Fatal(err)
	}
	if !warned(plan) {
		t.Errorf("a 0644 file made private drew no note: %v", plan.Notes)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode during the run = %v, want 0600", fi.Mode().Perm())
	}
	plan.Cleanup()
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode after the run = %v, want the file's own 0644", fi.Mode().Perm())
	}

	// A session that dies without its cleanup: the next launch puts the
	// mode back with the contents.
	crashed := ctx
	crashed.PID = deadPID(t)
	if _, err := p.Plan(crashed, secretSel(), []string{"devin"}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode during the run = %v, want 0600", fi.Mode().Perm())
	}
	if notes := RecoverStale(ctx.State, false); len(notes) == 0 {
		t.Fatal("nothing was recovered")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode after recovery = %v, want the file's own 0644", fi.Mode().Perm())
	}

	// Private already.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err = p.Plan(ctx, secretSel(), []string{"devin"})
	if err != nil {
		t.Fatal(err)
	}
	if warned(plan) {
		t.Errorf("a 0600 file drew a warning: %v", plan.Notes)
	}
	plan.Cleanup()
}

// envSel is a stdio server whose env carries every reference form, and a
// remote one with a reference in a header.
func envSel() spec.Selection {
	sel := oneSel("gh", map[string]any{"command": "npx", "args": []any{"-y", "@modelcontextprotocol/server-github"},
		"env": map[string]any{
			"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_secret", "MIXED": "pre-a-post", "DEF": "d",
			"ESC": "${C}", "UUID": "uid-1", "BARE": "$D", "PLAIN": "x",
		}})
	sel.Names = append(sel.Names, "api")
	sel.Specs["api"] = map[string]any{"type": "http", "url": "https://example.com/mcp",
		"headers": map[string]any{"Authorization": "Bearer hunter2"}}
	sel.Raw = map[string]map[string]any{
		"gh": {"command": "npx", "args": []any{"-y", "@modelcontextprotocol/server-github"},
			"env": map[string]any{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "${GH}", "MIXED": "pre-${A}-post", "DEF": "${B:-d}",
				"ESC": "$${C}", "UUID": "{UUID}", "BARE": "$D", "PLAIN": "x",
			}},
		"api": {"type": "http", "url": "https://example.com/mcp",
			"headers": map[string]any{"Authorization": "Bearer ${T}"}},
	}
	return sel
}

func writtenEnv(t *testing.T, path string) (env map[string]any, headers map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]map[string]map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	env, _ = top["mcpServers"]["gh"]["env"].(map[string]any)
	headers, _ = top["mcpServers"]["api"]["headers"].(map[string]any)
	return env, headers
}

// Gemini expands ${VAR} in the env block itself, so a reference it can
// expand exactly as mcpick would is written as the catalog wrote it and
// the token stays out of settings.json. `${NAME:?why}` and
// `${NAME:-default}` — what import writes — are written as `${NAME}` once
// NAME is set. A default in use, an escape, {UUID} and every other key
// stay expanded.
func TestGeminiKeepsEnvReferences(t *testing.T) {
	ctx := testCtx(t)
	t.Setenv("MCPICK_TEST_REQ", "req-secret")
	t.Setenv("MCPICK_TEST_DEFSET", "defset-secret")
	t.Setenv("MCPICK_TEST_EMPTY", "")
	sel := envSel()
	gh := sel.Specs["gh"]["env"].(map[string]any)
	rawGH := sel.Raw["gh"]["env"].(map[string]any)
	for k, v := range map[string][2]string{
		"REQ":    {"${MCPICK_TEST_REQ:?export MCPICK_TEST_REQ}", "req-secret"},
		"DEFSET": {"x-${MCPICK_TEST_DEFSET:-d}", "x-defset-secret"},
		"DEFEMP": {"${MCPICK_TEST_EMPTY:-fallback}", "fallback"},
	} {
		rawGH[k], gh[k] = v[0], v[1]
	}
	plan, err := byName["gemini"].Plan(ctx, sel, []string{"gemini"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	path := filepath.Join(ctx.Root, ".gemini", "settings.json")
	env, headers := writtenEnv(t, path)
	for k, want := range map[string]string{
		"GITHUB_PERSONAL_ACCESS_TOKEN": "${GH}", "MIXED": "pre-${A}-post",
		"DEF": "d", "ESC": "${C}", "UUID": "uid-1", "BARE": "$D", "PLAIN": "x",
		"REQ": "${MCPICK_TEST_REQ}", "DEFSET": "x-${MCPICK_TEST_DEFSET}", "DEFEMP": "fallback",
	} {
		if env[k] != want {
			t.Errorf("env %s = %v, want %q", k, env[k], want)
		}
	}
	if headers["Authorization"] != "Bearer hunter2" {
		t.Errorf("Authorization = %v; Gemini does not expand headers, so it must be written expanded", headers["Authorization"])
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "ghp_secret") {
		t.Errorf("the token behind ${GH} reached the project file:\n%s", data)
	}
	// The caller's selection is left as it was: other agents launched from
	// it need the expanded values.
	if got := sel.Specs["gh"]["env"].(map[string]any)["GITHUB_PERSONAL_ACCESS_TOKEN"]; got != "ghp_secret" {
		t.Errorf("the caller's selection was changed: %v", got)
	}
}

// An agent that does not expand references gets the values.
func TestProjectFileWritesExpandedEnvWithoutEnvRefs(t *testing.T) {
	ctx := testCtx(t)
	plan, err := byName["devin"].Plan(ctx, envSel(), []string{"devin"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	env, _ := writtenEnv(t, filepath.Join(ctx.Root, ".devin", "mcp_config.json"))
	if env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "ghp_secret" || env["MIXED"] != "pre-a-post" {
		t.Errorf("env = %v, want every value expanded", env)
	}
}

// Without Raw nothing is known about what was expanded: the values are
// written as given.
func TestKeepEnvRefsWithoutRaw(t *testing.T) {
	sel := oneSel("gh", map[string]any{"command": "x", "env": map[string]any{"A": "${A}"}})
	if got := keepEnvRefs(sel); got.Specs["gh"]["env"].(map[string]any)["A"] != "${A}" {
		t.Errorf("keepEnvRefs without Raw changed the value: %v", got.Specs)
	}
}
