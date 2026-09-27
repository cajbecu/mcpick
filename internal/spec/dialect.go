// Package spec is the canonical model of one MCP server entry and the
// machinery for writing it in an agent's format: generic JSON and TOML
// dialects that each backend configures, placeholder expansion, and the
// splicing of generated server blocks into files the user owns. The formats
// of particular agents live with their backends, in internal/backend.
package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// View is the canonical shape mcpick converts every dialect into before
// emitting it again. Tools disagree on the URL key (url / httpUrl / serverUrl),
// on whether the command line is a string plus args or a single array, and on
// what the header table is called, so round-tripping through one struct is what
// makes a catalog cross-tool.
type View struct {
	Transport string // stdio, http or sse
	URL       string
	Headers   map[string]string
	Command   string
	Args      []string
	Env       map[string]string
	Extra     map[string]any
}

// knownSpecKeys are consumed by ViewOf; everything else is carried in Extra.
var knownSpecKeys = map[string]bool{
	"type": true, "transport": true, "url": true, "httpUrl": true, "serverUrl": true,
	"headers": true, "http_headers": true, "command": true, "args": true,
	"env": true, "environment": true, "enabled": true,
}

// owners records which agents understand a catalog key that is not part of
// the common shape. Such a key passes through to those agents and is dropped
// for every other: a Codex-only key in a Claude config is at best ignored and
// at worst fails validation for the whole file. Backends declare their keys
// when they register.
var owners = map[string]map[string]bool{}

// Own declares that key is understood by agent.
func Own(agent string, keys ...string) {
	for _, k := range keys {
		if owners[k] == nil {
			owners[k] = map[string]bool{}
		}
		owners[k][agent] = true
	}
}

// ExtraAllowed reports whether key may be written for agent: a key nobody
// claims passes everywhere (a newer key mcpick has not heard of), a claimed
// key only to the agents that claim it.
func ExtraAllowed(agent, key string) bool {
	o := owners[key]
	return len(o) == 0 || o[agent]
}

// Extras are the entries of v.Extra that may be written for agent.
func Extras(agent string, v View) map[string]any {
	out := map[string]any{}
	for k, val := range v.Extra {
		if ExtraAllowed(agent, k) {
			out[k] = val
		}
	}
	return out
}

func ViewOf(spec map[string]any) View {
	v := View{Headers: map[string]string{}, Env: map[string]string{}, Extra: map[string]any{}}

	for _, k := range []string{"url", "httpUrl", "serverUrl"} {
		if s, ok := spec[k].(string); ok && s != "" {
			v.URL = s
			break
		}
	}
	switch c := spec["command"].(type) {
	case string:
		v.Command = c
	case []any:
		// opencode packs the whole command line into one array
		for i, a := range c {
			if i == 0 {
				v.Command = fmt.Sprint(a)
				continue
			}
			v.Args = append(v.Args, fmt.Sprint(a))
		}
	}
	if args, ok := spec["args"].([]any); ok {
		for _, a := range args {
			v.Args = append(v.Args, fmt.Sprint(a))
		}
	}
	for _, k := range []string{"env", "environment"} {
		if m, ok := spec[k].(map[string]any); ok {
			for kk, vv := range m {
				v.Env[kk] = fmt.Sprint(vv)
			}
		}
	}
	for _, k := range []string{"headers", "http_headers"} {
		if m, ok := spec[k].(map[string]any); ok {
			for kk, vv := range m {
				v.Headers[kk] = fmt.Sprint(vv)
			}
		}
	}

	t, _ := spec["type"].(string)
	if t == "" {
		t, _ = spec["transport"].(string)
	}
	switch strings.ToLower(t) {
	case "local", "stdio":
		v.Transport = "stdio"
	case "sse":
		v.Transport = "sse"
	case "remote", "http", "streamable-http", "streamablehttp", "streamable_http":
		v.Transport = "http"
	}
	if v.Transport == "" {
		switch {
		case v.URL == "":
			v.Transport = "stdio"
		case spec["httpUrl"] != nil:
			v.Transport = "http"
		case spec["url"] != nil && strings.HasSuffix(strings.TrimRight(v.URL, "/"), "/sse"):
			// Gemini spells SSE as a bare `url`; the path is the only hint.
			v.Transport = "sse"
		default:
			v.Transport = "http"
		}
	}

	for k, val := range spec {
		if !knownSpecKeys[k] {
			v.Extra[k] = val
		}
	}
	return v
}

func (v View) Remote() bool { return v.URL != "" }

func SortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// StringMap converts a string map for JSON encoding.
func StringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// AnySlice converts a string slice for JSON encoding.
func AnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// Dialect renders a selection in one agent's config format.
type Dialect interface {
	Emit(sel Selection) ([]byte, error)
}

// Lossy is implemented by a dialect that cannot carry everything a catalog
// entry can hold; it names what would silently be lost.
type Lossy interface {
	Lossy(sel Selection) []string
}

// JSON is the shape most agents share: a map of servers under one key, each
// with a URL or a command line. What differs is configured here.
type JSON struct {
	Agent  string // whose keys (see Own) this dialect writes
	TopKey string // "mcpServers", "mcp_servers", ...
	// Type writes the transport as "type".
	Type bool
	// URLKey holds a remote server's address: "url" when empty. SSEURLKey,
	// when set, is used for legacy SSE servers instead (Gemini tells the two
	// transports apart by the key alone).
	URLKey, SSEURLKey string
	// Defaults are keys the agent requires that others do not have; a
	// catalog value for the key wins.
	Defaults map[string]any
}

func (d JSON) urlKey(transport string) string {
	if transport == "sse" && d.SSEURLKey != "" {
		return d.SSEURLKey
	}
	if d.URLKey != "" {
		return d.URLKey
	}
	return "url"
}

// Entry renders one server.
func (d JSON) Entry(v View) map[string]any {
	out := map[string]any{}
	for k, val := range d.Defaults {
		out[k] = val
	}
	for k, val := range Extras(d.Agent, v) {
		out[k] = val
	}
	if d.Type {
		out["type"] = v.Transport
	}
	if v.Remote() {
		out[d.urlKey(v.Transport)] = v.URL
		if len(v.Headers) > 0 {
			out["headers"] = StringMap(v.Headers)
		}
	} else {
		out["command"] = v.Command
		if len(v.Args) > 0 {
			out["args"] = AnySlice(v.Args)
		}
	}
	if len(v.Env) > 0 {
		out["env"] = StringMap(v.Env)
	}
	return out
}

func (d JSON) Emit(sel Selection) ([]byte, error) {
	servers := map[string]any{}
	for _, n := range sel.Names {
		servers[n] = d.Entry(ViewOf(sel.Specs[n]))
	}
	return MarshalIndented(map[string]any{d.TopKey: servers})
}

// MarshalIndented is JSON as agents' config files are written: two-space
// indent, trailing newline.
func MarshalIndented(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Claude is Claude Code's own format. It is also what mcpick writes for an
// agent it has no adapter for, and the shape of its catalog.
var Claude = JSON{Agent: "claude", TopKey: "mcpServers", Type: true}

// TOML is the [mcp_servers.<name>] format Codex and Grok use.
type TOML struct {
	Agent string // whose keys (see Own) this dialect writes
	// Headers names the header table: Codex calls it http_headers, Grok
	// headers.
	Headers string
	// NoSSE marks an agent that cannot speak the legacy SSE transport.
	NoSSE bool
}

func (d TOML) Emit(sel Selection) ([]byte, error) {
	var b strings.Builder
	for _, n := range sel.Names {
		v := ViewOf(sel.Specs[n])
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", tomlKey(n))
		if v.Remote() {
			fmt.Fprintf(&b, "url = %s\n", tomlString(v.URL))
		} else {
			fmt.Fprintf(&b, "command = %s\n", tomlString(v.Command))
			if len(v.Args) > 0 {
				fmt.Fprintf(&b, "args = %s\n", tomlArray(v.Args))
			}
		}
		for _, k := range SortedKeys(v.Extra) {
			if !ExtraAllowed(d.Agent, k) {
				continue
			}
			if val, ok := tomlScalar(v.Extra[k]); ok {
				fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), val)
			}
		}
		if len(v.Env) > 0 {
			fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", tomlKey(n))
			for _, k := range SortedKeys(v.Env) {
				fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlString(v.Env[k]))
			}
		}
		if v.Remote() && len(v.Headers) > 0 {
			fmt.Fprintf(&b, "\n[mcp_servers.%s.%s]\n", tomlKey(n), d.Headers)
			for _, k := range SortedKeys(v.Headers) {
				fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlString(v.Headers[k]))
			}
		}
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// Lossy names the servers the agent will not be able to reach.
func (d TOML) Lossy(sel Selection) []string {
	if !d.NoSSE {
		return nil
	}
	var out []string
	for _, n := range sel.Names {
		if ViewOf(sel.Specs[n]).Transport == "sse" {
			out = append(out, fmt.Sprintf(
				"%s: %s speaks stdio and streamable HTTP only; this legacy SSE server will not connect", n, d.Agent))
		}
	}
	return out
}

func tomlScalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return tomlString(t), true
	case bool:
		return fmt.Sprint(t), true
	case int, int64, float64:
		return fmt.Sprint(t), true
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		return tomlArray(parts), true
	}
	return "", false
}

func tomlArray(in []string) string {
	parts := make([]string, len(in))
	for i, a := range in {
		parts[i] = tomlString(a)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func bareTOMLKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !bareKeyRune(r) {
			return false
		}
	}
	return true
}

func bareKeyRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

func tomlKey(s string) string {
	if bareTOMLKey(s) {
		return s
	}
	return tomlString(s)
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// --- splicing into files the user owns --------------------------------------

var tomlTableHeader = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)

func isServerTable(name string) bool {
	return name == "mcp_servers" || strings.HasPrefix(name, "mcp_servers.")
}

// splitTOML separates the [mcp_servers...] tables from everything else. A
// line-based cut is enough because TOML table headers always start a line;
// multi-line strings that happen to contain one are the known blind spot, and
// no MCP config mcpick has seen uses them.
func splitTOML(data []byte) (rest, servers string) {
	var keep, srv []string
	inServers := false
	for _, line := range strings.Split(string(data), "\n") {
		if m := tomlTableHeader.FindStringSubmatch(line); m != nil {
			inServers = isServerTable(m[1])
		}
		if inServers {
			srv = append(srv, line)
		} else {
			keep = append(keep, line)
		}
	}
	return strings.TrimRight(strings.Join(keep, "\n"), "\n"),
		strings.TrimRight(strings.Join(srv, "\n"), "\n")
}

func joinTOML(rest, servers string) []byte {
	switch {
	case rest == "":
		return []byte(servers + "\n")
	case servers == "":
		return []byte(rest + "\n")
	}
	return []byte(rest + "\n\n" + servers + "\n")
}

func TOMLServerNames(data []byte) []string {
	_, servers := splitTOML(data)
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(servers, "\n") {
		m := tomlTableHeader.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := strings.TrimPrefix(m[1], "mcp_servers.")
		if name == m[1] {
			continue
		}
		if strings.HasPrefix(name, `"`) {
			if end := strings.Index(name[1:], `"`); end >= 0 {
				name = name[1 : end+1]
			}
		} else if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Splice puts the generated server block into a copy of the user's
// config, leaving every other setting exactly as they wrote it.
func Splice(original, generated []byte, topKey string, toml bool) ([]byte, error) {
	if toml {
		rest, _ := splitTOML(original)
		return joinTOML(rest, strings.TrimRight(string(generated), "\n")), nil
	}
	return spliceJSON(original, generated, topKey)
}

// Restore is the inverse, applied to a file the agent may have edited
// while it ran: its edits stay, the server block goes back to the original's.
func Restore(current, original []byte, topKey string, toml bool) ([]byte, error) {
	if toml {
		rest, _ := splitTOML(current)
		_, servers := splitTOML(original)
		return joinTOML(rest, servers), nil
	}
	top, order, err := DecodeOrdered(current)
	if err != nil {
		return nil, err
	}
	orig, _, err := DecodeOrdered(original)
	if err != nil {
		return nil, err
	}
	for _, k := range []string{topKey, "$schema"} {
		if v, ok := orig[k]; ok {
			top[k] = v
		} else {
			delete(top, k)
		}
	}
	return MarshalOrdered(top, order)
}

func spliceJSON(original, generated []byte, topKey string) ([]byte, error) {
	top, order, err := DecodeOrdered(original)
	if err != nil {
		return nil, fmt.Errorf("existing config is not valid JSON: %w", err)
	}
	gen := map[string]json.RawMessage{}
	if err := json.Unmarshal(generated, &gen); err != nil {
		return nil, err
	}
	for k, v := range gen {
		// The server block and the schema pointer are mcpick's to replace.
		// Anything else a dialect emits is a document-level key the agent
		// insists on (muse's schema_version): it fills a gap in a new file
		// and never overrides what the user's file says.
		if _, have := top[k]; k == topKey || k == "$schema" || !have {
			top[k] = v
		}
	}
	return MarshalOrdered(top, order)
}

// DecodeOrdered reads a JSON object and the order of its keys. An empty input
// is an empty object: the file did not exist yet.
func DecodeOrdered(data []byte) (map[string]json.RawMessage, []string, error) {
	top := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return top, nil, nil
	}
	order, err := TopLevelOrder(data)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, nil, err
	}
	return top, order, nil
}
