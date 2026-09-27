package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

func oneSel(name string, spec map[string]any) Selection {
	return Selection{Names: []string{name}, Specs: map[string]map[string]any{name: spec}}
}

// opencode's single-array command has to survive a round trip back into the
// Claude shape, or a catalog imported from opencode renders as an empty command.
func TestOpencodeCommandRoundTrip(t *testing.T) {
	v := ViewOf(map[string]any{"type": "local", "command": []any{"npx", "-y", "pkg"}})
	if v.Command != "npx" {
		t.Errorf("command = %q, want npx", v.Command)
	}
	if len(v.Args) != 2 || v.Args[1] != "pkg" {
		t.Errorf("args = %v, want [-y pkg]", v.Args)
	}
	if v.Transport != "stdio" {
		t.Errorf("transport = %q, want stdio", v.Transport)
	}
}

func TestViewOfInfersTransport(t *testing.T) {
	if got := ViewOf(map[string]any{"url": "https://x/mcp"}).Transport; got != "http" {
		t.Errorf("a bare url should imply http, got %q", got)
	}
	if got := ViewOf(map[string]any{"command": "x"}).Transport; got != "stdio" {
		t.Errorf("a bare command should imply stdio, got %q", got)
	}
	if got := ViewOf(map[string]any{"type": "remote", "url": "https://x"}).Transport; got != "http" {
		t.Errorf("opencode's 'remote' should normalise to http, got %q", got)
	}
}

// A user's config.toml holds far more than MCP servers. Replacing the file
// instead of splicing into it would silently reset their model and approval
// settings.
// A user's config.toml holds far more than MCP servers. Replacing the file
// instead of splicing into it would silently reset their model and approval
// settings.
func TestSpliceTOMLKeepsUnrelatedTables(t *testing.T) {
	original := `model = "gpt-5"
approval_policy = "on-request"

[mcp_servers.old]
command = "gone"

[mcp_servers.old.env]
A = "1"

[history]
persistence = "save-all"
`
	spliced, err := Splice([]byte(original), []byte("[mcp_servers.new]\ncommand = \"here\"\n"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	out := string(spliced)
	for _, want := range []string{`model = "gpt-5"`, `[history]`, `persistence = "save-all"`, `[mcp_servers.new]`} {
		if !strings.Contains(out, want) {
			t.Errorf("merged output is missing %s:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"mcp_servers.old", `command = "gone"`, `A = "1"`} {
		if strings.Contains(out, unwanted) {
			t.Errorf("merged output still carries %s:\n%s", unwanted, out)
		}
	}
}

func TestMergeIntoKeepsForeignKeys(t *testing.T) {
	original := []byte(`{"theme":"dark","mcpServers":{"old":{"url":"https://x/old"}}}`)
	generated, err := Claude.Emit(oneSel("new", map[string]any{"type": "http", "url": "https://x/new"}))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := spliceJSON(original, generated, "mcpServers")
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(merged, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["theme"]; !ok {
		t.Error("unrelated settings must survive")
	}
	var servers map[string]any
	if err := json.Unmarshal(top["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers["old"]; ok {
		t.Error("the server block is replaced wholesale, not merged entry by entry")
	}
	if _, ok := servers["new"]; !ok {
		t.Error("the generated server is missing")
	}
}

func TestMergeIntoRejectsBrokenJSON(t *testing.T) {
	if _, err := spliceJSON([]byte("{not json"), []byte(`{"mcpServers":{}}`), "mcpServers"); err == nil {
		t.Fatal("a corrupt config must be reported, not silently overwritten")
	}
}

func TestTOMLStringEscaping(t *testing.T) {
	if got := tomlString("a\"b\\c\nd"); got != `"a\"b\\c\nd"` {
		t.Errorf("got %s", got)
	}
	if got := tomlKey("with space"); got != `"with space"` {
		t.Errorf("a non-bare key must be quoted, got %s", got)
	}
	if got := tomlKey("plain-key_1"); got != "plain-key_1" {
		t.Errorf("a bare key must stay bare, got %s", got)
	}
}

func TestExpandPlaceholders(t *testing.T) {
	t.Setenv("MCPICK_TEST_TOKEN", "secret")
	spec := map[string]any{
		"url":  "http://x/mcp?s={UUID}&t=${MCPICK_TEST_TOKEN}&d=${MCPICK_TEST_MISSING:-fallback}",
		"args": []any{"--sid", "{UUID}"},
	}
	out, err := Expand(spec, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	got := out.(map[string]any)
	want := "http://x/mcp?s=abc123&t=secret&d=fallback"
	if got["url"] != want {
		t.Errorf("url = %q, want %q", got["url"], want)
	}
	if got["args"].([]any)[1] != "abc123" {
		t.Errorf("args = %v, want [--sid abc123]", got["args"])
	}
}

func TestExpandRequiredVariableFails(t *testing.T) {
	spec := map[string]any{"headers": map[string]any{
		"Authorization": "Bearer ${MCPICK_TEST_REQUIRED:?set it in your shell}",
	}}
	_, err := Expand(spec, "uid")
	if err == nil {
		t.Fatal("a missing ${VAR:?} must be an error, not an empty header")
	}
	if !strings.Contains(err.Error(), "set it in your shell") {
		t.Errorf("error %q should carry the explanation", err)
	}
}

func TestExpandRequiredVariableSatisfied(t *testing.T) {
	t.Setenv("MCPICK_TEST_REQUIRED", "value")
	out, err := Expand("x-${MCPICK_TEST_REQUIRED:?why}", "uid")
	if err != nil {
		t.Fatal(err)
	}
	if out != "x-value" {
		t.Errorf("got %q, want x-value", out)
	}
}

func TestExpandEscape(t *testing.T) {
	t.Setenv("MCPICK_TEST_TOKEN", "secret")
	out, err := Expand("$${MCPICK_TEST_TOKEN}", "uid")
	if err != nil {
		t.Fatal(err)
	}
	if out != "${MCPICK_TEST_TOKEN}" {
		t.Errorf("got %q, want the literal placeholder", out)
	}
}

func TestExpandUnsetIsEmpty(t *testing.T) {
	out, err := Expand("a${MCPICK_TEST_NOT_SET}b", "uid")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ab" {
		t.Errorf("got %q, want ab", out)
	}
}

func TestRedactMovesSecretsToVariables(t *testing.T) {
	spec := map[string]any{
		"type": "http",
		"url":  "https://api.example.com/mcp",
		"headers": map[string]any{
			"Authorization": "Bearer sk-live-123",
			"X-Trace":       "on",
		},
		"env": map[string]any{"API_KEY": "abcdef", "DEBUG": "1"},
	}
	out, vars := Redact("github", spec)

	headers := out["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer "+Placeholder("GITHUB_AUTHORIZATION") {
		t.Errorf("Authorization = %v, want a Bearer placeholder", headers["Authorization"])
	}
	if headers["X-Trace"] != "on" {
		t.Errorf("X-Trace is not a secret and must survive, got %v", headers["X-Trace"])
	}
	env := out["env"].(map[string]any)
	if env["API_KEY"] != Placeholder("GITHUB_API_KEY") {
		t.Errorf("API_KEY = %v, want a placeholder", env["API_KEY"])
	}
	if env["DEBUG"] != "1" {
		t.Errorf("DEBUG = %v, want it untouched", env["DEBUG"])
	}
	joined := strings.Join(vars, " ")
	// The variable holds the credential alone; the scheme stays in the catalog.
	for _, want := range []string{"GITHUB_AUTHORIZATION=sk-live-123", "GITHUB_API_KEY=abcdef"} {
		if !strings.Contains(joined, want) {
			t.Errorf("vars %v should report %q", vars, want)
		}
	}
	// The original is untouched, so a failed import cannot corrupt the catalog.
	if spec["headers"].(map[string]any)["Authorization"] != "Bearer sk-live-123" {
		t.Error("redact mutated its input")
	}
}

// A redacted placeholder fails the launch when the variable is missing,
// naming it, instead of rendering an empty credential.
func TestPlaceholderFailsLoudlyWhenUnset(t *testing.T) {
	t.Setenv("GITHUB_API_KEY", "")
	_, err := Expand(map[string]any{"headers": map[string]any{"X-Api-Key": Placeholder("GITHUB_API_KEY")}}, "u")
	if err == nil || !strings.Contains(err.Error(), "export GITHUB_API_KEY") {
		t.Fatalf("err = %v; want a failure that says what to export", err)
	}
	t.Setenv("GITHUB_API_KEY", "abc")
	out, err := Expand(Placeholder("GITHUB_API_KEY"), "u")
	if err != nil || out != "abc" {
		t.Fatalf("out = %v, err = %v", out, err)
	}
}

// Credentials that are not under a secret-sounding key: a flag's value in
// args, a K=V argument, a URL's password or query parameter, a database URL
// in env, a token with a well-known prefix under a plain header.
func TestRedactFindsCredentialsOutsideSecretKeys(t *testing.T) {
	spec := map[string]any{
		"type":    "stdio",
		"command": "npx",
		"args": []any{"-y", "pkg", "--api-key", "sk-abcdef0123456789ABCDEF", "--token=t0ken-value-1",
			"PGPASSWORD=pw-in-arg", "--no-auth", "evil-pkg", "--key-file", "/etc/key.pem",
			"--auth", "false", "--dsn", "postgres://app:hunter2@db.example.com:5432/app",
			"--verbose"},
		"url": "https://user:url-pw@mcp.example.com/sse?api_key=q-secret&keyword=plain&v=2",
		"env": map[string]any{
			"DATABASE_URL": "postgres://app:hunter2@db.example.com:5432/app",
			"TOKEN_URL":    "https://issuer.example.com/token",
			"CLIENT_ID":    "client-123",
			"DEBUG":        "1",
		},
		"headers": map[string]any{
			"X-Custom": "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
			"X-Plain":  "sk-xxxxxxxxxxxxxxxxxxxx", // the prefix alone, no entropy: not a token
		},
	}
	out, vars := Redact("svc", spec)
	args := out["args"].([]any)
	want := []any{"-y", "pkg", "--api-key", Placeholder("SVC_API_KEY"), "--token=" + Placeholder("SVC_TOKEN"),
		"PGPASSWORD=" + Placeholder("SVC_PGPASSWORD"), "--no-auth", "evil-pkg", "--key-file", "/etc/key.pem",
		"--auth", "false", "--dsn", "postgres://app:" + Placeholder("SVC_ARGS_PASSWORD") + "@db.example.com:5432/app",
		"--verbose"}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
	if out["url"] != "https://user:"+Placeholder("SVC_PASSWORD")+"@mcp.example.com/sse?api_key="+Placeholder("SVC_API_KEY_2")+"&keyword=plain&v=2" {
		t.Errorf("url = %v", out["url"])
	}
	env := out["env"].(map[string]any)
	// The same password as in args keeps the name it was given first.
	if env["DATABASE_URL"] != "postgres://app:"+Placeholder("SVC_ARGS_PASSWORD")+"@db.example.com:5432/app" {
		t.Errorf("DATABASE_URL = %v", env["DATABASE_URL"])
	}
	for _, k := range []string{"TOKEN_URL", "CLIENT_ID", "DEBUG"} {
		if env[k] != spec["env"].(map[string]any)[k] {
			t.Errorf("%s = %v, want it untouched", k, env[k])
		}
	}
	headers := out["headers"].(map[string]any)
	if headers["X-Custom"] != Placeholder("SVC_X_CUSTOM") {
		t.Errorf("X-Custom = %v, want the ghp_ token redacted", headers["X-Custom"])
	}
	if headers["X-Plain"] != "sk-xxxxxxxxxxxxxxxxxxxx" {
		t.Errorf("X-Plain = %v, want the low-entropy value kept", headers["X-Plain"])
	}
	got := strings.Join(vars, "\n")
	for _, line := range []string{
		"SVC_API_KEY=sk-abcdef0123456789ABCDEF", "SVC_TOKEN=t0ken-value-1", "SVC_PGPASSWORD=pw-in-arg",
		"SVC_ARGS_PASSWORD=hunter2", "SVC_PASSWORD=url-pw", "SVC_API_KEY_2=q-secret",
		"SVC_X_CUSTOM=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("vars lack %q:\n%s", line, got)
		}
	}
	if len(vars) != 7 {
		t.Errorf("%d vars, want 7:\n%s", len(vars), got)
	}
}

func TestSecretish(t *testing.T) {
	for name, want := range map[string]bool{
		"Authorization": true, "Proxy-Authorization": true, "API_KEY": true, "apiKey": true, "x-api-key": true,
		"GITHUB_TOKEN": true, "--token": true, "--pat": true, "DB_PASSWORD": true, "passwd": true, "Cookie": true,
		"GITHUB_PERSONAL_ACCESS_TOKEN": true, "SECRET": true, "PGPASSWORD": true, "accessToken": true,
		"keyword": false, "monkey": false, "--path": false, "--format": false, "KEY_FILE": false,
		"CLIENT_ID": false, "TOKEN_URL": false, "--no-auth": false, "--auth-type": false, "DEBUG": false,
		"DATABASE_URL": false, "OAUTH_CLIENT_ID": false,
	} {
		if got := secretish(name); got != want {
			t.Errorf("secretish(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMaskText(t *testing.T) {
	for in, want := range map[string]string{
		`Post "https://h/mcp?api_key=abc&v=2": refused`:    `Post "https://h/mcp?api_key=***&v=2": refused`,
		"dial postgres://u:pw@h:5432/db":                   "dial postgres://u:***@h:5432/db",
		"token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 x": "token *** x",
		"plain text with ?keyword=abc":                     "plain text with ?keyword=abc",
		"as written ?key=${K}":                             "as written ?key=${K}",
		"https://h/cb#access_token=abc123&state=x":         "https://h/cb#access_token=***&state=x",
	} {
		if got := MaskText(in); got != want {
			t.Errorf("MaskText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactLeavesExistingPlaceholders(t *testing.T) {
	spec := map[string]any{
		"headers": map[string]any{"Authorization": "Bearer ${TOKEN}"},
		"url":     "https://u:${PW}@h/mcp?api_key=${K}",
		"args":    []any{"--api-key", "${K}", "--token=${T}"},
	}
	out, vars := Redact("x", spec)
	if out["headers"].(map[string]any)["Authorization"] != "Bearer ${TOKEN}" {
		t.Error("an existing placeholder must not be re-wrapped")
	}
	if out["url"] != spec["url"] {
		t.Errorf("url = %v, want it as written", out["url"])
	}
	if a := out["args"].([]any); a[1] != "${K}" || a[2] != "--token=${T}" {
		t.Errorf("args = %v, want them as written", a)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v, want none", vars)
	}
}

// The inverse of Splice: the agent's edits to the file survive, the server
// block goes back to what it was.
func TestRestoreKeepsEditsAndOriginalServers(t *testing.T) {
	original := []byte(`{"theme":"dark","mcpServers":{"mine":{"url":"https://x/mine"}}}`)
	edited := []byte(`{"theme":"light","newKey":1,"mcpServers":{"generated":{"url":"https://x/g"}}}`)
	out, err := Restore(edited, original, "mcpServers", false)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	if top["theme"] != "light" || top["newKey"] == nil {
		t.Errorf("the edits made during the run were lost: %s", out)
	}
	servers := top["mcpServers"].(map[string]any)
	if _, ok := servers["mine"]; !ok || len(servers) != 1 {
		t.Errorf("servers = %v, want the original block back", servers)
	}
}

func TestRestoreDropsKeyTheOriginalLacked(t *testing.T) {
	out, err := Restore([]byte(`{"a":1,"mcpServers":{"g":{}}}`), nil, "mcpServers", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "mcpServers") {
		t.Errorf("a server block mcpick added must go: %s", out)
	}
}

func TestRestoreTOML(t *testing.T) {
	original := []byte("model = \"a\"\n\n[mcp_servers.mine]\ncommand = \"m\"\n")
	edited := []byte("model = \"b\"\n\n[mcp_servers.gen]\ncommand = \"g\"\n\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n")
	out := string(must(Restore(edited, original, "", true)))
	for _, want := range []string{`model = "b"`, `[projects."/w"]`, `[mcp_servers.mine]`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "mcp_servers.gen") {
		t.Errorf("the generated server survived:\n%s", out)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// Splicing must not reorder a file the user reads in their editor.
func TestSpliceKeepsKeyOrder(t *testing.T) {
	original := []byte(`{"zeta":1,"mcpServers":{},"alpha":2}`)
	out, err := Splice(original, must(Claude.Emit(oneSel("s", map[string]any{"url": "https://x"}))), "mcpServers", false)
	if err != nil {
		t.Fatal(err)
	}
	order, err := TopLevelOrder(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "zeta,mcpServers,alpha" {
		t.Errorf("order = %v", order)
	}
}

func TestTOMLServerNames(t *testing.T) {
	data := []byte("[mcp_servers.a]\ncommand=\"x\"\n[mcp_servers.a.env]\nK=\"v\"\n[mcp_servers.\"b.c\"]\nurl=\"u\"\n[other]\n")
	got := strings.Join(TOMLServerNames(data), ",")
	if got != "a,b.c" {
		t.Errorf("names = %s, want a,b.c", got)
	}
}

// A key one agent owns reaches that agent only; a key nobody owns passes
// everywhere, since it may be one mcpick has not heard of yet.
func TestOwnedKeysReachOnlyTheirOwner(t *testing.T) {
	Own("test-owner", "owned_key")
	if !ExtraAllowed("test-owner", "owned_key") {
		t.Error("the owner lost its own key")
	}
	if ExtraAllowed("test-other", "owned_key") {
		t.Error("an owned key reached another agent")
	}
	if !ExtraAllowed("test-other", "nobodys_key") {
		t.Error("an unowned key was dropped")
	}
}

// muse refuses a settings file without schema_version, and for a user who
// has no settings file the generated block is the whole file. A dialect may
// emit such a document-level key; the splice has to keep it in a new file
// and must not override the user's own value where the file already has one.
func TestSpliceKeepsDocumentKeysTheAgentRequires(t *testing.T) {
	gen := []byte(`{"schema_version": 1, "mcpServers": {"s": {"command": "x"}}}`)
	fresh, err := Splice(nil, gen, "mcpServers", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fresh), `"schema_version": 1`) {
		t.Errorf("a new file lost the key the agent requires:\n%s", fresh)
	}
	existing, err := Splice([]byte(`{"schema_version": 3, "theme": "dark"}`), gen, "mcpServers", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema_version": 3`, `"theme": "dark"`, `"mcpServers"`} {
		if !strings.Contains(string(existing), want) {
			t.Errorf("missing %s:\n%s", want, existing)
		}
	}
	if strings.Contains(string(existing), `"schema_version": 1`) {
		t.Errorf("the user's own value was overridden:\n%s", existing)
	}
}

// A credential is known by its form as well as by its key: `Bearer x`
// under AUTH_HEADER or X-Custom, a header given to a curl-style command
// as `-H 'Name: value'`, a `Token x` argument. The scheme stays readable.
func TestRedactFindsCredentialsByForm(t *testing.T) {
	spec := map[string]any{
		"command": "srv",
		"args": []any{"-H", "X-Api-Key: abcdef0123456789xyz", "--header", "X-Trace: on",
			"-H", "X-Any: Bearer hdrarg-secret-1", "--header=Authorization: Basic dXNlcjpwYXNz",
			"Token tokarg-secret-2", "-H", "X-Ref: ${REF}"},
		"env": map[string]any{
			"AUTH_HEADER": "Bearer hdrsecret12345",
			"GREETING":    "Bearer me",
		},
		"headers": map[string]any{"X-Custom": "Bearer customsecret1234"},
	}
	out, vars := Redact("e", spec)
	args := out["args"].([]any)
	want := []any{"-H", "X-Api-Key: " + Placeholder("E_X_API_KEY"), "--header", "X-Trace: on",
		"-H", "X-Any: Bearer " + Placeholder("E_X_ANY"), "--header=Authorization: Basic " + Placeholder("E_AUTHORIZATION"),
		"Token " + Placeholder("E_ARGS"), "-H", "X-Ref: ${REF}"}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
	env := out["env"].(map[string]any)
	if env["AUTH_HEADER"] != "Bearer "+Placeholder("E_AUTH_HEADER") {
		t.Errorf("AUTH_HEADER = %v", env["AUTH_HEADER"])
	}
	if env["GREETING"] != "Bearer me" {
		t.Errorf("GREETING = %v; a short word after Bearer is not a credential", env["GREETING"])
	}
	if h := out["headers"].(map[string]any); h["X-Custom"] != "Bearer "+Placeholder("E_X_CUSTOM") {
		t.Errorf("X-Custom = %v", h["X-Custom"])
	}
	joined := strings.Join(vars, " ")
	for _, s := range []string{"abcdef0123456789xyz", "hdrarg-secret-1", "dXNlcjpwYXNz", "tokarg-secret-2", "hdrsecret12345", "customsecret1234"} {
		if !strings.Contains(joined, "="+s) {
			t.Errorf("vars %v should carry %q", vars, s)
		}
	}
}

// One `${VAR}` in a value no longer hides the rest of it: only a match
// that holds a reference is left as written.
func TestRedactSearchesAroundAPlaceholder(t *testing.T) {
	spec := map[string]any{
		"url": "https://h/mcp?user=${USER}&token=qsecret-1&key=${K}",
		"env": map[string]any{"DSN": "postgres://${DBUSER}:dbpw-secret@h/db?password=${P}"},
	}
	out, vars := Redact("s", spec)
	if out["url"] != "https://h/mcp?user=${USER}&token="+Placeholder("S_TOKEN")+"&key=${K}" {
		t.Errorf("url = %v", out["url"])
	}
	if env := out["env"].(map[string]any); env["DSN"] != "postgres://${DBUSER}:"+Placeholder("S_DSN_PASSWORD")+"@h/db?password=${P}" {
		t.Errorf("DSN = %v", env["DSN"])
	}
	if strings.Join(vars, " ") != "S_DSN_PASSWORD=dbpw-secret S_TOKEN=qsecret-1" {
		t.Errorf("vars = %v", vars)
	}
}

func TestMaskTextBearer(t *testing.T) {
	for in, want := range map[string]string{
		"HTTP 401: invalid credential Bearer literalsecret123 for /echo": "HTTP 401: invalid credential Bearer *** for /echo",
		"bearer ${TOK}": "bearer ${TOK}",
		"Bearer short":  "Bearer short",
	} {
		if got := MaskText(in); got != want {
			t.Errorf("MaskText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubberPutsBackReferences(t *testing.T) {
	t.Setenv("MCPICK_TEST_TOK", "SECRET/VALUE+42")
	t.Setenv("MCPICK_TEST_PORT", "8080")
	raw := map[string]any{"url": "https://h:${MCPICK_TEST_PORT}/x?k=${MCPICK_TEST_TOK}", "env": map[string]any{"A": "$${MCPICK_TEST_TOK}"}}
	final := map[string]any{"url": "https://h:8080/x?k=SECRET/VALUE+42", "headers": map[string]any{"Authorization": "Bearer oauth-token-1"}}
	s := NewScrubber(raw, final)
	for in, want := range map[string]string{
		"got SECRET/VALUE+42 back":     "got ${MCPICK_TEST_TOK} back",
		"lookup secret/value+42: nope": "lookup ${MCPICK_TEST_TOK}: nope",
		"k=SECRET%2FVALUE%2B42":        "k=${MCPICK_TEST_TOK}",
		"Bearer oauth-token-1 refused": "Bearer *** refused",
		"port 8080 is fine":            "port 8080 is fine", // too short to scrub
	} {
		if got := s.Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
	if (*Scrubber)(nil).Scrub("x") != "x" {
		t.Error("a nil Scrubber changes nothing")
	}
}

// OAuth's implicit flow returns the token in the URL fragment; a catalog URL
// copied from a browser can carry it there, and it must be found like a
// query parameter.
func TestRedactFindsATokenInTheFragment(t *testing.T) {
	out, vars := Redact("svc", map[string]any{"type": "http", "url": "https://h/mcp#access_token=frag-secret-123&state=x"})
	u, _ := out["url"].(string)
	if strings.Contains(u, "frag-secret-123") || len(vars) == 0 {
		t.Errorf("url = %q, vars = %v", u, vars)
	}
}
