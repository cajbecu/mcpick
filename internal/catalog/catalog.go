// Package catalog merges the MCP servers mcpick can see — its own catalog
// files, Claude Code's local and user config, and Claude Code plugins —
// into one ordered list, and edits the files those servers came from.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Origins say where a server was defined, which decides both precedence and
// what deleting it means. They are named as Claude Code names its scopes.
const (
	OriginProject = "project" // .mcp.yaml / .mcp.json in the repository
	OriginLocal   = "local"   // ~/.claude.json projects[<root>]
	OriginUser    = "user"    // ~/.claude.json mcpServers
	OriginPlugin  = "plugin"  // an enabled Claude Code plugin; read-only
)

// Names are the files mcpick reads as its own catalog, in precedence order.
// .mcp.json is the file the rest of the ecosystem already writes; .mcp.yaml is
// preferred for new entries because it takes comments.
var Names = []string{".mcp.yaml", ".mcp.json"}

// Server is one entry of the merged catalog.
type Server struct {
	Name   string
	Origin string
	Spec   map[string]any
	// Disabled means the server is listed in disabledMcpServers. For a
	// server of its own, Claude Code then refuses it even when it arrives
	// through --mcp-config. A plugin server is listed under its namespaced
	// name, which does not match the plain name mcpick hands over, so Claude
	// would load it: the flag is how mcpick knows the user switched it off.
	Disabled bool
	// DisabledAs is the entry in disabledMcpServers that disabled it — the
	// plain name, or plugin:<plugin>:<server> — which is what has to be
	// removed to enable it again.
	DisabledAs string
	// Source is the file the entry was read from, for display.
	Source string
	// ClaudeName is what Claude Code calls the server: its own name, or
	// plugin:<plugin>:<server> for a plugin's. Claude keys its OAuth tokens
	// and its disabled list by it.
	ClaudeName string
}

// ClaudeNames maps each server to the name Claude Code knows it by.
func (c *Catalog) ClaudeNames() map[string]string {
	out := make(map[string]string, len(c.Servers))
	for _, s := range c.Servers {
		out[s.Name] = s.ClaudeName
	}
	return out
}

// Endpoint is the URL or command line the server is reached at, for display.
func (s Server) Endpoint() string {
	v := spec.ViewOf(s.Spec)
	if v.Remote() {
		return v.URL
	}
	if v.Command == "" {
		return "?"
	}
	return strings.Join(append([]string{v.Command}, v.Args...), " ")
}

// Catalog is the merged view.
type Catalog struct {
	Servers  []Server
	Profiles map[string][]string
	// ProfileFiles names the catalog file each profile was read from, so
	// that one can be deleted where it is.
	ProfileFiles map[string]string
	// Path is the catalog file + and d edit.
	Path       string
	ClaudePath string
	// Files is every catalog file that existed when the catalog was loaded,
	// in reading order, with the exact bytes the servers above were parsed
	// from. A trust in the catalog as a whole (see internal/trust) is keyed
	// on these bytes, not on the files as they are on disk later: what was
	// shown and consented to is what was parsed.
	Files []File
	// ProjectKey is the key this workspace has under projects in
	// ~/.claude.json; see projectKey.
	ProjectKey string
	Warnings   []string
}

// Find returns the server named name.
func (c *Catalog) Find(name string) (Server, bool) {
	for _, s := range c.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// SpecOf returns the unexpanded spec, which is what measurements are keyed on:
// the fingerprint has to survive a different environment.
func (c *Catalog) SpecOf(name string) map[string]any {
	s, _ := c.Find(name)
	return s.Spec
}

func (c *Catalog) warnf(format string, args ...any) {
	c.Warnings = append(c.Warnings, fmt.Sprintf(format, args...))
}

// WorkspaceRoot walks up from dir to the directory that owns the session: the
// nearest ancestor holding a catalog file or a .git, whichever comes first.
// Stopping at the first .git keeps a stray ~/.mcp.yaml from swallowing every
// repository under the home directory.
func WorkspaceRoot(dir string) string {
	d := dir
	for {
		for _, marker := range append(append([]string{}, Names...), ".git") {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// Path picks the catalog file to write: the first of Names that exists under
// root, else root/.mcp.yaml.
func Path(root string) string {
	for _, n := range Names {
		p := filepath.Join(root, n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(root, Names[0])
}

// Load merges every source. path is the catalog file mcpick writes to; root is
// the workspace root; cwd is where mcpick was started, which Claude Code may
// have used as the project key instead of root.
func Load(path, root, cwd string) (*Catalog, error) {
	cat := &Catalog{
		Path:         path,
		ClaudePath:   ClaudeJSONPath(),
		Profiles:     map[string][]string{},
		ProfileFiles: map[string]string{},
	}
	seen := map[string]Server{}
	// A name defined again in a later source is shadowed by the first.
	// Those are collected per pair of files and reported as one line at
	// the end, and only when the two definitions differ: the same spec
	// twice, or one the redacted form of the other — what `import` leaves
	// behind in ~/.claude.json — is nothing to warn about.
	type pair struct{ first, second string }
	shadowed := map[pair][]string{}
	var pairs []pair
	add := func(name, origin, source string, sp map[string]any) {
		if name == "" {
			return
		}
		if prev, dup := seen[name]; dup {
			if prev.Source != source && !sameServer(name, prev.Spec, sp) {
				k := pair{prev.Source, source}
				if _, ok := shadowed[k]; !ok {
					pairs = append(pairs, k)
				}
				shadowed[k] = append(shadowed[k], name)
			}
			return
		}
		s := Server{Name: name, Origin: origin, Spec: sp, Source: source}
		seen[name] = s
		cat.Servers = append(cat.Servers, s)
	}

	// Every catalog file under root is read, not just the one mcpick writes
	// to, so a repository carrying the ecosystem-standard .mcp.json works out
	// of the box. Each is read once and parsed from those bytes, which the
	// catalog keeps (Files).
	var err error
	if cat.Files, err = Snapshot(path, root); err != nil {
		return nil, err
	}
	for _, f := range cat.Files {
		names, specs, profiles, err := Parse(f.Path, f.Data)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			add(n, OriginProject, f.Path, specs[n])
		}
		for k, v := range profiles {
			if _, ok := cat.Profiles[k]; !ok {
				cat.Profiles[k] = v
				cat.ProfileFiles[k] = f.Path
			}
		}
	}

	top, err := readClaudeJSON(cat.ClaudePath)
	if err != nil {
		return nil, err
	}
	cat.ProjectKey = projectKey(top, root, cwd)
	disabled := disabledServers(top, cat.ProjectKey)

	for _, src := range []struct {
		origin string
		raw    json.RawMessage
	}{
		{OriginLocal, projectServers(top, cat.ProjectKey)},
		{OriginUser, top["mcpServers"]},
	} {
		if len(src.raw) == 0 {
			continue
		}
		m := map[string]map[string]any{}
		if err := json.Unmarshal(src.raw, &m); err != nil {
			cat.warnf("%s: %s mcpServers is malformed (%v), skipped",
				fsutil.ShortenHome(cat.ClaudePath), src.origin, err)
			continue
		}
		for _, k := range spec.SortedKeys(m) {
			add(k, src.origin, cat.ClaudePath, m[k])
		}
	}

	// Plugins come last: --strict-mcp-config drops them, so they have to be
	// in the catalog for the selection to be able to keep them at all.
	claudeNames := map[string]string{}
	for _, p := range pluginServers(cat) {
		add(p.Name, OriginPlugin, p.Source, p.Spec)
		claudeNames[p.Name] = p.ClaudeName
	}

	for _, k := range pairs {
		names := shadowed[k]
		wins := "the first wins"
		if isCatalogFile(k.first) {
			wins = "the catalog wins"
		}
		cat.warnf("%d %s in %s %s %s (%s); %s", len(names), plural(len(names), "server", "servers"),
			shortFile(k.first), plural(len(names), "shadows", "shadow"), shortFile(k.second),
			strings.Join(names, ", "), wins)
	}

	for i := range cat.Servers {
		s := &cat.Servers[i]
		s.ClaudeName = s.Name
		if s.Origin == OriginPlugin {
			s.ClaudeName = claudeNames[s.Name]
		}
		switch {
		case disabled[s.Name]:
			s.Disabled, s.DisabledAs = true, s.Name
		case s.Origin == OriginPlugin && disabled[claudeNames[s.Name]]:
			s.Disabled, s.DisabledAs = true, claudeNames[s.Name]
		}
	}
	cat.checkProfiles()
	return cat, nil
}

// sameServer says whether two definitions of a name are the same server:
// equal specs, or one the redacted form of the other (spec.Redact), as
// `import` writes into the catalog. Both are compared through JSON, so a
// number read from YAML and one from JSON compare as values.
func sameServer(name string, a, b map[string]any) bool {
	if reflect.DeepEqual(canonical(a), canonical(b)) {
		return true
	}
	ra, _ := spec.Redact(name, a)
	rb, _ := spec.Redact(name, b)
	return reflect.DeepEqual(canonical(ra), canonical(b)) || reflect.DeepEqual(canonical(a), canonical(rb))
}

// canonical is v as JSON reads it back: maps, slices, strings, float64.
func canonical(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(data, &out) != nil {
		return v
	}
	return out
}

func isCatalogFile(path string) bool {
	return slices.Contains(Names, filepath.Base(path))
}

// shortFile names a source file the way the user knows it: a catalog file
// by its name, the rest with ~ for the home directory.
func shortFile(path string) string {
	if isCatalogFile(path) {
		return filepath.Base(path)
	}
	return fsutil.ShortenHome(path)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// checkProfiles flags names a profile lists that no longer exist, which would
// otherwise vanish from the selection without a word.
func (c *Catalog) checkProfiles() {
	for _, p := range spec.SortedKeys(c.Profiles) {
		var missing []string
		for _, n := range c.Profiles[p] {
			if _, ok := c.Find(n); !ok {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			c.warnf("profile %s lists %s, not in the catalog", p, strings.Join(missing, ", "))
		}
	}
}

func catalogFiles(path, root string) []string {
	out := []string{path}
	for _, n := range Names {
		p := filepath.Join(root, n)
		if p != path {
			out = append(out, p)
		}
	}
	return out
}

// File is one catalog file as it was read: its path and its exact bytes.
type File struct {
	Path string
	Data []byte
}

// Snapshot reads the catalog files Load reads for a workspace — path and
// every Names under root — that exist now, in reading order, bytes
// included. Load parses from it, and a trust in the catalog as a whole is
// keyed on it (see internal/trust): the same read serves both, so what is
// hashed is what was parsed. A file that cannot be read for a reason other
// than not existing is an error, as it is for Load.
func Snapshot(path, root string) ([]File, error) {
	var out []File
	for _, p := range catalogFiles(path, root) {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: p, Data: data})
	}
	return out, nil
}

// ReadFile reads a catalog file, YAML or JSON by extension. Both carry the same
// shape: a servers (or mcpServers) map plus an optional profiles map. A file
// that does not exist reads as empty.
func ReadFile(path string) ([]string, map[string]map[string]any, map[string][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, map[string]map[string]any{}, nil, nil
		}
		return nil, map[string]map[string]any{}, nil, err
	}
	return Parse(path, data)
}

// Parse is ReadFile over bytes already read; path picks the format by its
// extension and names the file in errors.
func Parse(path string, data []byte) ([]string, map[string]map[string]any, map[string][]string, error) {
	if strings.HasSuffix(path, ".json") {
		return parseJSON(path, data)
	}
	return parseYAML(path, data)
}

func parseJSON(path string, data []byte) ([]string, map[string]map[string]any, map[string][]string, error) {
	specs := map[string]map[string]any{}
	var doc struct {
		Servers    map[string]map[string]any `json:"servers"`
		McpServers map[string]map[string]any `json:"mcpServers"`
		Profiles   map[string][]string       `json:"profiles"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, specs, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	m := doc.McpServers
	if m == nil {
		m = doc.Servers
	}
	// JSON objects are unordered to Go; the file's own order is what the
	// user sees in their editor, so keep it.
	names := jsonKeyOrder(data, m)
	for _, k := range names {
		specs[k] = m[k]
	}
	return names, specs, doc.Profiles, nil
}

func jsonKeyOrder(data []byte, m map[string]map[string]any) []string {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) == nil {
		for _, key := range []string{"mcpServers", "servers"} {
			if raw, ok := top[key]; ok {
				if order, err := spec.TopLevelOrder(raw); err == nil && len(order) == len(m) {
					return order
				}
			}
		}
	}
	return spec.SortedKeys(m)
}

// yamlDocOf reads a catalog file's bytes as a YAML node tree: nil when
// there is nothing in them (empty, comments only, a bare null). The file
// has to be one document whose root is a mapping. A second document was
// read as if it were not there — and then written back without it — and a
// root that is a list or a scalar has nowhere a server could go, so either
// is refused rather than guessed at. path names the file in errors.
func yamlDocOf(path string, data []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return nil, fmt.Errorf("%s holds more than one YAML document; a catalog is one document", fsutil.ShortenHome(path))
	case !errors.Is(err, io.EOF):
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return nil, nil
	}
	if root := doc.Content[0]; root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: the document is %s, not a mapping", fsutil.ShortenHome(path), kindName(root))
	}
	return &doc, nil
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	}
	return "not a mapping"
}

// block returns the mapping under key in root: nil when the key is absent
// or its value is null (an empty block, which an edit may fill in), an
// error when it is anything else — `servers: []` is a list, and a server
// written into it would not be one a reader finds.
func block(root *yaml.Node, key, path string) (*yaml.Node, error) {
	n := mapValue(root, key)
	if n == nil || isNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: %s is %s, not a mapping of names to servers", fsutil.ShortenHome(path), key, kindName(n))
	}
	return n, nil
}

// serversBlock is the servers mapping, under servers or mcpServers, and
// the key it was found under — "servers" when neither is there.
func serversBlock(root *yaml.Node, path string) (*yaml.Node, string, error) {
	for _, key := range []string{"servers", "mcpServers"} {
		if mapValue(root, key) == nil {
			continue
		}
		n, err := block(root, key, path)
		return n, key, err
	}
	return nil, "servers", nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func parseYAML(path string, data []byte) ([]string, map[string]map[string]any, map[string][]string, error) {
	specs := map[string]map[string]any{}
	doc, err := yamlDocOf(path, data)
	if err != nil || doc == nil {
		return nil, specs, nil, err
	}
	root := doc.Content[0]

	profiles := map[string][]string{}
	p, err := block(root, "profiles", path)
	if err != nil {
		return nil, specs, nil, err
	}
	if p != nil {
		if err := p.Decode(&profiles); err != nil {
			return nil, specs, nil, fmt.Errorf("%s: profiles: %w", path, err)
		}
	}

	servers, _, err := serversBlock(root, path)
	if err != nil {
		return nil, specs, nil, err
	}
	if servers == nil {
		return nil, specs, profiles, nil
	}
	var names []string
	for i := 0; i+1 < len(servers.Content); i += 2 {
		name := servers.Content[i].Value
		sp := map[string]any{}
		if err := servers.Content[i+1].Decode(&sp); err != nil {
			return nil, specs, nil, fmt.Errorf("%s: server %s: %w", path, name, err)
		}
		names = append(names, name)
		specs[name] = sp
	}
	return names, specs, profiles, nil
}

// writeYAMLDoc writes doc to path once its bytes read back as the servers
// in want (encodeChecked).
func writeYAMLDoc(path string, doc *yaml.Node, want map[string]map[string]any) error {
	data, err := encodeChecked(path, doc, want)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, fsutil.ModeOf(path, 0o644))
}

// readYAML is yamlDoc and the servers the file holds now, which an edit
// has to keep (writeYAMLDoc).
func readYAML(path string) (*yaml.Node, map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, map[string]map[string]any{}, nil
		}
		return nil, nil, err
	}
	doc, err := yamlDocOf(path, data)
	if err != nil {
		return nil, nil, err
	}
	_, specs, _, err := parseYAML(path, data)
	if err != nil {
		return nil, nil, err
	}
	return doc, specs, nil
}

// mapEntry is the key and value nodes of key in mapping m, nil when absent.
func mapEntry(m *yaml.Node, key string) []*yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i : i+2]
		}
	}
	return nil
}

func newYAMLDoc() *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
}

// setMapEntry replaces key in mapping m, or appends it.
func setMapEntry(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
}

func deleteMapEntry(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// lockCatalog is what every edit of a catalog file starts with. The lock
// is held across the read, the check and the atomic write, so two pickers
// adding a server each cannot write over one another, and a move that
// checks its destination is free sees the file it then writes. The waits
// are ~/.claude.json's.
func lockCatalog(path string) (func(), error) {
	return fsutil.Lock(path, claudeLockWait, claudeLockStale)
}

// AddServer writes or replaces one server in the catalog file at path.
func AddServer(path, name string, sp map[string]any) error {
	return addServer(path, name, sp, true)
}

// changedError is a source entry that is not what the caller loaded: it
// was edited, or removed, since. Nothing is moved until the caller has
// seen the current one.
func changedError(name string) error {
	return fmt.Errorf("%s changed since the picker loaded it; reload (esc, then reopen) and move again", name)
}

// addServer writes one server into the catalog file at path, under the
// file's lock. With replace unset a server of that name already there is
// refused, not written over: that is the move's rule. A servers block that
// is null becomes a mapping; one of any other shape is refused (block).
func addServer(path, name string, sp map[string]any, replace bool) error {
	unlock, err := lockCatalog(path)
	if err != nil {
		return err
	}
	defer unlock()
	if strings.HasSuffix(path, ".json") {
		return editJSONServers(path, func(servers map[string]json.RawMessage) error {
			if _, dup := servers[name]; dup && !replace {
				return fmt.Errorf("%s already exists in %s", name, fsutil.ShortenHome(path))
			}
			raw, err := marshalNoEscape(sp)
			if err != nil {
				return err
			}
			servers[name] = raw
			return nil
		})
	}
	doc, want, err := readYAML(path)
	if err != nil {
		return err
	}
	if doc == nil {
		doc = newYAMLDoc()
	}
	root := doc.Content[0]
	servers, key, err := serversBlock(root, path)
	if err != nil {
		return err
	}
	if servers == nil {
		servers = &yaml.Node{Kind: yaml.MappingNode}
		setMapEntry(root, key, servers)
	}
	if !replace && mapValue(servers, name) != nil {
		return fmt.Errorf("%s already exists in %s", name, fsutil.ShortenHome(path))
	}
	if old := mapValue(servers, name); old != nil {
		if used := anchoredElsewhere(doc, old); len(used) > 0 {
			return anchorError(path, name, used)
		}
	}
	var value yaml.Node
	if err := value.Encode(sp); err != nil {
		return err
	}
	setMapEntry(servers, name, &value)
	want[name] = sp
	return writeYAMLDoc(path, doc, want)
}

// DeleteServer removes one server from the catalog file at path. A name
// that is not there is not an error: there is nothing to undo.
func DeleteServer(path, name string) error {
	return deleteServer(path, name, nil)
}

// deleteServer removes name from the catalog file at path, under the
// file's lock. With expect set, the entry has to still be what expect says
// — the definition the caller loaded — or it is left alone and reported as
// changed (changedError): the move's rule, so a picker holding an old copy
// never deletes a newer one.
func deleteServer(path, name string, expect map[string]any) error {
	unlock, err := lockCatalog(path)
	if err != nil {
		return err
	}
	defer unlock()
	if strings.HasSuffix(path, ".json") {
		return editJSONServers(path, func(servers map[string]json.RawMessage) error {
			if expect != nil {
				var have map[string]any
				raw, ok := servers[name]
				if !ok || json.Unmarshal(raw, &have) != nil || !reflect.DeepEqual(have, expect) {
					return changedError(name)
				}
			}
			delete(servers, name)
			return nil
		})
	}
	doc, want, err := readYAML(path)
	if err != nil {
		return err
	}
	var servers *yaml.Node
	if doc != nil {
		if servers, _, err = serversBlock(doc.Content[0], path); err != nil {
			return err
		}
	}
	if expect != nil {
		var have map[string]any
		n := mapValue(servers, name)
		if n == nil || n.Decode(&have) != nil || !reflect.DeepEqual(have, expect) {
			return changedError(name)
		}
	}
	entry := mapEntry(servers, name)
	if entry == nil {
		return nil
	}
	if used := anchoredElsewhere(doc, entry...); len(used) > 0 {
		return anchorError(path, name, used)
	}
	deleteMapEntry(servers, name)
	delete(want, name)
	return writeYAMLDoc(path, doc, want)
}

// editJSONServers rewrites the server map of a JSON catalog, keeping every
// other key and the order of both the file's keys and the servers. The
// caller holds the file's lock. A server block that is null becomes an
// object; one of any other shape is refused.
func editJSONServers(path string, fn func(map[string]json.RawMessage) error) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	top, order, err := spec.DecodeOrdered(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	key := "mcpServers"
	if len(top["servers"]) > 0 && len(top["mcpServers"]) == 0 {
		key = "servers"
	}
	if raw := top[key]; string(bytes.TrimSpace(raw)) == "null" {
		top[key] = nil
	}
	servers, serverOrder, err := spec.DecodeOrdered(top[key])
	if err != nil {
		return fmt.Errorf("%s: %s is not a JSON object of names to servers", fsutil.ShortenHome(path), key)
	}
	if err := fn(servers); err != nil {
		return err
	}
	if top[key], err = spec.MarshalOrdered(servers, serverOrder); err != nil {
		return err
	}
	out, err := spec.MarshalOrdered(top, order)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, out, fsutil.ModeOf(path, 0o644))
}

// marshalNoEscape is json.Marshal without the HTML escaping that turns & into
// & — valid JSON either way, but a needless diff in a file people read.
func marshalNoEscape(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// ProfileNames lists profiles in a stable order.
func (c *Catalog) ProfileNames() []string {
	out := spec.SortedKeys(c.Profiles)
	sort.Strings(out)
	return out
}

// Resolve expands the selected servers, in catalog order, into what a session
// will actually run with.
func Resolve(cat *Catalog, sel map[string]bool, uid string) (spec.Selection, error) {
	out := spec.Selection{Specs: map[string]map[string]any{}, Raw: map[string]map[string]any{}}
	for _, s := range cat.Servers {
		if !sel[s.Name] {
			continue
		}
		e, err := spec.Expand(s.Spec, uid)
		if err != nil {
			return out, fmt.Errorf("%s: %w", s.Name, err)
		}
		m, ok := e.(map[string]any)
		if !ok {
			return out, fmt.Errorf("%s: spec is not a mapping", s.Name)
		}
		out.Names = append(out.Names, s.Name)
		out.Specs[s.Name] = m
		out.Raw[s.Name] = s.Spec
	}
	return out, nil
}

// DeleteProfile removes a profile from a catalog file. Profiles are personal
// now (see internal/profile); this is for the ones a catalog still carries —
// often not chosen on purpose, like a pasted prompt an older picker saved as
// a profile name. Deleting one that is not there is an error, so a typo does
// not look like success.
func DeleteProfile(path, name string) error {
	unlock, err := lockCatalog(path)
	if err != nil {
		return err
	}
	defer unlock()
	if strings.HasSuffix(path, ".json") {
		return deleteJSONProfile(path, name)
	}
	doc, want, err := readYAML(path)
	if err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("no profile %q: %s does not exist", name, fsutil.ShortenHome(path))
	}
	profiles, err := block(doc.Content[0], "profiles", path)
	if err != nil {
		return err
	}
	entry := mapEntry(profiles, name)
	if entry == nil {
		return fmt.Errorf("no profile %q in %s", name, fsutil.ShortenHome(path))
	}
	removed := entry
	if len(profiles.Content) == 2 {
		removed = mapEntry(doc.Content[0], "profiles") // the whole block goes
	}
	if used := anchoredElsewhere(doc, removed...); len(used) > 0 {
		return anchorError(path, "profile "+name, used)
	}
	deleteMapEntry(profiles, name)
	if len(profiles.Content) == 0 {
		deleteMapEntry(doc.Content[0], "profiles") // no empty profiles: {} left behind
	}
	return writeYAMLDoc(path, doc, want)
}

func deleteJSONProfile(path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	top, order, err := spec.DecodeOrdered(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	profiles := map[string]json.RawMessage{}
	if len(top["profiles"]) > 0 {
		if err := json.Unmarshal(top["profiles"], &profiles); err != nil {
			return fmt.Errorf("%s: profiles: %w", path, err)
		}
	}
	if _, ok := profiles[name]; !ok {
		return fmt.Errorf("no profile %q in %s", name, fsutil.ShortenHome(path))
	}
	delete(profiles, name)
	if len(profiles) == 0 {
		delete(top, "profiles")
	} else {
		raw, err := marshalNoEscape(profiles)
		if err != nil {
			return err
		}
		top["profiles"] = raw
	}
	out, err := spec.MarshalOrdered(top, order)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, out, fsutil.ModeOf(path, 0o644))
}
