package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Result is what one connection attempt learned about a server.
type Result struct {
	Name    string        `json:"name"`
	OK      bool          `json:"ok"`
	Err     string        `json:"error,omitempty"`
	Tools   int           `json:"tools"`
	Tokens  int           `json:"tokens"`
	Elapsed time.Duration `json:"-"`
	Millis  int64         `json:"ms"`
	Server  ServerInfo    `json:"server,omitempty"`
	Remote  bool          `json:"remote"`
	// Auth says where the credentials came from when mcpick supplied them,
	// e.g. "Claude Code token"; empty when none were needed or found.
	Auth string `json:"auth,omitempty"`

	ToolList []Tool `json:"-"`
}

// EstimateTokens approximates what a server's tool definitions cost in the
// model's context window. Every tool ships its name, description and JSON
// schema on every request, and four bytes per token is the usual rule of thumb
// for English plus JSON punctuation. It is an estimate, not a tokenizer.
func EstimateTokens(tools []Tool) int {
	data, err := json.Marshal(tools)
	if err != nil {
		return 0
	}
	return len(data) / 4
}

// The result is a named return so the deferred timing lands in what the
// caller gets; set on a local copy, it was lost and every latency read 0ms.
func Probe(ctx context.Context, name string, sp map[string]any, timeout time.Duration) (res Result) {
	v := spec.ViewOf(sp)
	res = Result{Name: name, Remote: v.Remote()}
	start := time.Now()
	defer func() {
		res.Elapsed = time.Since(start)
		res.Millis = res.Elapsed.Milliseconds()
	}()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := Dial(ctx, v)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer conn.Close()

	info, err := Handshake(ctx, conn)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Server = info

	tools, err := ListTools(ctx, conn)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.OK = true
	res.ToolList = tools
	res.Tools = len(tools)
	res.Tokens = EstimateTokens(tools)
	return res
}

// ProbeAll contacts every server in the selection concurrently. Servers are
// independent, and a dead one must not hold up the report.
func ProbeAll(ctx context.Context, sel spec.Selection, timeout time.Duration, parallel int) []Result {
	if parallel < 1 {
		parallel = 8
	}
	out := make([]Result, len(sel.Names))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, n := range sel.Names {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = Probe(ctx, n, sel.Specs[n], timeout)
		}(i, n)
	}
	wg.Wait()
	return out
}

// --- Measurement cache ----------------------------------------------------

// The picker shows a token estimate next to every server. Connecting to all of
// them on every launch would defeat the point of a fast picker, so results are
// cached against a hash of the spec: change the spec, lose the cache entry.

type Measurement struct {
	Tools  int    `json:"tools"`
	Tokens int    `json:"tokens"`
	OK     bool   `json:"ok"`
	Err    string `json:"error,omitempty"`
	At     string `json:"at"`
	Spec   string `json:"spec"`
	Auth   string `json:"auth,omitempty"`
}

type Cache struct {
	path    string
	Entries map[string]Measurement `json:"entries"`
}

func Fingerprint(sp map[string]any) string {
	data, err := json.Marshal(normalizeForHash(sp))
	if err != nil {
		return ""
	}
	return fsutil.ShortHash(string(data))
}

// normalizeForHash sorts map keys so the fingerprint does not depend on Go's
// map iteration order.
func normalizeForHash(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := spec.SortedKeys(t)
		out := make([][2]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, [2]any{k, normalizeForHash(t[k])})
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeForHash(e)
		}
		return out
	}
	return v
}

func LoadCache() *Cache {
	c := &Cache{
		path:    filepath.Join(fsutil.StateDir(), "measurements.json"),
		Entries: map[string]Measurement{},
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, c)
	if c.Entries == nil {
		c.Entries = map[string]Measurement{}
	}
	return c
}

func (c *Cache) Get(name string, sp map[string]any) (Measurement, bool) {
	m, ok := c.Entries[name]
	if !ok || m.Spec != Fingerprint(sp) {
		return Measurement{}, false
	}
	return m, true
}

func (c *Cache) Put(name string, sp map[string]any, r Result) {
	c.Entries[name] = Measurement{
		Tools:  r.Tools,
		Tokens: r.Tokens,
		OK:     r.OK,
		Err:    r.Err,
		Auth:   r.Auth,
		At:     time.Now().UTC().Format(time.RFC3339),
		Spec:   Fingerprint(sp),
	}
}

func (c *Cache) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(c.path, append(data, '\n'), 0o600)
}

func HumanTokens(n int) string {
	switch {
	case n <= 0:
		return ""
	case n < 1000:
		return fmt.Sprintf("%dt", n)
	case n < 100_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%dk", n/1000)
	}
}

func SortByCost(rs []Result) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Tokens > rs[j].Tokens })
}

// ErrorKind reduces a connection failure to a word that fits in a table
// column: the HTTP status when there is one, else the class of failure. The
// full message is for when someone asks; the kind is for scanning a list.
func ErrorKind(msg string) string {
	m := strings.ToLower(msg)
	if strings.Contains(m, "token for this server has expired") {
		return "expired"
	}
	if code := httpStatus.FindStringSubmatch(msg); code != nil {
		return code[1]
	}
	for _, k := range []struct{ needle, kind string }{
		{"connection refused", "refused"},
		{"no such host", "dns"},
		{"server misbehaving", "dns"},
		{"deadline exceeded", "timeout"},
		{"timeout", "timeout"},
		{"x509", "tls"},
		{"certificate", "tls"},
		{"tls:", "tls"},
		{"executable file not found", "no cmd"},
		{"no such file", "no cmd"},
		{"no command", "no cmd"},
		{"server exited", "exited"},
		{"is required", "env var"}, // ${VAR:?} unset
		{"connection reset", "reset"},
		{"eof", "closed"},
	} {
		if strings.Contains(m, k.needle) {
			return k.kind
		}
	}
	return "error"
}

var httpStatus = regexp.MustCompile(`HTTP (\d{3})`)

// NeedsLogin says whether an OAuth login would fix the failure.
func NeedsLogin(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "http 401") || strings.Contains(m, "unauthorized") ||
		strings.Contains(m, "invalid_token")
}
