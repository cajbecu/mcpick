// Package proxy runs mcpick as a single MCP server in front of the selected
// upstream servers, for agents mcpick cannot launch with a generated config.
package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Separator joins server and tool names. It matches the convention Grok and
// the Devin CLI use for namespaced MCP tools.
const Separator = "__"

// maxToolName is the limit clients enforce on tool names (Claude, OpenAI and
// Gemini all reject longer ones); a longer name makes the whole list fail.
const maxToolName = 64

// retryDead is how long a failed upstream is left alone before the proxy
// tries it again. Long enough not to hammer a dead server on every request,
// short enough that one that was merely restarting comes back.
const retryDead = 30 * time.Second

type route struct{ server, tool string }

// Proxy fans one MCP connection out to the selected upstream servers.
type Proxy struct {
	sel     spec.Selection
	timeout time.Duration

	mu      sync.Mutex
	conns   map[string]mcp.Conn
	dialing map[string]*sync.Mutex
	dead    map[string]deadline
	routes  map[string]route // exposed tool name -> upstream
}

type deadline struct {
	why   string
	until time.Time
}

// New returns a proxy for sel. Upstreams are connected lazily.
func New(sel spec.Selection, timeout time.Duration) *Proxy {
	return &Proxy{
		sel:     sel,
		timeout: timeout,
		conns:   map[string]mcp.Conn{},
		dialing: map[string]*sync.Mutex{},
		dead:    map[string]deadline{},
		routes:  map[string]route{},
	}
}

// Close disconnects every upstream.
func (p *Proxy) Close() {
	p.mu.Lock()
	conns := p.conns
	p.conns = map[string]mcp.Conn{}
	p.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

func (p *Proxy) dialLock(name string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.dialing[name]
	if !ok {
		m = &sync.Mutex{}
		p.dialing[name] = m
	}
	return m
}

// connect opens an upstream on first use. Concurrent requests for the same
// server wait for one dial instead of each starting their own process.
func (p *Proxy) connect(ctx context.Context, name string) (mcp.Conn, error) {
	lock := p.dialLock(name)
	lock.Lock()
	defer lock.Unlock()

	p.mu.Lock()
	if c, ok := p.conns[name]; ok {
		p.mu.Unlock()
		return c, nil
	}
	if d, ok := p.dead[name]; ok && time.Now().Before(d.until) {
		p.mu.Unlock()
		return nil, errors.New(d.why)
	}
	sp, ok := p.sel.Specs[name]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown server %q", name)
	}

	dialCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	conn, err := mcp.Dial(dialCtx, spec.ViewOf(sp))
	if err == nil {
		if _, err = mcp.Handshake(dialCtx, conn); err != nil {
			conn.Close()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.dead[name] = deadline{why: err.Error(), until: time.Now().Add(retryDead)}
		return nil, err
	}
	delete(p.dead, name)
	p.conns[name] = conn
	return conn, nil
}

// drop forgets an upstream whose transport failed, so the next request
// reconnects instead of failing forever against a process that has exited.
func (p *Proxy) drop(name string, conn mcp.Conn) {
	p.mu.Lock()
	if p.conns[name] == conn {
		delete(p.conns, name)
	}
	p.mu.Unlock()
	conn.Close()
}

func isTransportError(err error) bool {
	var rpcErr *mcp.RPCError
	return err != nil && !errors.As(err, &rpcErr)
}

// exposedName builds the name a client sees. Clients accept
// [A-Za-z0-9_-]{1,64}; anything else is replaced, and a name that is still too
// long is cut and suffixed with a hash so two long names cannot collide.
func exposedName(server, tool string) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
				return r
			}
			return '_'
		}, s)
	}
	name := clean(server) + Separator + clean(tool)
	if len(name) <= maxToolName {
		return name
	}
	h := fsutil.ShortHash(server + "\x00" + tool)[:8]
	return name[:maxToolName-len(h)-1] + "_" + h
}

// ListTools lists every reachable upstream's tools, concurrently, under their
// exposed names. A broken upstream hides its own tools; it does not break the
// list.
func (p *Proxy) ListTools(ctx context.Context) []mcp.Tool {
	results := make([][]mcp.Tool, len(p.sel.Names))
	var wg sync.WaitGroup
	for i, name := range p.sel.Names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			conn, err := p.connect(ctx, name)
			if err != nil {
				return
			}
			callCtx, cancel := context.WithTimeout(ctx, p.timeout)
			defer cancel()
			tools, err := mcp.ListTools(callCtx, conn)
			if isTransportError(err) {
				p.drop(name, conn)
			}
			results[i] = tools
		}(i, name)
	}
	wg.Wait()

	routes := map[string]route{}
	var out []mcp.Tool
	for i, tools := range results {
		for _, t := range tools {
			exposed := exposedName(p.sel.Names[i], t.Name)
			routes[exposed] = route{server: p.sel.Names[i], tool: t.Name}
			t.Name = exposed
			out = append(out, t)
		}
	}
	p.mu.Lock()
	p.routes = routes
	p.mu.Unlock()
	return out
}

// CallTool routes one call to the upstream that owns the tool.
func (p *Proxy) CallTool(ctx context.Context, exposed string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	r, ok := p.routes[exposed]
	p.mu.Unlock()
	if !ok {
		// A client may call without listing first (it cached the list from
		// an earlier session); fall back to splitting the name.
		server, tool, found := strings.Cut(exposed, Separator)
		if !found {
			return nil, fmt.Errorf("tool %q is not namespaced as server%stool", exposed, Separator)
		}
		r = route{server: server, tool: tool}
	}
	conn, err := p.connect(ctx, r.server)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	params := map[string]any{"name": r.tool}
	if len(args) > 0 {
		params["arguments"] = args
	}
	var raw json.RawMessage
	if err := conn.Call(callCtx, "tools/call", params, &raw); err != nil {
		if isTransportError(err) {
			p.drop(r.server, conn)
		}
		return nil, err
	}
	return raw, nil
}

// negotiate answers a client's protocol version with the same one when mcpick
// speaks it, as the spec asks, and with mcpick's newest otherwise.
func negotiate(requested string) string {
	for _, v := range mcp.SupportedVersions {
		if v == requested {
			return v
		}
	}
	return mcp.ProtocolVersion
}

// Handle answers one JSON-RPC message. Notifications (no id) return nil.
func (p *Proxy) Handle(ctx context.Context, raw json.RawMessage) *mcp.RPCResponse {
	var req struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return &mcp.RPCResponse{JSONRPC: "2.0", Error: &mcp.RPCError{Code: -32700, Message: "parse error"}}
	}
	if req.ID == nil {
		return nil
	}
	resp := &mcp.RPCResponse{JSONRPC: "2.0", ID: req.ID}
	fail := func(code int, msg string) *mcp.RPCResponse {
		resp.Error = &mcp.RPCError{Code: code, Message: msg}
		return resp
	}
	ok := func(v any) *mcp.RPCResponse {
		data, err := json.Marshal(v)
		if err != nil {
			return fail(-32603, err.Error())
		}
		resp.Result = data
		return resp
	}

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return ok(map[string]any{
			"protocolVersion": negotiate(params.ProtocolVersion),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mcpick", "version": mcp.ClientVersion},
			"instructions": fmt.Sprintf(
				"Aggregated MCP servers: %s. Tools are namespaced <server>%s<tool>.",
				strings.Join(p.sel.Names, ", "), Separator),
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		tools := p.ListTools(ctx)
		if tools == nil {
			tools = []mcp.Tool{}
		}
		return ok(map[string]any{"tools": tools})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fail(-32602, err.Error())
		}
		res, err := p.CallTool(ctx, params.Name, params.Arguments)
		if err != nil {
			return fail(-32603, err.Error())
		}
		resp.Result = res
		return resp
	case "resources/list":
		return ok(map[string]any{"resources": []any{}})
	case "resources/templates/list":
		return ok(map[string]any{"resourceTemplates": []any{}})
	case "prompts/list":
		return ok(map[string]any{"prompts": []any{}})
	default:
		return fail(-32601, "method not found: "+req.Method)
	}
}

// ServeStdio is the default transport: newline-delimited JSON-RPC on in and
// out, which every MCP client can speak.
func (p *Proxy) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		resp := p.Handle(ctx, json.RawMessage(line))
		if resp == nil {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

// CheckAddr refuses a listen address that is reachable from other machines.
// The proxy has no authentication and forwards every call with the upstreams'
// credentials, so anything but loopback hands those credentials to the
// network. MCPICK_SERVE_ALLOW_REMOTE=1 overrides it for someone who has put
// their own auth in front.
func CheckAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q: %w", addr, err)
	}
	if os.Getenv("MCPICK_SERVE_ALLOW_REMOTE") == "1" {
		return nil
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("--addr %s is not a loopback address; the proxy has no authentication "+
		"(bind 127.0.0.1, or set MCPICK_SERVE_ALLOW_REMOTE=1 behind your own auth)", addr)
}

// allowedOrigin enforces the Origin check the MCP spec requires of HTTP
// servers. Without it, any web page the user visits can drive a localhost
// server through DNS rebinding.
func allowedOrigin(origin string) bool {
	if origin == "" {
		return true // not a browser
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler is the streamable-HTTP endpoint, exposed for tests.
func (p *Proxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !allowedOrigin(r.Header.Get("Origin")) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := p.Handle(r.Context(), body)
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return mux
}

// ServeHTTP listens on addr until ctx is done.
func (p *Proxy) ServeHTTP(ctx context.Context, addr string) error {
	if err := CheckAddr(addr); err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: p.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	fmt.Fprintf(os.Stderr, "mcpick: serving %d server(s) on http://%s/mcp\n", len(p.sel.Names), addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
