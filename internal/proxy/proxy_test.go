package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/spec"
)

// fakeServer is a minimal MCP server over streamable HTTP, enough to exercise
// the client, the probe and the Proxy without a real upstream.
func fakeServer(t *testing.T, name string, tools []string, sse bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcp.RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		resp := mcp.RPCResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = json.RawMessage(fmt.Sprintf(
				`{"protocolVersion":%q,"capabilities":{},"serverInfo":{"name":%q,"version":"1.0"}}`,
				mcp.ProtocolVersion, name))
		case "tools/list":
			var list []string
			for _, tn := range tools {
				list = append(list, fmt.Sprintf(
					`{"name":%q,"description":"does %s","inputSchema":{"type":"object"}}`, tn, tn))
			}
			resp.Result = json.RawMessage(`{"tools":[` + strings.Join(list, ",") + `]}`)
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(rawParamsOf(req), &p)
			resp.Result = json.RawMessage(fmt.Sprintf(
				`{"content":[{"type":"text","text":"%s ran %s"}]}`, name, p.Name))
		default:
			resp.Error = &mcp.RPCError{Code: -32601, Message: "method not found"}
		}
		body, _ := json.Marshal(resp)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rawParamsOf(req mcp.RPCRequest) json.RawMessage {
	data, _ := json.Marshal(req.Params)
	return data
}

func proxyFor(t *testing.T) *Proxy {
	t.Helper()
	a := fakeServer(t, "alpha", []string{"one", "two"}, false)
	b := fakeServer(t, "beta", []string{"three"}, false)
	sel := spec.Selection{
		Names: []string{"alpha", "beta"},
		Specs: map[string]map[string]any{
			"alpha": {"type": "http", "url": a.URL},
			"beta":  {"type": "http", "url": b.URL},
		},
	}
	p := New(sel, 5*time.Second)
	t.Cleanup(p.Close)
	return p
}

func call(t *testing.T, p *Proxy, method string, params any) mcp.RPCResponse {
	t.Helper()
	id := 1
	req := mcp.RPCRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp := p.Handle(context.Background(), raw)
	if resp == nil {
		t.Fatal("expected a response")
	}
	return *resp
}

func TestProxyAggregatesAndNamespacesTools(t *testing.T) {
	p := proxyFor(t)
	resp := call(t, p, "tools/list", map[string]any{})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var out struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tools) != 3 {
		t.Fatalf("got %d tools, want 3", len(out.Tools))
	}
	want := map[string]bool{"alpha__one": true, "alpha__two": true, "beta__three": true}
	for _, tool := range out.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Errorf("missing tools: %v", want)
	}
}

func TestProxyRoutesCallsByPrefix(t *testing.T) {
	p := proxyFor(t)
	resp := call(t, p, "tools/call", map[string]any{"name": "beta__three", "arguments": map[string]any{}})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if !strings.Contains(string(resp.Result), "beta ran three") {
		t.Errorf("result = %s, want the call routed to beta", resp.Result)
	}
}

func TestProxyRejectsUnnamespacedTool(t *testing.T) {
	p := proxyFor(t)
	resp := call(t, p, "tools/call", map[string]any{"name": "three"})
	if resp.Error == nil {
		t.Fatal("a tool name without a server prefix cannot be routed")
	}
}

// One dead upstream must not take the whole list down with it.
func TestProxySurvivesDeadUpstream(t *testing.T) {
	good := fakeServer(t, "alpha", []string{"one"}, false)
	sel := spec.Selection{
		Names: []string{"alpha", "dead"},
		Specs: map[string]map[string]any{
			"alpha": {"type": "http", "url": good.URL},
			"dead":  {"type": "http", "url": "http://127.0.0.1:1/mcp"},
		},
	}
	p := New(sel, 2*time.Second)
	defer p.Close()

	resp := call(t, p, "tools/list", map[string]any{})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if !strings.Contains(string(resp.Result), "alpha__one") {
		t.Errorf("the healthy server's tools are missing: %s", resp.Result)
	}
}

func TestProxyInitializeAdvertisesTools(t *testing.T) {
	p := proxyFor(t)
	resp := call(t, p, "initialize", map[string]any{})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var out struct {
		Capabilities map[string]any `json:"capabilities"`
		ServerInfo   mcp.ServerInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Capabilities["tools"]; !ok {
		t.Error("the proxy serves tools and must say so")
	}
	if out.ServerInfo.Name != "mcpick" {
		t.Errorf("serverInfo = %+v", out.ServerInfo)
	}
}

func TestProxyIgnoresNotifications(t *testing.T) {
	p := proxyFor(t)
	req := mcp.RPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}
	raw, _ := json.Marshal(req)
	if resp := p.Handle(context.Background(), raw); resp != nil {
		t.Errorf("a notification must not be answered, got %+v", resp)
	}
}

func TestServeStdioRoundTrip(t *testing.T) {
	p := proxyFor(t)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
`)
	var out strings.Builder
	if err := p.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d responses, want 2 (the notification is not one):\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[1], "alpha__one") {
		t.Errorf("tools/list answer = %s", lines[1])
	}
}

// Clients reject tool names outside [A-Za-z0-9_-]{1,64}, and one bad name
// fails the whole list, not just that tool.
func TestExposedNamesStayWithinClientLimits(t *testing.T) {
	long := strings.Repeat("x", 80)
	for _, tc := range []struct{ server, tool string }{
		{"my.server", "do-thing"},
		{"s", long},
		{long, long},
	} {
		got := exposedName(tc.server, tc.tool)
		if len(got) > maxToolName {
			t.Errorf("%q is %d chars", got, len(got))
		}
		for _, r := range got {
			if !strings.ContainsRune(allowedToolRunes, r) {
				t.Errorf("%q contains %q", got, r)
			}
		}
	}
	if exposedName("s", long+"a") == exposedName("s", long+"b") {
		t.Error("two long names truncated to the same exposed name")
	}
}

func TestProxyRoutesSanitisedNames(t *testing.T) {
	up := fakeServer(t, "dotted", []string{"go"}, false)
	p := New(spec.Selection{Names: []string{"my.server"}, Specs: map[string]map[string]any{
		"my.server": {"type": "http", "url": up.URL},
	}}, 5*time.Second)
	defer p.Close()
	call(t, p, "tools/list", map[string]any{})
	resp := call(t, p, "tools/call", map[string]any{"name": "my_server__go"})
	if resp.Error != nil {
		t.Fatalf("a sanitised name must still route: %v", resp.Error)
	}
}

// The MCP spec requires HTTP servers to check Origin: without it any web page
// can drive a localhost server through DNS rebinding.
func TestHTTPRejectsForeignOrigin(t *testing.T) {
	p := proxyFor(t)
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	for origin, want := range map[string]int{
		"https://evil.example":  http.StatusForbidden,
		"http://localhost:3000": http.StatusOK,
		"":                      http.StatusOK,
	} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("origin %q: status %d, want %d", origin, resp.StatusCode, want)
		}
	}
}

func TestCheckAddrRefusesNonLoopback(t *testing.T) {
	t.Setenv("MCPICK_SERVE_ALLOW_REMOTE", "")
	for addr, ok := range map[string]bool{
		"127.0.0.1:7000": true, "localhost:7000": true, "[::1]:7000": true,
		":7000": false, "0.0.0.0:7000": false, "192.168.1.5:7000": false,
	} {
		if err := CheckAddr(addr); (err == nil) != ok {
			t.Errorf("CheckAddr(%s) = %v", addr, err)
		}
	}
	t.Setenv("MCPICK_SERVE_ALLOW_REMOTE", "1")
	if err := CheckAddr(":7000"); err != nil {
		t.Errorf("the override must allow it: %v", err)
	}
}

func TestInitializeEchoesSupportedVersion(t *testing.T) {
	p := proxyFor(t)
	resp := call(t, p, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	if !strings.Contains(string(resp.Result), `"2024-11-05"`) {
		t.Errorf("result = %s, want the client's supported version echoed", resp.Result)
	}
	resp = call(t, p, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	if !strings.Contains(string(resp.Result), mcp.ProtocolVersion) {
		t.Errorf("result = %s, want mcpick's own version for an unknown one", resp.Result)
	}
}

// A failed upstream is retried after a while, not written off for the life of
// the proxy — it may only have been restarting.
func TestDeadUpstreamIsRetried(t *testing.T) {
	p := New(spec.Selection{Names: []string{"x"}, Specs: map[string]map[string]any{
		"x": {"type": "http", "url": "http://127.0.0.1:1/mcp"},
	}}, time.Second)
	defer p.Close()
	if _, err := p.connect(context.Background(), "x"); err == nil {
		t.Fatal("expected a failure")
	}
	p.mu.Lock()
	d := p.dead["x"]
	d.until = time.Now().Add(-time.Second)
	p.dead["x"] = d
	p.mu.Unlock()

	up := fakeServer(t, "x", []string{"t"}, false)
	p.sel.Specs["x"]["url"] = up.URL
	if _, err := p.connect(context.Background(), "x"); err != nil {
		t.Errorf("an expired failure must be retried: %v", err)
	}
}

const allowedToolRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
