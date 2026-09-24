package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/spec"
)

// TestMain doubles as a fake stdio MCP server: when MCPICK_FAKE_STDIO is set,
// the test binary speaks the protocol on stdin/stdout instead of running tests.
// That exercises the real process plumbing without depending on npx or python.
func TestMain(m *testing.M) {
	if os.Getenv("MCPICK_FAKE_STDIO") == "1" {
		fakeStdioServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeStdioServer() {
	// Noise on stdout before the first response is common in the wild.
	fmt.Println("starting up, not json")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result string
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"stdio-fake","version":"1"}}`
		case "tools/list":
			result = `{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			// Answer slowly and out of order relative to other calls, which
			// is what the single reader has to get right.
			time.Sleep(10 * time.Millisecond)
			result = `{"content":[{"type":"text","text":"ok"}]}`
		case "crash":
			fmt.Fprintln(os.Stderr, "fatal: the server fell over")
			os.Exit(3)
		default:
			fmt.Printf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"nope"}}`+"\n", *req.ID)
			continue
		}
		// Interleave a notification, as real servers do.
		fmt.Println(`{"jsonrpc":"2.0","method":"notifications/message","params":{}}`)
		fmt.Printf(`{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", *req.ID, result)
	}
}

func stdioView(t *testing.T) spec.View {
	t.Helper()
	return spec.View{Transport: "stdio", Command: os.Args[0], Args: []string{"-test.run=^$"},
		Env: map[string]string{"MCPICK_FAKE_STDIO": "1"}}
}

func TestStdioHandshakeAndTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, stdioView(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	info, err := Handshake(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "stdio-fake" {
		t.Errorf("serverInfo = %+v", info)
	}
	tools, err := ListTools(ctx, conn)
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
}

// Concurrent calls on one stdio connection must each get their own answer.
// The first implementation read the pipe from one goroutine per call, so two
// calls raced for each other's responses.
func TestStdioConcurrentCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, stdioView(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := Handshake(ctx, conn); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out map[string]any
			errs <- conn.Call(ctx, "tools/call", map[string]any{"name": "echo"}, &out)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

// A server that dies must fail the pending call with its last words, not hang
// until the timeout.
func TestStdioCrashReportsStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, stdioView(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Call(ctx, "crash", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "fell over") {
		t.Fatalf("err = %v, want the server's stderr", err)
	}
}

func TestDialMissingCommandReturnsNilConn(t *testing.T) {
	conn, err := Dial(context.Background(), spec.View{Transport: "stdio", Command: "/nonexistent/mcp-server"})
	if err == nil {
		t.Fatal("expected an error")
	}
	// A typed nil inside the interface would make `conn != nil` true and a
	// caller's cleanup would dereference it.
	if conn != nil {
		t.Fatal("Dial must return a nil interface on error")
	}
}

// strictServer validates what the client sends the way the reference SDKs do,
// so a misspelled key fails here instead of against someone's real server.
func strictServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var method string
		_ = json.Unmarshal(req["method"], &method)
		if method == "initialize" {
			var p map[string]json.RawMessage
			_ = json.Unmarshal(req["params"], &p)
			for _, k := range []string{"protocolVersion", "capabilities", "clientInfo"} {
				if _, ok := p[k]; !ok {
					t.Errorf("initialize params lack %q: %s", k, req["params"])
				}
			}
		}
		if _, ok := req["id"]; !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"strict","version":"1"},"tools":[]}}`, req["id"])
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInitializeSendsSpecKeys(t *testing.T) {
	srv := strictServer(t)
	res := Probe(context.Background(), "s", map[string]any{"type": "http", "url": srv.URL}, 5*time.Second)
	if !res.OK {
		t.Fatalf("probe failed: %s", res.Err)
	}
	if res.Server.Name != "strict" {
		t.Errorf("serverInfo = %+v", res.Server)
	}
}

// An event stream may carry notifications before the answer; taking the first
// frame as the response was a bug.
func TestHTTPEventStreamSkipsNotifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID *int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"serverInfo\":{\"name\":\"late\"},\"tools\":[{\"name\":\"t\"}]}}\n\n", *req.ID)
	}))
	defer srv.Close()
	res := Probe(context.Background(), "s", map[string]any{"type": "http", "url": srv.URL}, 5*time.Second)
	if !res.OK || res.Server.Name != "late" || res.Tools != 1 {
		t.Fatalf("res = %+v", res)
	}
}

// The deprecated HTTP+SSE transport: a GET stream announces the POST endpoint,
// and every response comes back on the stream.
func TestLegacySSETransport(t *testing.T) {
	events := make(chan string, 16)
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "event: endpoint\ndata: /messages?session=1\n\n")
		flusher.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-events:
				fmt.Fprint(w, e)
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("session") != "1" {
			t.Errorf("POST went to %s, not the announced endpoint", r.URL)
		}
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusAccepted)
		if req.ID == nil {
			return
		}
		result := `{"serverInfo":{"name":"legacy"},"tools":[{"name":"a"},{"name":"b"}]}`
		events <- fmt.Sprintf("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":%s}\n\n", *req.ID, result)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := Probe(context.Background(), "s", map[string]any{"type": "sse", "url": srv.URL + "/sse"}, 5*time.Second)
	if !res.OK || res.Server.Name != "legacy" || res.Tools != 2 {
		t.Fatalf("res = %+v", res)
	}
}

// The latency doctor prints has to be the real one.
func TestProbeReportsLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		var req struct {
			ID *int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, *req.ID)
	}))
	defer srv.Close()
	res := Probe(context.Background(), "s", map[string]any{"type": "http", "url": srv.URL}, 5*time.Second)
	if res.Millis < 30 {
		t.Errorf("latency = %dms, want at least 30ms", res.Millis)
	}
}
