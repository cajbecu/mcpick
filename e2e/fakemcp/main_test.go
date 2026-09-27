package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fake is what every agent in the e2e run talks to. If it answered the
// wrong shape, every agent would look broken and the run would blame mcpick
// for it; so the answers are checked against the spec's field names here,
// with a client written independently of the server.
func TestHandshakeAndToolsSpeakTheSpec(t *testing.T) {
	s, err := newServer("srv-a", "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(id, method, params string) map[string]any {
		t.Helper()
		var req request
		raw := `{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `","params":` + params + `}`
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		resp := s.handle("stdio", req)
		if resp == nil {
			t.Fatalf("%s got no answer", method)
		}
		data, _ := json.Marshal(resp)
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		if got, _ := json.Marshal(out["id"]); string(got) != id {
			t.Errorf("%s: id echoed as %s, want %s", method, got, id)
		}
		return out
	}

	init := call("1", "initialize", `{"protocolVersion":"2025-03-26","clientInfo":{"name":"t","version":"0"}}`)
	res := init["result"].(map[string]any)
	if res["protocolVersion"] != "2025-03-26" {
		t.Errorf("the server must agree to the client's protocol version, got %v", res["protocolVersion"])
	}
	if info, _ := res["serverInfo"].(map[string]any); info["name"] != "fakemcp-srv-a" {
		t.Errorf("serverInfo = %v", res["serverInfo"])
	}

	list := call("2", "tools/list", `{}`)
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "nonce" {
		t.Fatalf("tools = %v, want exactly one, named nonce", tools)
	}
	if _, ok := tools[0].(map[string]any)["inputSchema"]; !ok {
		t.Error("a tool without inputSchema fails validation in strict clients")
	}

	got := call("3", "tools/call", `{"name":"nonce","arguments":{}}`)
	content := got["result"].(map[string]any)["content"].([]any)
	if text := content[0].(map[string]any)["text"]; text != s.nonce || len(s.nonce) != 16 {
		t.Errorf("tools/call returned %v, want the 16-hex nonce %q", text, s.nonce)
	}

	if e := call("4", "resources/list", `{}`)["error"].(map[string]any); e["code"] != float64(-32601) {
		t.Errorf("an unsupported method must be -32601, got %v", e)
	}
}

// A notification carries no id and must get no answer: on stdio an answer to
// it would be an unsolicited message the client cannot route.
func TestNotificationsAreNotAnswered(t *testing.T) {
	s, _ := newServer("srv-a", "")
	var req request
	_ = json.Unmarshal([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), &req)
	if resp := s.handle("stdio", req); resp != nil {
		t.Errorf("a notification was answered: %+v", resp)
	}
}

// Streamable HTTP clients expect a session id from initialize, JSON answers
// to requests and 202 for notifications; a fake that got any of these wrong
// would show as a transport failure in every HTTP-capable agent.
func TestStreamableHTTPShape(t *testing.T) {
	s, _ := newServer("srv-b", "")
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	post := func(body string) *http.Response {
		t.Helper()
		resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	defer resp.Body.Close()
	if resp.Header.Get("Mcp-Session-Id") == "" {
		t.Error("initialize must issue Mcp-Session-Id")
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content type %q; the answer is a plain JSON body", ct)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if !strings.Contains(buf.String(), `"protocolVersion":"2025-06-18"`) {
		t.Errorf("initialize answer: %s", buf.String())
	}

	note := post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	note.Body.Close()
	if note.StatusCode != http.StatusAccepted {
		t.Errorf("a notification got %d, want 202", note.StatusCode)
	}

	get, _ := http.Get(srv.URL + "/mcp")
	get.Body.Close()
	if get.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET got %d; a server without a stream says 405", get.StatusCode)
	}
}

// The log is what the runner reads to decide which agent connected where.
// Its first four fields are fixed; a change here breaks that parsing.
func TestLogLineLayout(t *testing.T) {
	var buf bytes.Buffer
	s := &server{name: "srv-c", log: &buf}
	s.logf("http", "initialize", "client=x/1 protocol=p")
	fields := strings.Fields(buf.String())
	if len(fields) < 5 || fields[1] != "srv-c" || fields[2] != "http" || fields[3] != "initialize" || fields[4] != "client=x/1" {
		t.Errorf("log line = %q, want <time> <server> <transport> <event> <details>", buf.String())
	}
}
