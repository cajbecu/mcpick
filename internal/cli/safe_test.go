package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// hostile is what a catalog or a server can put in text that reaches the
// terminal: OSC 52 (writes the clipboard), ESC[2J (clears the screen), the
// same CSI as one C1 byte in UTF-8, and a right-to-left override.
const hostile = "\x1b]52;c;aGk=\x07\x1b[2J\u009b2J\u202e"

// spelled is how hostile must look once printed: every escape as text.
var spelled = []string{`\x1b]52;c;aGk=\a`, `\x1b[2J`, `\u009b2J`, `\u202e`}

func hasRawEscape(s string) bool { return strings.ContainsAny(s, "\x1b\u009b\u202e\x07") }

// hostileWorkspace is a project whose catalog carries terminal escapes in a
// server name, an address, a profile entry and a placeholder's reason, and
// whose one reachable server answers 500 with escapes in the body. It
// returns a runner that captures both streams and the exit status.
func hostileWorkspace(t *testing.T) (root string, run func(args ...string) (out, errw string, code int)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom "+hostile, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	root = t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	if err := os.WriteFile(filepath.Join(claude, ".claude.json"), []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// YAML double-quoted scalars take Go's escapes (\x1b, \a, \u009b), and
	// the parser refuses a raw C1 byte in the stream, so the text is
	// written escaped; what it parses to is the raw text.
	q := strconv.QuoteToASCII
	cat := "servers:\n" +
		"  " + q("ev"+hostile+"il") + ": {type: http, url: " + srv.URL + "}\n" +
		"  plain: {type: http, url: " + q("https://example.com/"+hostile) + "}\n" +
		"  needs: {type: http, url: https://example.com/mcp, headers: {Authorization: " + q("Bearer ${MCPICK_TEST_UNSET:?set it "+hostile+"}") + "}}\n" +
		"profiles:\n  review: [" + q("mis"+hostile+"sing") + "]\n"
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	run = func(args ...string) (string, string, int) {
		t.Helper()
		var out, errw strings.Builder
		a := &app{out: &out, errw: &errw, version: "test"}
		code := a.exit(a.run(args))
		return out.String(), errw.String(), code
	}
	return root, run
}

// Every line the command line prints from the catalog or a server goes
// through trust.Safe: list, doctor, measure, profile, move, the catalog
// warnings and the top-level error. The JSON forms stay encoded, with the
// characters encoding/json leaves raw escaped (trust.SafeJSON).
func TestCommandOutputNeutralisesTerminalEscapes(t *testing.T) {
	_, run := hostileWorkspace(t)
	check := func(t *testing.T, which, text string, want ...string) {
		t.Helper()
		if hasRawEscape(text) {
			t.Errorf("%s carries a raw terminal escape:\n%q", which, text)
		}
		for _, w := range want {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q:\n%s", which, w, text)
			}
		}
	}

	out, errw, code := run("list")
	if code != 0 {
		t.Fatalf("list = %d\n%s%s", code, out, errw)
	}
	check(t, "list stdout", out, spelled...)
	check(t, "list stdout", out, "ev"+`\x1b]52`, "example.com/"+`\x1b]52`, "mis"+`\x1b]52`)
	check(t, "list stderr (catalog warning)", errw, "profile review lists mis"+`\x1b]52`)

	out, errw, _ = run("profile")
	check(t, "profile list", out+errw, "mis"+`\x1b]52`)

	out, errw, _ = run("--timeout", "5s", "--select", "ev"+hostile+"il", "doctor")
	check(t, "doctor stdout", out, spelled...)
	check(t, "doctor stdout", out, "FAIL  ev"+`\x1b]52`, "HTTP 500: boom "+`\x1b]52`)
	check(t, "doctor stderr", errw)

	// --select is a check on the repository's server; --all is not, and
	// measure would skip it unchecked (its address is private).
	out, errw, _ = run("--timeout", "5s", "--select", "ev"+hostile+"il", "measure")
	check(t, "measure stdout", out, "ev"+`\x1b]52`, "HTTP 500: boom "+`\x1b]52`)
	check(t, "measure stderr", errw)

	// A placeholder's reason is the catalog's text and ends in the error
	// that stops the launch.
	out, errw, code = run("--select", "needs", "--agent", "codex", "export")
	if code != 1 {
		t.Fatalf("export with an unset ${VAR:?} = %d\n%s%s", code, out, errw)
	}
	check(t, "top-level error", errw, "mcpick: needs: MCPICK_TEST_UNSET is required: set it "+`\x1b]52`)

	out, errw, code = run("move", "ev"+hostile+"il", "local")
	if code != 0 {
		t.Fatalf("move = %d\n%s%s", code, out, errw)
	}
	check(t, "move stdout", out, "ev"+`\x1b]52`)
	check(t, "move stderr", errw)

	// --json is encoded, which is its own escaping, and what encoding/json
	// leaves raw — C1 controls, bidi overrides — is escaped too; a consumer
	// decodes it to the text as it is.
	out, _, _ = run("--json", "list")
	check(t, "list --json", out)
	var rows []struct{ Name, Endpoint string }
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	found := false
	for _, r := range rows {
		found = found || r.Name == "ev"+hostile+"il"
	}
	if !found {
		t.Errorf("list --json must keep the text as it is:\n%s", out)
	}
}

// A transport error names the URL the request went to. Expanded, that URL
// carries `?key=${K}` as the value; measure and doctor show it as the
// catalog wrote it, in the text and in --json alike.
func TestMeasureErrorsShowURLAsWritten(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MCPICK_TEST_KEY", "live-key-value")
	cat := "servers:\n  keyed: {type: http, url: \"http://127.0.0.1:1/mcp?api_key=${MCPICK_TEST_KEY}\"}\n"
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--select", "keyed", "measure"}, {"--select", "keyed", "--json", "measure"},
		{"--select", "keyed", "doctor"}, {"--select", "keyed", "--json", "doctor"},
	} {
		var out, errw strings.Builder
		a := &app{out: &out, errw: &errw, version: "test"}
		_ = a.run(append([]string{"--timeout", "2s"}, args...))
		all := out.String() + errw.String()
		if strings.Contains(all, "live-key-value") {
			t.Errorf("%v printed the expanded value:\n%s", args, all)
		}
		if !strings.Contains(all, "api_key=${MCPICK_TEST_KEY}") {
			t.Errorf("%v should show the URL as written:\n%s", args, all)
		}
	}
}
