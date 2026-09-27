package backend

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/spec"
)

// These pin each agent's dialect: the same catalog entry has to come out in the
// shape that agent reads, or it is silently ignored.

func emitter(name string) func(spec.Selection) ([]byte, error) {
	return byName[name].Dialect().Emit
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// Each tool spells the remote URL differently; rendering the same catalog entry
// for the wrong one produces a config the tool silently ignores.
func TestRemoteURLKeyPerTool(t *testing.T) {
	entry := map[string]any{"type": "http", "url": "https://example.com/mcp"}
	for _, tc := range []struct {
		name   string
		emit   func(spec.Selection) ([]byte, error)
		topKey string
		urlKey string
	}{
		{"claude", emitter("claude"), "mcpServers", "url"},
		{"gemini", emitter("gemini"), "mcpServers", "httpUrl"},
		{"antigravity", emitter("antigravity"), "mcpServers", "serverUrl"},
		{"muse", emitter("muse"), "mcpServers", "url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.emit(oneSel("srv", entry))
			if err != nil {
				t.Fatal(err)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal(data, &top); err != nil {
				t.Fatal(err)
			}
			var servers map[string]map[string]any
			if err := json.Unmarshal(top[tc.topKey], &servers); err != nil {
				t.Fatalf("no server map under %q: %v\n%s", tc.topKey, err, data)
			}
			entry, ok := servers["srv"]
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
	sel := spec.Selection{
		Names: []string{"local", "remote"},
		Specs: map[string]map[string]any{
			"local": {"type": "stdio", "command": "uvx", "args": []any{"some-mcp", "--flag"},
				"env": map[string]any{"K": "v"}},
			"remote": {"type": "http", "url": "https://example.com/mcp",
				"headers": map[string]any{"Authorization": "Bearer x"}},
		},
	}
	data, err := emitter("opencode")(sel)
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

func TestTOMLEmission(t *testing.T) {
	sel := spec.Selection{
		Names: []string{"local", "remote"},
		Specs: map[string]map[string]any{
			"local": {"type": "stdio", "command": "uvx", "args": []any{"a", `b"c`},
				"env": map[string]any{"K": "v"}},
			"remote": {"type": "http", "url": "https://example.com/mcp",
				"headers": map[string]any{"Authorization": "Bearer x"}},
		},
	}
	grok, err := emitter("grok")(sel)
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
	codex, err := emitter("codex")(sel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codex), "[mcp_servers.remote.http_headers]") {
		t.Errorf("codex output must carry an http_headers table:\n%s", codex)
	}
	if strings.Contains(string(codex), "[mcp_servers.remote.headers]") {
		t.Errorf("codex output must not use grok's table name:\n%s", codex)
	}
	if len(lossy(byName["codex"].Dialect(), sel)) != 0 {
		t.Error("nothing here is lost in the codex dialect")
	}
}

// Codex speaks stdio and streamable HTTP only; a legacy SSE server would sit in
// its config and never connect.
func TestLossyFlagsSSEForCodex(t *testing.T) {
	sel := oneSel("old", map[string]any{"type": "sse", "url": "https://x/sse"})
	if got := lossy(byName["codex"].Dialect(), sel); len(got) != 1 {
		t.Errorf("warnings = %v, want one about SSE", got)
	}
}

// Codex-only keys in the catalog reach codex and nobody else.
func TestDialectKeysReachOnlyTheirAgent(t *testing.T) {
	sel := oneSel("srv", map[string]any{
		"type": "http", "url": "https://x/mcp",
		"headers":              map[string]any{"Authorization": "Bearer x"},
		"bearer_token_env_var": "SRV_TOKEN",
	})
	claude, err := emitter("claude")(sel)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(claude), "bearer_token_env_var") {
		t.Errorf("a codex key leaked into the claude config:\n%s", claude)
	}
	data, err := emitter("codex")(sel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `bearer_token_env_var = "SRV_TOKEN"`) {
		t.Errorf("codex output:\n%s", data)
	}
}

// Gemini tells SSE from streamable HTTP by the key alone: url is SSE, httpUrl
// is HTTP. Getting it backwards connects with the wrong transport.
func TestGeminiURLKeyFollowsTransport(t *testing.T) {
	sel := spec.Selection{
		Names: []string{"h", "s"},
		Specs: map[string]map[string]any{
			"h": {"type": "http", "url": "https://x/mcp"},
			"s": {"type": "sse", "url": "https://x/sse"},
		},
	}
	var top struct {
		McpServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(must(emitter("gemini")(sel)), &top); err != nil {
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
	if err := json.Unmarshal(must(emitter("copilot")(oneSel("s", map[string]any{"url": "https://x"}))), &top); err != nil {
		t.Fatal(err)
	}
	if tools, ok := top.McpServers["s"]["tools"].([]any); !ok || len(tools) != 1 || tools[0] != "*" {
		t.Errorf("copilot entry = %v, want tools [*]", top.McpServers["s"])
	}
}
