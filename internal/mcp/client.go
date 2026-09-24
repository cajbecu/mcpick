// Package mcp is a small MCP client: stdio, streamable HTTP and the legacy
// HTTP+SSE transport, enough of the protocol to initialize a server and list
// and call its tools. It is hand-written so mcpick tracks the spec, not an SDK.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/spec"
)

// ProtocolVersion is the revision mcpick asks for. Servers that speak a newer
// one answer with theirs; mcpick only needs initialize and tools/*, which have
// not changed shape.
const ProtocolVersion = "2025-06-18"

// SupportedVersions are the revisions the proxy will agree to when a client
// asks for one explicitly, newest first.
var SupportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type RPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int   `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Conn interface {
	Call(ctx context.Context, method string, params any, out any) error
	Notify(ctx context.Context, method string, params any) error
	Close() error
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// Dial opens a connection to one server: a child process for stdio, the legacy
// two-endpoint SSE transport for type "sse", streamable HTTP otherwise.
//
// The nil returns are spelled out on purpose. Returning a nil *stdioConn as an
// Conn produces a non-nil interface, and a caller's `if conn != nil`
// cleanup then dereferences it.
func Dial(ctx context.Context, v spec.View) (Conn, error) {
	switch {
	case !v.Remote():
		c, err := newStdioConn(v)
		if err != nil {
			return nil, err
		}
		return c, nil
	case v.Transport == "sse":
		c, err := newSSEConn(ctx, v)
		if err != nil {
			return nil, err
		}
		return c, nil
	default:
		return newHTTPConn(v), nil
	}
}

// Handshake runs initialize + notifications/initialized, which every server
// requires before it will answer anything else.
func Handshake(ctx context.Context, c Conn) (ServerInfo, error) {
	var res struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	err := c.Call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mcpick", "version": ClientVersion},
	}, &res)
	if err != nil {
		return ServerInfo{}, err
	}
	if err := c.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return res.ServerInfo, err
	}
	return res.ServerInfo, nil
}

func ListTools(ctx context.Context, c Conn) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.Call(ctx, "tools/list", params, &res); err != nil {
			return all, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" || res.NextCursor == cursor || len(res.Tools) == 0 {
			return all, nil
		}
		cursor = res.NextCursor
	}
	return all, fmt.Errorf("tools/list did not finish after 100 pages")
}

func decodeResult(resp RPCResponse, out any) error {
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

// pending routes responses to the call waiting for them. Both transports that
// deliver answers on a shared stream (stdio and legacy SSE) need it: one
// goroutine owns the stream, and nobody else reads from it.
type pending struct {
	mu     sync.Mutex
	nextID int
	wait   map[int]chan RPCResponse
	err    error
}

func newPending() *pending { return &pending{wait: map[int]chan RPCResponse{}} }

func (p *pending) add() (int, chan RPCResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return 0, nil, p.err
	}
	p.nextID++
	ch := make(chan RPCResponse, 1)
	p.wait[p.nextID] = ch
	return p.nextID, ch, nil
}

func (p *pending) drop(id int) {
	p.mu.Lock()
	delete(p.wait, id)
	p.mu.Unlock()
}

func (p *pending) deliver(resp RPCResponse) {
	if resp.ID == nil {
		return // a notification or a server->client request: not ours to answer
	}
	p.mu.Lock()
	ch, ok := p.wait[*resp.ID]
	delete(p.wait, *resp.ID)
	p.mu.Unlock()
	if ok {
		ch <- resp
	}
}

// fail ends every outstanding call. Closing the channels wakes the waiters,
// which then read p.err.
func (p *pending) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
	for id, ch := range p.wait {
		close(ch)
		delete(p.wait, id)
	}
}

func (p *pending) await(ctx context.Context, id int, ch chan RPCResponse, out any) error {
	select {
	case <-ctx.Done():
		p.drop(id)
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			p.mu.Lock()
			err := p.err
			p.mu.Unlock()
			return err
		}
		return decodeResult(resp, out)
	}
}

// --- stdio ----------------------------------------------------------------

type stdioConn struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	writeMu sync.Mutex
	pending *pending
	done    chan struct{}
	stderr  *tailBuffer

	// cmd.Wait may run once; both the reader and Close need its result.
	waitOnce sync.Once
	exited   chan struct{}
}

// waitExit starts the single cmd.Wait and returns a channel closed when it
// returns. Only then is the stderr copy guaranteed complete: os/exec copies
// it from a goroutine of its own, which can still be running when stdout has
// already reached EOF.
func (c *stdioConn) waitExit() <-chan struct{} {
	c.waitOnce.Do(func() {
		go func() {
			_ = c.cmd.Wait()
			close(c.exited)
		}()
	})
	return c.exited
}

func newStdioConn(v spec.View) (*stdioConn, error) {
	if v.Command == "" {
		return nil, fmt.Errorf("no command")
	}
	// Not CommandContext: the server outlives the dial that started it and is
	// stopped by Close, which gives it the grace period the spec asks for.
	cmd := exec.Command(v.Command, v.Args...) //nolint:noctx // lifetime is the connection's, see Close
	cmd.Env = os.Environ()
	for k, val := range v.Env {
		cmd.Env = append(cmd.Env, k+"="+val)
	}
	// npx, uvx and friends start the real server as a grandchild. Its own
	// process group lets Close take the whole tree down, not just the wrapper.
	proc.SetProcessGroup(cmd)

	c := &stdioConn{cmd: cmd, pending: newPending(), done: make(chan struct{}),
		stderr: &tailBuffer{max: 4 << 10}, exited: make(chan struct{})}
	cmd.Stderr = c.stderr
	// A grandchild that inherited stderr and outlives the server would keep
	// the copy — and so Wait — going forever; cap it.
	cmd.WaitDelay = 2 * time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.in = in
	go c.readLoop(out)
	return c, nil
}

func (c *stdioConn) readLoop(out io.Reader) {
	defer close(c.done)
	r := bufio.NewReaderSize(out, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var resp RPCResponse
			// Servers that log to stdout are common enough to tolerate.
			if json.Unmarshal(line, &resp) == nil {
				c.pending.deliver(resp)
			}
		}
		if err != nil {
			// stdout is closed; give the process a moment to exit so its
			// last words on stderr are in the buffer before they are read.
			select {
			case <-c.waitExit():
			case <-time.After(2 * time.Second):
			}
			reason := fmt.Errorf("server exited")
			if tail := strings.TrimSpace(c.stderr.String()); tail != "" {
				reason = fmt.Errorf("server exited: %s", lastLine(tail))
			}
			c.pending.fail(reason)
			return
		}
	}
}

func (c *stdioConn) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.in.Write(append(data, '\n'))
	return err
}

func (c *stdioConn) Notify(ctx context.Context, method string, params any) error {
	return c.write(RPCRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *stdioConn) Call(ctx context.Context, method string, params any, out any) error {
	id, ch, err := c.pending.add()
	if err != nil {
		return err
	}
	if err := c.write(RPCRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		c.pending.drop(id)
		return err
	}
	return c.pending.await(ctx, id, ch, out)
}

// Close follows the shutdown the MCP spec describes for stdio: close stdin,
// give the server a moment, then kill the process group.
func (c *stdioConn) Close() error {
	c.in.Close()
	exited := c.waitExit()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		proc.KillProcessGroup(c.cmd)
		<-exited
	}
	c.pending.fail(fmt.Errorf("connection closed"))
	return nil
}

// tailBuffer keeps the last max bytes written to it, so a server that dies on
// startup can say why without an unbounded buffer.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// --- streamable http ------------------------------------------------------

type httpConn struct {
	url     string
	headers map[string]string
	mu      sync.Mutex
	session string
	nextID  int
}

func newHTTPConn(v spec.View) *httpConn {
	return &httpConn{url: v.URL, headers: v.Headers}
}

// Close ends the session politely; servers that keep per-session state would
// otherwise hold it until it times out.
func (c *httpConn) Close() error {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
	if err != nil {
		return nil
	}
	c.setHeaders(req, session)
	if resp, err := httpClient.Do(req); err == nil {
		resp.Body.Close()
	}
	return nil
}

func (c *httpConn) setHeaders(req *http.Request, session string) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
}

func (c *httpConn) post(ctx context.Context, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	c.setHeaders(req, session)
	return httpClient.Do(req)
}

func (c *httpConn) Notify(ctx context.Context, method string, params any) error {
	resp, err := c.post(ctx, RPCRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	return nil
}

func (c *httpConn) Call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	resp, err := c.post(ctx, RPCRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.mu.Lock()
		c.session = s
		c.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(snippet))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	rr, err := readRPCResponse(resp, id)
	if err != nil {
		return err
	}
	return decodeResult(rr, out)
}

// readRPCResponse handles both answer shapes streamable HTTP allows: a plain
// JSON body, or an event stream. A stream may carry progress notifications and
// server->client requests before the answer, so it is read until the message
// with our id arrives, not just its first frame.
func readRPCResponse(resp *http.Response, id int) (RPCResponse, error) {
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return RPCResponse{}, err
		}
		var rr RPCResponse
		if err := json.Unmarshal(body, &rr); err != nil {
			return RPCResponse{}, fmt.Errorf("server answered with non-JSON: %w", err)
		}
		return rr, nil
	}
	var found *RPCResponse
	err := readSSE(io.LimitReader(resp.Body, 8<<20), func(_, data string) bool {
		var rr RPCResponse
		if json.Unmarshal([]byte(data), &rr) != nil || rr.ID == nil || *rr.ID != id {
			return true
		}
		found = &rr
		return false
	})
	if found != nil {
		return *found, nil
	}
	if err != nil {
		return RPCResponse{}, err
	}
	return RPCResponse{}, fmt.Errorf("event stream ended without a response")
}

// readSSE parses a text/event-stream, calling fn for every event until fn
// returns false or the stream ends.
func readSSE(r io.Reader, fn func(event, data string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	event := ""
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				if !fn(event, strings.Join(data, "\n")) {
					return nil
				}
			}
			event, data = "", nil
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if len(data) > 0 {
		fn(event, strings.Join(data, "\n"))
	}
	return sc.Err()
}

// --- legacy HTTP+SSE (2024-11-05) -----------------------------------------

// sseConn speaks the transport the spec deprecated but many servers still run:
// a long-lived GET stream that first announces a POST endpoint, then carries
// every response.
type sseConn struct {
	endpoint string
	headers  map[string]string
	cancel   context.CancelFunc
	pending  *pending
	body     io.Closer
}

func newSSEConn(ctx context.Context, v spec.View) (*sseConn, error) {
	streamCtx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, v.URL, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, val := range v.Headers {
		req.Header.Set(k, val)
	}
	// The stream lives as long as the connection, so it cannot share the
	// client-wide timeout.
	resp, err := (&http.Client{}).Do(req) //nolint:bodyclose // the stream is the connection; Close closes it
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	c := &sseConn{headers: v.Headers, cancel: cancel, pending: newPending(), body: resp.Body}
	endpoint := make(chan string, 1)
	go func() {
		err := readSSE(resp.Body, func(event, data string) bool {
			if event == "endpoint" {
				select {
				case endpoint <- data:
				default:
				}
				return true
			}
			var rr RPCResponse
			if json.Unmarshal([]byte(data), &rr) == nil {
				c.pending.deliver(rr)
			}
			return true
		})
		if err == nil {
			err = errors.New("event stream closed")
		}
		c.pending.fail(err)
	}()

	select {
	case <-ctx.Done():
		c.Close()
		return nil, ctx.Err()
	case ep := <-endpoint:
		base, err := url.Parse(v.URL)
		if err != nil {
			c.Close()
			return nil, err
		}
		ref, err := url.Parse(ep)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("bad endpoint event %q: %w", ep, err)
		}
		c.endpoint = base.ResolveReference(ref).String()
		return c, nil
	}
}

func (c *sseConn) Close() error {
	c.cancel()
	c.body.Close()
	c.pending.fail(fmt.Errorf("connection closed"))
	return nil
}

func (c *sseConn) post(ctx context.Context, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return nil
}

func (c *sseConn) Notify(ctx context.Context, method string, params any) error {
	return c.post(ctx, RPCRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *sseConn) Call(ctx context.Context, method string, params any, out any) error {
	id, ch, err := c.pending.add()
	if err != nil {
		return err
	}
	if err := c.post(ctx, RPCRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		c.pending.drop(id)
		return err
	}
	return c.pending.await(ctx, id, ch, out)
}

// ClientVersion is reported to servers in clientInfo; main sets it.
var ClientVersion = "dev"
