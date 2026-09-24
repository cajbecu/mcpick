// Package catalog merges the MCP servers mcpick can see — its own catalog
// files, Claude Code's project and global config, and Claude Code plugins —
// into one ordered list, and edits the files those servers came from.
package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Origins say where a server was defined, which decides both precedence and
// what deleting it means.
const (
	OriginWorkspace = "workspace" // .mcp.yaml / .mcp.json beside the workspace
	OriginProject   = "project"   // ~/.claude.json projects[<root>]
	OriginGlobal    = "global"    // ~/.claude.json mcpServers
	OriginPlugin    = "plugin"    // an enabled Claude Code plugin; read-only
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
	// Path is the catalog file + and d edit, and where profiles are saved.
	Path       string
	ClaudePath string
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
		Path:       path,
		ClaudePath: ClaudeJSONPath(),
		Profiles:   map[string][]string{},
	}
	seen := map[string]string{}
	add := func(name, origin, source string, sp map[string]any) {
		if name == "" {
			return
		}
		if prev, dup := seen[name]; dup {
			if prev != source {
				cat.warnf("%s is defined in %s and %s; using the first", name,
					fsutil.ShortenHome(prev), fsutil.ShortenHome(source))
			}
			return
		}
		seen[name] = source
		cat.Servers = append(cat.Servers, Server{Name: name, Origin: origin, Spec: sp, Source: source})
	}

	// Every catalog file under root is read, not just the one mcpick writes
	// to, so a repository carrying the ecosystem-standard .mcp.json works out
	// of the box.
	for _, p := range catalogFiles(path, root) {
		names, specs, profiles, err := ReadFile(p)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			add(n, OriginWorkspace, p, specs[n])
		}
		for k, v := range profiles {
			if _, ok := cat.Profiles[k]; !ok {
				cat.Profiles[k] = v
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
		{OriginProject, projectServers(top, cat.ProjectKey)},
		{OriginGlobal, top["mcpServers"]},
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

// ReadFile reads a catalog file, YAML or JSON by extension. Both carry the same
// shape: a servers (or mcpServers) map plus an optional profiles map.
func ReadFile(path string) ([]string, map[string]map[string]any, map[string][]string, error) {
	if strings.HasSuffix(path, ".json") {
		return readJSON(path)
	}
	return readYAML(path)
}

func readJSON(path string) ([]string, map[string]map[string]any, map[string][]string, error) {
	specs := map[string]map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, specs, nil, nil
		}
		return nil, specs, nil, err
	}
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

func yamlDoc(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	return &doc, nil
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

func serversNode(root *yaml.Node) *yaml.Node {
	if n := mapValue(root, "servers"); n != nil {
		return n
	}
	return mapValue(root, "mcpServers")
}

func readYAML(path string) ([]string, map[string]map[string]any, map[string][]string, error) {
	specs := map[string]map[string]any{}
	doc, err := yamlDoc(path)
	if err != nil || doc == nil {
		return nil, specs, nil, err
	}
	root := doc.Content[0]

	profiles := map[string][]string{}
	if p := mapValue(root, "profiles"); p != nil {
		if err := p.Decode(&profiles); err != nil {
			return nil, specs, nil, fmt.Errorf("%s: profiles: %w", path, err)
		}
	}

	servers := serversNode(root)
	if servers == nil || servers.Kind != yaml.MappingNode {
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

func writeYAMLDoc(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, buf.Bytes(), fsutil.ModeOf(path, 0o644))
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

// AddServer writes or replaces one server in the catalog file at path.
func AddServer(path, name string, sp map[string]any) error {
	if strings.HasSuffix(path, ".json") {
		return editJSONServers(path, func(servers map[string]json.RawMessage) error {
			raw, err := marshalNoEscape(sp)
			if err != nil {
				return err
			}
			servers[name] = raw
			return nil
		})
	}
	doc, err := yamlDoc(path)
	if err != nil {
		return err
	}
	if doc == nil {
		doc = newYAMLDoc()
	}
	root := doc.Content[0]
	servers := serversNode(root)
	if servers == nil {
		servers = &yaml.Node{Kind: yaml.MappingNode}
		setMapEntry(root, "servers", servers)
	}
	var value yaml.Node
	if err := value.Encode(sp); err != nil {
		return err
	}
	setMapEntry(servers, name, &value)
	return writeYAMLDoc(path, doc)
}

// DeleteServer removes one server from the catalog file at path.
func DeleteServer(path, name string) error {
	if strings.HasSuffix(path, ".json") {
		return editJSONServers(path, func(servers map[string]json.RawMessage) error {
			delete(servers, name)
			return nil
		})
	}
	doc, err := yamlDoc(path)
	if err != nil || doc == nil {
		return err
	}
	servers := serversNode(doc.Content[0])
	if servers == nil || !deleteMapEntry(servers, name) {
		return nil
	}
	return writeYAMLDoc(path, doc)
}

// editJSONServers rewrites the server map of a JSON catalog, keeping every
// other key and the order of both the file's keys and the servers.
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
	servers, serverOrder, err := spec.DecodeOrdered(top[key])
	if err != nil {
		return err
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

// SaveProfile stores a named selection in the catalog file so it travels with
// the repository.
func SaveProfile(path, name string, names []string) error {
	if strings.HasSuffix(path, ".json") {
		return editJSONProfiles(path, func(p map[string][]string) { p[name] = names })
	}
	doc, err := yamlDoc(path)
	if err != nil {
		return err
	}
	if doc == nil {
		doc = newYAMLDoc()
	}
	root := doc.Content[0]
	profiles := mapValue(root, "profiles")
	if profiles == nil {
		profiles = &yaml.Node{Kind: yaml.MappingNode}
		setMapEntry(root, "profiles", profiles)
	}
	var value yaml.Node
	if err := value.Encode(names); err != nil {
		return err
	}
	value.Style = yaml.FlowStyle
	setMapEntry(profiles, name, &value)
	return writeYAMLDoc(path, doc)
}

// DeleteProfile removes a named selection. Deleting one that is not there is
// an error, so a typo does not look like success.
func DeleteProfile(path, name string) error {
	if strings.HasSuffix(path, ".json") {
		found := false
		err := editJSONProfiles(path, func(p map[string][]string) {
			_, found = p[name]
			delete(p, name)
		})
		if err == nil && !found {
			return fmt.Errorf("no profile %q in %s", name, fsutil.ShortenHome(path))
		}
		return err
	}
	doc, err := yamlDoc(path)
	if err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("no profile %q: %s does not exist", name, fsutil.ShortenHome(path))
	}
	profiles := mapValue(doc.Content[0], "profiles")
	if profiles == nil || !deleteMapEntry(profiles, name) {
		return fmt.Errorf("no profile %q in %s", name, fsutil.ShortenHome(path))
	}
	return writeYAMLDoc(path, doc)
}

func editJSONProfiles(path string, fn func(map[string][]string)) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	top, order, err := spec.DecodeOrdered(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	profiles := map[string][]string{}
	if len(top["profiles"]) > 0 {
		if err := json.Unmarshal(top["profiles"], &profiles); err != nil {
			return fmt.Errorf("%s: profiles: %w", path, err)
		}
	}
	fn(profiles)
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
	out := spec.Selection{Specs: map[string]map[string]any{}}
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
	}
	return out, nil
}
