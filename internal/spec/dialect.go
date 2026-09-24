// Package spec is the canonical model of one MCP server entry and every
// dialect mcpick reads and writes: Claude, Codex and Grok TOML, Gemini,
// Antigravity, Copilot, opencode, Muse and pi. It also owns placeholder
// expansion and the splicing of generated server blocks into files the user
// owns.
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

// dialectKeys are extras that mean something to exactly one agent. They pass
// through to that agent and are dropped for every other: a Codex-only key in a
// Claude config is at best ignored and at worst fails validation for the whole
// file.
var dialectKeys = map[string]map[string]bool{
	"codex": {
		"bearer_token_env_var": true, "env_http_headers": true, "env_vars": true,
		"startup_timeout_sec": true, "tool_timeout_sec": true, "enabled_tools": true,
		"disabled_tools": true, "required": true, "auth": true, "cwd": true,
		"default_tools_approval_mode": true, "http_headers_helper": true,
	},
	"gemini": {
		"timeout": true, "trust": true, "includeTools": true, "excludeTools": true,
		"authProviderType": true, "targetAudience": true, "targetServiceAccount": true,
		"cwd": true, "oauth": true,
	},
	"copilot": {"tools": true},
	"pi": {
		"lifecycle": true, "idleTimeout": true, "directTools": true, "debug": true,
		"inheritEnv": true, "literalEnv": true, "protocolVersion": true,
	},
}

// extraAllowed reports whether key may be emitted for dialect: keys nobody
// claims pass everywhere (a newer key mcpick has not heard of), keys a dialect
// owns pass only there.
func extraAllowed(dialect, key string) bool {
	owned := false
	for d, keys := range dialectKeys {
		if keys[key] {
			if d == dialect {
				return true
			}
			owned = true
		}
	}
	return !owned
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

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// jsonDialect describes one JSON-shaped agent config.
type jsonDialect struct {
	name     string
	topKey   string
	withType bool
	// defaults are keys the agent requires that other dialects do not have;
	// a catalog value for the key wins.
	defaults map[string]any
	// urlKey picks the key for a remote URL; Gemini uses a different one per
	// transport.
	urlKey func(transport string) string
}

func fixedURL(key string) func(string) string { return func(string) string { return key } }

var (
	dialectClaude = jsonDialect{name: "claude", topKey: "mcpServers", withType: true, urlKey: fixedURL("url")}
	dialectGemini = jsonDialect{name: "gemini", topKey: "mcpServers", urlKey: func(t string) string {
		if t == "sse" {
			return "url"
		}
		return "httpUrl"
	}}
	dialectAntigravity = jsonDialect{name: "antigravity", topKey: "mcpServers", urlKey: fixedURL("serverUrl")}
	dialectMuse        = jsonDialect{name: "muse", topKey: "mcp_servers", withType: true, urlKey: fixedURL("url")}
	dialectPi          = jsonDialect{name: "pi", topKey: "mcpServers", urlKey: fixedURL("url")}
	// Copilot's user config lists the tools to expose; without "tools" the
	// server connects and offers nothing.
	dialectCopilot = jsonDialect{name: "copilot", topKey: "mcpServers", withType: true,
		urlKey: fixedURL("url"), defaults: map[string]any{"tools": []any{"*"}}}
)

func (d jsonDialect) entry(v View) map[string]any {
	out := map[string]any{}
	for k, val := range d.defaults {
		out[k] = val
	}
	for k, val := range v.Extra {
		if extraAllowed(d.name, k) {
			out[k] = val
		}
	}
	if d.withType {
		out["type"] = v.Transport
	}
	if v.Remote() {
		out[d.urlKey(v.Transport)] = v.URL
		if len(v.Headers) > 0 {
			out["headers"] = stringMap(v.Headers)
		}
	} else {
		out["command"] = v.Command
		if len(v.Args) > 0 {
			out["args"] = toAnySlice(v.Args)
		}
	}
	if len(v.Env) > 0 {
		out["env"] = stringMap(v.Env)
	}
	return out
}

func (d jsonDialect) emit(sel Selection) ([]byte, error) {
	servers := map[string]any{}
	for _, n := range sel.Names {
		servers[n] = d.entry(ViewOf(sel.Specs[n]))
	}
	data, err := json.MarshalIndent(map[string]any{d.topKey: servers}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func EmitClaude(sel Selection) ([]byte, error)      { return dialectClaude.emit(sel) }
func EmitGemini(sel Selection) ([]byte, error)      { return dialectGemini.emit(sel) }
func EmitAntigravity(sel Selection) ([]byte, error) { return dialectAntigravity.emit(sel) }
func EmitMuse(sel Selection) ([]byte, error)        { return dialectMuse.emit(sel) }
func EmitPi(sel Selection) ([]byte, error)          { return dialectPi.emit(sel) }
func EmitCopilot(sel Selection) ([]byte, error)     { return dialectCopilot.emit(sel) }

func EmitOpencode(sel Selection) ([]byte, error) {
	servers := map[string]any{}
	for _, n := range sel.Names {
		v := ViewOf(sel.Specs[n])
		entry := map[string]any{"enabled": true}
		for k, val := range v.Extra {
			if extraAllowed("opencode", k) {
				entry[k] = val
			}
		}
		if v.Remote() {
			entry["type"] = "remote"
			entry["url"] = v.URL
			if len(v.Headers) > 0 {
				entry["headers"] = stringMap(v.Headers)
			}
		} else {
			entry["type"] = "local"
			entry["command"] = append([]any{v.Command}, toAnySlice(v.Args)...)
			if len(v.Env) > 0 {
				entry["environment"] = stringMap(v.Env)
			}
		}
		servers[n] = entry
	}
	data, err := json.MarshalIndent(map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"mcp":     servers,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// emitTOML writes the [mcp_servers.<name>] dialect used by Codex and Grok.
// Codex spells the header table http_headers; Grok uses headers.
func emitTOML(sel Selection, dialect, headerTable string) ([]byte, error) {
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
			if !extraAllowed(dialect, k) {
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
			fmt.Fprintf(&b, "\n[mcp_servers.%s.%s]\n", tomlKey(n), headerTable)
			for _, k := range SortedKeys(v.Headers) {
				fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlString(v.Headers[k]))
			}
		}
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

func EmitCodex(sel Selection) ([]byte, error) { return emitTOML(sel, "codex", "http_headers") }
func EmitGrok(sel Selection) ([]byte, error)  { return emitTOML(sel, "grok", "headers") }

// Lossy reports what a dialect cannot carry, so a server that quietly
// disappears becomes a message instead of a mystery.
func Lossy(tool string, sel Selection) []string {
	var out []string
	for _, n := range sel.Names {
		v := ViewOf(sel.Specs[n])
		if tool == "codex" && v.Transport == "sse" {
			out = append(out, fmt.Sprintf(
				"%s: codex speaks stdio and streamable HTTP only; this legacy SSE server will not connect", n))
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
		if k == topKey || k == "$schema" {
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
