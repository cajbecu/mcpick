package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

func oneSel(name string, spec map[string]any) Selection {
	return Selection{Names: []string{name}, Specs: map[string]map[string]any{name: spec}}
}

// Each tool spells the remote URL differently; rendering the same catalog entry
// for the wrong one produces a config the tool silently ignores.
func TestRemoteURLKeyPerTool(t *testing.T) {
	spec := map[string]any{"type": "http", "url": "https://example.com/mcp"}
	for _, tc := range []struct {
		name   string
		emit   func(Selection) ([]byte, error)
		topKey string
		urlKey string
	}{
		{"claude", EmitClaude, "mcpServers", "url"},
		{"gemini", EmitGemini, "mcpServers", "httpUrl"},
		{"antigravity", EmitAntigravity, "mcpServers", "serverUrl"},
		{"muse", EmitMuse, "mcp_servers", "url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.emit(oneSel("srv", spec))
			if err != nil {
				t.Fatal(err)
			}
			var top map[string]map[string]map[string]any
			if err := json.Unmarshal(data, &top); err != nil {
				t.Fatal(err)
			}
			entry, ok := top[tc.topKey]["srv"]
			if !ok {
				t.Fatalf("no srv under %q: %s", tc.topKey, data)
			}
			if entry[tc.urlKey] != "https://example.com/mcp" {
				t.Errorf("%s missing: %v", tc.urlKey, entry)
			}
		})
	}
}

func TestOpencodeShape(t *testing.T) {
	sel := Selection{
		Names: []string{"local", "remote"},
		Specs: map[string]map[string]any{
			"local": {"type": "stdio", "command": "uvx", "args": []any{"some-mcp", "--flag"},
				"env": map[string]any{"K": "v"}},
			"remote": {"type": "http", "url": "https://example.com/mcp",
				"headers": map[string]any{"Authorization": "Bearer x"}},
		},
	}
	data, err := EmitOpencode(sel)
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		MCP map[string]map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}

	local := top.MCP["local"]
	if local["type"] != "local" {
		t.Errorf("type = %v, want local", local["type"])
	}
	cmd, ok := local["command"].([]any)
	if !ok || len(cmd) != 3 || cmd[0] != "uvx" || cmd[2] != "--flag" {
		t.Errorf("command = %v, want the whole line as one array", local["command"])
	}
	if _, ok := local["environment"]; !ok {
		t.Error("opencode spells env 'environment'")
	}
	if local["enabled"] != true {
		t.Error("a rendered server must be enabled")
	}
	if top.MCP["remote"]["type"] != "remote" {
		t.Errorf("remote type = %v", top.MCP["remote"]["type"])
	}
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

func TestTOMLEmission(t *testing.T) {
	sel := Selection{
		Names: []string{"local", "remote"},
		Specs: map[string]map[string]any{
			"local": {"type": "stdio", "command": "uvx", "args": []any{"a", `b"c`},
				"env": map[string]any{"K": "v"}},
			"remote": {"type": "http", "url": "https://example.com/mcp",
				"headers": map[string]any{"Authorization": "Bearer x"}},
		},
	}
	grok, err := EmitGrok(sel)
	if err != nil {
		t.Fatal(err)
	}
	body := string(grok)
	for _, want := range []string{
		`[mcp_servers.local]`,
		`command = "uvx"`,
		`args = ["a", "b\"c"]`,
		`[mcp_servers.local.env]`,
		`url = "https://example.com/mcp"`,
		`[mcp_servers.remote.headers]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("grok output is missing %s:\n%s", want, body)
		}
	}

	// Codex calls the header table http_headers; under "headers" it would be
	// ignored and the server would answer 401.
	codex, err := EmitCodex(sel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codex), "[mcp_servers.remote.http_headers]") {
		t.Errorf("codex output must carry an http_headers table:\n%s", codex)
	}
	if strings.Contains(string(codex), "[mcp_servers.remote.headers]") {
		t.Errorf("codex output must not use grok's table name:\n%s", codex)
	}
	if len(Lossy("codex", sel)) != 0 {
		t.Error("nothing here is lost in the codex dialect")
	}
}

// Codex speaks stdio and streamable HTTP only; a legacy SSE server would sit in
// its config and never connect.
func TestLossyFlagsSSEForCodex(t *testing.T) {
	sel := oneSel("old", map[string]any{"type": "sse", "url": "https://x/sse"})
	if got := Lossy("codex", sel); len(got) != 1 {
		t.Errorf("warnings = %v, want one about SSE", got)
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
	generated, err := EmitClaude(oneSel("new", map[string]any{"type": "http", "url": "https://x/new"}))
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

// Codex-only keys in the catalog reach codex and nobody else.
func TestDialectKeysReachOnlyTheirAgent(t *testing.T) {
	sel := oneSel("srv", map[string]any{
		"type": "http", "url": "https://x/mcp",
		"headers":              map[string]any{"Authorization": "Bearer x"},
		"bearer_token_env_var": "SRV_TOKEN",
	})
	claude, err := EmitClaude(sel)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(claude), "bearer_token_env_var") {
		t.Errorf("a codex key leaked into the claude config:\n%s", claude)
	}
	data, err := EmitCodex(sel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `bearer_token_env_var = "SRV_TOKEN"`) {
		t.Errorf("codex output:\n%s", data)
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
	out, vars := Redact("ahrefs", spec)

	headers := out["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer ${AHREFS_AUTHORIZATION}" {
		t.Errorf("Authorization = %v, want a Bearer placeholder", headers["Authorization"])
	}
	if headers["X-Trace"] != "on" {
		t.Errorf("X-Trace is not a secret and must survive, got %v", headers["X-Trace"])
	}
	env := out["env"].(map[string]any)
	if env["API_KEY"] != "${AHREFS_API_KEY}" {
		t.Errorf("API_KEY = %v, want a placeholder", env["API_KEY"])
	}
	if env["DEBUG"] != "1" {
		t.Errorf("DEBUG = %v, want it untouched", env["DEBUG"])
	}
	joined := strings.Join(vars, " ")
	// The variable holds the credential alone; the scheme stays in the catalog.
	for _, want := range []string{"AHREFS_AUTHORIZATION=sk-live-123", "AHREFS_API_KEY=abcdef"} {
		if !strings.Contains(joined, want) {
			t.Errorf("vars %v should report %q", vars, want)
		}
	}
	// The original is untouched, so a failed import cannot corrupt the catalog.
	if spec["headers"].(map[string]any)["Authorization"] != "Bearer sk-live-123" {
		t.Error("redact mutated its input")
	}
}

func TestRedactLeavesExistingPlaceholders(t *testing.T) {
	spec := map[string]any{"headers": map[string]any{"Authorization": "Bearer ${TOKEN}"}}
	out, vars := Redact("x", spec)
	if out["headers"].(map[string]any)["Authorization"] != "Bearer ${TOKEN}" {
		t.Error("an existing placeholder must not be re-wrapped")
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
	out, err := Splice(original, must(EmitClaude(oneSel("s", map[string]any{"url": "https://x"}))), "mcpServers", false)
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

// Gemini tells SSE from streamable HTTP by the key alone: url is SSE, httpUrl
// is HTTP. Getting it backwards connects with the wrong transport.
func TestGeminiURLKeyFollowsTransport(t *testing.T) {
	sel := Selection{
		Names: []string{"h", "s"},
		Specs: map[string]map[string]any{
			"h": {"type": "http", "url": "https://x/mcp"},
			"s": {"type": "sse", "url": "https://x/sse"},
		},
	}
	var top struct {
		McpServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(must(EmitGemini(sel)), &top); err != nil {
		t.Fatal(err)
	}
	if top.McpServers["h"]["httpUrl"] == nil || top.McpServers["s"]["url"] == nil {
		t.Errorf("gemini entries = %v", top.McpServers)
	}
	if _, ok := top.McpServers["h"]["type"]; ok {
		t.Error("gemini has no type key; the transport is in the URL key")
	}
}

func TestCopilotGetsToolsDefault(t *testing.T) {
	var top struct {
		McpServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(must(EmitCopilot(oneSel("s", map[string]any{"url": "https://x"}))), &top); err != nil {
		t.Fatal(err)
	}
	if tools, ok := top.McpServers["s"]["tools"].([]any); !ok || len(tools) != 1 || tools[0] != "*" {
		t.Errorf("copilot entry = %v, want tools [*]", top.McpServers["s"])
	}
}
