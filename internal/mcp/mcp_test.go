package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeServer is a minimal MCP server over streamable HTTP, enough to exercise
// the client, the probe and the proxy without a real upstream.
func fakeServer(t *testing.T, name string, tools []string, sse bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		resp := RPCResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = json.RawMessage(fmt.Sprintf(
				`{"protocolVersion":%q,"capabilities":{},"serverInfo":{"name":%q,"version":"1.0"}}`,
				ProtocolVersion, name))
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
			resp.Error = &RPCError{Code: -32601, Message: "method not found"}
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

func rawParamsOf(req RPCRequest) json.RawMessage {
	data, _ := json.Marshal(req.Params)
	return data
}

func TestProbeCountsToolsAndTokens(t *testing.T) {
	srv := fakeServer(t, "alpha", []string{"one", "two"}, false)
	res := Probe(context.Background(), "alpha",
		map[string]any{"type": "http", "url": srv.URL}, 5*time.Second)
	if !res.OK {
		t.Fatalf("probe failed: %s", res.Err)
	}
	if res.Tools != 2 {
		t.Errorf("tools = %d, want 2", res.Tools)
	}
	if res.Tokens <= 0 {
		t.Error("a server with tools must have a non-zero context cost")
	}
	if res.Server.Name != "alpha" {
		t.Errorf("serverInfo = %+v", res.Server)
	}
}

// Streamable HTTP lets a server answer with an event stream instead of a JSON
// body; both have to work or half the remote servers look dead.
func TestProbeHandlesEventStream(t *testing.T) {
	srv := fakeServer(t, "beta", []string{"x"}, true)
	res := Probe(context.Background(), "beta",
		map[string]any{"type": "http", "url": srv.URL}, 5*time.Second)
	if !res.OK {
		t.Fatalf("probe failed: %s", res.Err)
	}
	if res.Tools != 1 {
		t.Errorf("tools = %d, want 1", res.Tools)
	}
}

func TestProbeReportsUnreachable(t *testing.T) {
	res := Probe(context.Background(), "dead",
		map[string]any{"type": "http", "url": "http://127.0.0.1:1/mcp"}, 2*time.Second)
	if res.OK {
		t.Fatal("a closed port is not a healthy server")
	}
	if res.Err == "" {
		t.Error("the failure should carry a reason")
	}
}

func TestHumanTokens(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{{0, ""}, {320, "320t"}, {4200, "4.2k"}, {250_000, "250k"}} {
		if got := HumanTokens(tc.in); got != tc.want {
			t.Errorf("HumanTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The fingerprint is what invalidates a cached measurement when a server is
// edited; Go's map order must not make it flap.
func TestSpecFingerprintIsStable(t *testing.T) {
	a := map[string]any{"url": "https://x", "headers": map[string]any{"A": "1", "B": "2"}}
	b := map[string]any{"headers": map[string]any{"B": "2", "A": "1"}, "url": "https://x"}
	if Fingerprint(a) != Fingerprint(b) {
		t.Error("key order changed the fingerprint")
	}
	c := map[string]any{"url": "https://y"}
	if Fingerprint(a) == Fingerprint(c) {
		t.Error("a different spec must get a different fingerprint")
	}
}

func TestMeasureCacheInvalidatesOnSpecChange(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	cache := LoadCache()
	spec := map[string]any{"url": "https://x"}
	cache.Put("srv", spec, Result{Name: "srv", OK: true, Tools: 3, Tokens: 900})

	if m, ok := cache.Get("srv", spec); !ok || m.Tokens != 900 {
		t.Fatalf("cache miss on an unchanged spec: %+v %v", m, ok)
	}
	if _, ok := cache.Get("srv", map[string]any{"url": "https://changed"}); ok {
		t.Error("editing a server must invalidate its measurement")
	}
}

func TestNeedsLoginRecognisesAuthFailures(t *testing.T) {
	for _, s := range []string{"HTTP 401: Unauthorized", "invalid_token", "HTTP 401: "} {
		if !NeedsLogin(s) {
			t.Errorf("NeedsLogin(%q) = false", s)
		}
	}
	if NeedsLogin("connection refused") {
		t.Error("a network failure is not an authorization failure")
	}
}

// The picker has seven characters for a failure; each kind must say enough
// to decide what to do next without reading the full message.
func TestErrorKind(t *testing.T) {
	for msg, want := range map[string]string{
		"HTTP 401: Unauthorized": "401",
		"HTTP 403: Forbidden":    "403",
		`Post "http://127.0.0.1:1/mcp": dial tcp 127.0.0.1:1: connect: connection refused`: "refused",
		"dial tcp: lookup nope.invalid: no such host":                                      "dns",
		"context deadline exceeded":                                                        "timeout",
		"tls: failed to verify certificate: x509: certificate signed by unknown authority": "tls",
		`exec: "uvx": executable file not found in $PATH`:                                  "no cmd",
		"server exited: fatal: bad config":                                                 "exited",
		"AHREFS_TOKEN is required: export it first":                                        "env var",
		"something nobody anticipated":                                                     "error",
	} {
		got := ErrorKind(msg)
		if got != want {
			t.Errorf("ErrorKind(%q) = %q, want %q", msg, got, want)
		}
		if len(got) > 7 {
			t.Errorf("%q is wider than the column", got)
		}
	}
}

func TestErrorKindExpired(t *testing.T) {
	if got := ErrorKind("Claude Code's token for this server has expired; reconnect it in Claude (/mcp)"); got != "expired" {
		t.Errorf("got %q", got)
	}
}
