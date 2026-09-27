// fakemcp is a stand-in MCP server for the end-to-end tests: one tool,
// `nonce`, that returns a value chosen at start-up, and a log of every
// initialize, tools/list and tools/call it receives, with the client that
// sent it. The log is the proof that an agent connected to exactly the
// servers mcpick handed it, without any model or credential involved.
//
// It speaks stdio by default and streamable HTTP with --http. The protocol
// handling is written from the specification, not shared with mcpick's
// client: a fake that mirrored the client's assumptions would prove nothing.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// server is the protocol state shared by both transports.
type server struct {
	name  string
	nonce string
	mu    sync.Mutex
	log   io.Writer
}

func newServer(name, logPath string) (*server, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	s := &server{name: name, nonce: hex.EncodeToString(buf), log: io.Discard}
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		s.log = f
	}
	return s, nil
}

// logf writes one line per protocol event. The format is what the runner
// parses: "<time> <server> <transport> <event> <details>".
func (s *server) logf(transport, event, details string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.log, "%s %s %s %s %s\n", time.Now().UTC().Format(time.RFC3339Nano), s.name, transport, event, details)
}

// handle answers one message. A nil response means a notification, which
// gets no answer.
func (s *server) handle(transport string, req request) *response {
	reply := func(result any) *response {
		return &response{JSONRPC: "2.0", ID: req.ID, Result: result}
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.logf(transport, "initialize", fmt.Sprintf("client=%s/%s protocol=%s", p.ClientInfo.Name, p.ClientInfo.Version, p.ProtocolVersion))
		version := p.ProtocolVersion
		if version == "" {
			version = "2025-06-18"
		}
		return reply(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fakemcp-" + s.name, "version": "0"},
		})
	case "tools/list":
		s.logf(transport, "tools/list", "")
		return reply(map[string]any{"tools": []map[string]any{{
			"name":        "nonce",
			"description": "Returns the nonce of the fake server " + s.name,
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		}}})
	case "tools/call":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.logf(transport, "tools/call", "tool="+p.Name)
		if p.Name != "nonce" {
			return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "unknown tool " + p.Name}}
		}
		return reply(map[string]any{"content": []map[string]any{{"type": "text", "text": s.nonce}}})
	case "ping":
		return reply(map[string]any{})
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		if req.Method != "" {
			s.logf(transport, "notification", req.Method)
		}
		return nil
	}
	// Anything else a client may ask for (resources, prompts) is honestly
	// absent rather than faked.
	return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}
}

// serveStdio reads newline-delimited JSON-RPC from stdin until EOF.
func (s *server) serveStdio() error {
	s.logf("stdio", "start", fmt.Sprintf("pid=%d", os.Getpid()))
	r := bufio.NewReaderSize(os.Stdin, 1<<20)
	enc := json.NewEncoder(os.Stdout)
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var req request
			if jerr := json.Unmarshal(line, &req); jerr != nil {
				s.logf("stdio", "bad-json", jerr.Error())
			} else if resp := s.handle("stdio", req); resp != nil {
				if err := enc.Encode(resp); err != nil {
					return err
				}
			}
		}
		if err != nil {
			s.logf("stdio", "eof", "")
			return nil
		}
	}
}

// handler is the streamable HTTP transport: every message is a POST, the
// answer is a plain JSON body, notifications get 202. A session id is issued
// so clients that require one are satisfied; nothing is kept per session.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			s.logf("http", "session-end", "")
			w.WriteHeader(http.StatusOK)
			return
		case http.MethodGet:
			// No server-initiated stream: the spec lets a server decline it.
			http.Error(w, "no event stream", http.StatusMethodNotAllowed)
			return
		case http.MethodPost:
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		resp := s.handle("http", req)
		if req.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "fake-"+s.nonce)
		}
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return mux
}

func (s *server) serveHTTP(addr string) error {
	s.logf("http", "start", "addr="+addr)
	srv := &http.Server{Addr: addr, Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

func main() {
	name := flag.String("name", "fake", "server name, written in every log line")
	logPath := flag.String("log", os.Getenv("FAKEMCP_LOG"), "append protocol events to this file")
	httpAddr := flag.String("http", "", "serve streamable HTTP on this address (path /mcp) instead of stdio")
	flag.Parse()

	s, err := newServer(*name, *logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakemcp:", err)
		os.Exit(1)
	}
	if *httpAddr != "" {
		err = s.serveHTTP(*httpAddr)
	} else {
		err = s.serveStdio()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakemcp:", err)
		os.Exit(1)
	}
}
