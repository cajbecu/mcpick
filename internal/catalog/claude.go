package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// ClaudeDir is Claude Code's configuration directory: $CLAUDE_CONFIG_DIR, else
// ~/.claude.
func ClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return fsutil.Home(".claude")
}

// ClaudeJSONPath is where Claude Code keeps its user state, project records and
// user-scoped MCP servers. With CLAUDE_CONFIG_DIR set it lives inside that
// directory; otherwise it sits beside ~/.claude, not in it.
func ClaudeJSONPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	return fsutil.Home(".claude.json")
}

func readClaudeJSON(path string) (map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	if path == "" {
		return top, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return top, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return top, nil
}

// projectKey finds the key under which Claude Code filed this directory. Claude
// keys projects by the path it was launched from, which is not always the path
// mcpick computed: symlinked checkouts and subdirectory launches both miss. Try
// the plausible candidates and fall back to root so the value is never empty.
func projectKey(top map[string]json.RawMessage, root, cwd string) string {
	projects := map[string]json.RawMessage{}
	if len(top["projects"]) > 0 {
		_ = json.Unmarshal(top["projects"], &projects)
	}
	candidates := []string{root, cwd}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		candidates = append(candidates, r)
	}
	if c, err := filepath.EvalSymlinks(cwd); err == nil {
		candidates = append(candidates, c)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, ok := projects[c]; ok {
			return c
		}
	}
	return root
}

func projectEntry(top map[string]json.RawMessage, key string) map[string]json.RawMessage {
	projects := map[string]json.RawMessage{}
	if len(top["projects"]) == 0 || json.Unmarshal(top["projects"], &projects) != nil {
		return nil
	}
	entry := map[string]json.RawMessage{}
	if len(projects[key]) == 0 || json.Unmarshal(projects[key], &entry) != nil {
		return nil
	}
	return entry
}

// claudeServers is the server map of one origin in ~/.claude.json, as raw
// JSON: the top-level one for user, the project entry's for local.
func claudeServers(top map[string]json.RawMessage, projectKey, origin string) json.RawMessage {
	if origin == OriginLocal {
		return projectServers(top, projectKey)
	}
	return top["mcpServers"]
}

func projectServers(top map[string]json.RawMessage, key string) json.RawMessage {
	return projectEntry(top, key)["mcpServers"]
}

// disabledServers collects the servers Claude Code refuses to start. The list
// outranks --mcp-config (anthropics/claude-code#14490), so a server listed here
// stays dark no matter what mcpick renders.
func disabledServers(top map[string]json.RawMessage, key string) map[string]bool {
	out := map[string]bool{}
	collect := func(raw json.RawMessage) {
		var names []string
		if len(raw) == 0 || json.Unmarshal(raw, &names) != nil {
			return
		}
		for _, n := range names {
			out[n] = true
		}
	}
	collect(top["disabledMcpServers"])
	collect(projectEntry(top, key)["disabledMcpServers"])
	return out
}

// claudeLockWait is how long an edit of ~/.claude.json waits for the lock
// another mcpick holds; claudeLockStale is when a leftover lock is ignored.
var (
	claudeLockWait  = 2 * time.Second
	claudeLockStale = 30 * time.Second
)

// lockClaudeJSON is what every edit of ~/.claude.json starts with: the lock,
// the file as it is, and a timestamped backup of it (five kept). The caller
// holds the lock until unlock. A file that does not exist yet reads as empty
// with no backup, so a first server can be written into a fresh config.
func lockClaudeJSON(path string) (data []byte, backup string, unlock func(), err error) {
	if path == "" {
		return nil, "", nil, fmt.Errorf("no home directory, cannot locate .claude.json")
	}
	unlock, err = fsutil.Lock(path, claudeLockWait, claudeLockStale)
	if err != nil {
		return nil, "", nil, err
	}
	data, err = os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			unlock()
			return nil, "", nil, err
		}
		return nil, "", unlock, nil
	}
	// Nanoseconds, and never over an existing file: a move writes the file
	// twice within a second, and a backup named by the second alone was
	// overwritten by the second write's, losing the state before the move.
	backup = fmt.Sprintf("%s.mcpick-bak-%s", path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := writeNew(backup, data); err != nil {
		unlock()
		return nil, "", nil, fmt.Errorf("writing backup: %w", err)
	}
	pruneBackups(path, 5)
	return data, backup, unlock, nil
}

// DeleteFromClaudeJSON removes one server from ~/.claude.json, from the
// top-level map (origin user) or from the project entry (origin local).
// Everything else is round-tripped as raw JSON in its original order, under a
// lock, after a timestamped backup.
func DeleteFromClaudeJSON(path, projectKey, name, origin string) error {
	return deleteFromClaudeJSON(path, projectKey, name, origin, nil)
}

// deleteFromClaudeJSON is DeleteFromClaudeJSON with the move's rule: with
// expect set, the entry has to still be what expect says — the definition
// the caller loaded — or it is left alone and reported as changed.
func deleteFromClaudeJSON(path, projectKey, name, origin string, expect map[string]any) error {
	data, _, unlock, err := lockClaudeJSON(path)
	if err != nil {
		return err
	}
	defer unlock()
	if data == nil {
		return fmt.Errorf("%s does not exist", fsutil.ShortenHome(path))
	}

	top, order, err := spec.DecodeOrdered(data)
	if err != nil {
		return err
	}

	dropFrom := func(raw json.RawMessage, where string) (json.RawMessage, error) {
		servers, sorder, err := spec.DecodeOrdered(raw)
		if err != nil {
			return nil, err
		}
		raw, ok := servers[name]
		if !ok {
			return nil, fmt.Errorf("%s not found in %s mcpServers", name, where)
		}
		if expect != nil {
			var have map[string]any
			if json.Unmarshal(raw, &have) != nil || !reflect.DeepEqual(have, expect) {
				return nil, changedError(name)
			}
		}
		delete(servers, name)
		return spec.MarshalOrdered(servers, sorder)
	}

	switch origin {
	case OriginUser:
		if top["mcpServers"], err = dropFrom(top["mcpServers"], "user"); err != nil {
			return err
		}
	case OriginLocal:
		projects, porder, err := spec.DecodeOrdered(top["projects"])
		if err != nil {
			return err
		}
		entry, eorder, err := spec.DecodeOrdered(projects[projectKey])
		if err != nil {
			return err
		}
		if entry["mcpServers"], err = dropFrom(entry["mcpServers"], "local"); err != nil {
			return err
		}
		if projects[projectKey], err = spec.MarshalOrdered(entry, eorder); err != nil {
			return err
		}
		if top["projects"], err = spec.MarshalOrdered(projects, porder); err != nil {
			return err
		}
	default:
		return fmt.Errorf("origin %q does not live in .claude.json", origin)
	}

	out, err := spec.MarshalOrdered(top, order)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, out, fsutil.ModeOf(path, 0o600))
}

// AddToClaudeJSON writes one server into ~/.claude.json, into the top-level
// map (origin user) or into the project entry under projectKey (origin
// local), which is created when the file has none. A server of that name
// already there is refused, not replaced. Everything else is round-tripped
// as raw JSON in its original order, under the same lock and after the same
// backup as DeleteFromClaudeJSON; the spec is written as given, placeholders
// included, since Claude Code reads it as its own.
func AddToClaudeJSON(path, projectKey, name, origin string, sp map[string]any) error {
	data, _, unlock, err := lockClaudeJSON(path)
	if err != nil {
		return err
	}
	defer unlock()

	top, order, err := spec.DecodeOrdered(data)
	if err != nil {
		return err
	}
	raw, err := marshalNoEscape(sp)
	if err != nil {
		return err
	}
	addTo := func(servers json.RawMessage, where string) (json.RawMessage, error) {
		m, morder, err := spec.DecodeOrdered(servers)
		if err != nil {
			return nil, err
		}
		if _, ok := m[name]; ok {
			return nil, fmt.Errorf("%s already exists in %s mcpServers of %s", name, where, fsutil.ShortenHome(path))
		}
		m[name] = raw
		return spec.MarshalOrdered(m, append(morder, name))
	}

	switch origin {
	case OriginUser:
		if top["mcpServers"], err = addTo(top["mcpServers"], "user"); err != nil {
			return err
		}
	case OriginLocal:
		if projectKey == "" {
			return fmt.Errorf("no project key for this workspace")
		}
		projects, porder, err := spec.DecodeOrdered(top["projects"])
		if err != nil {
			return err
		}
		entry, eorder, err := spec.DecodeOrdered(projects[projectKey])
		if err != nil {
			return err
		}
		if entry["mcpServers"], err = addTo(entry["mcpServers"], "local"); err != nil {
			return err
		}
		if _, ok := projects[projectKey]; !ok {
			porder = append(porder, projectKey)
		}
		if projects[projectKey], err = spec.MarshalOrdered(entry, eorder); err != nil {
			return err
		}
		if top["projects"], err = spec.MarshalOrdered(projects, porder); err != nil {
			return err
		}
	default:
		return fmt.Errorf("origin %q does not live in .claude.json", origin)
	}

	out, err := spec.MarshalOrdered(top, order)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, out, fsutil.ModeOf(path, 0o600))
}

// EnableInClaude removes entries from disabledMcpServers in ~/.claude.json,
// both the top-level list and this project's, so Claude Code starts those
// servers again — for good, not just for one session. It takes the same lock
// and leaves the same backup as DeleteFromClaudeJSON.
//
// A Claude Code session running elsewhere rewrites the file when it exits,
// from what it had in memory, and can put an entry back.
func EnableInClaude(path, projectKey string, entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	data, backup, unlock, err := lockClaudeJSON(path)
	if err != nil {
		return err
	}
	defer unlock()
	if data == nil {
		return fmt.Errorf("%s does not exist", fsutil.ShortenHome(path))
	}

	drop := map[string]bool{}
	for _, e := range entries {
		drop[e] = true
	}
	filter := func(raw json.RawMessage) (json.RawMessage, bool, error) {
		var names []string
		if len(raw) == 0 || json.Unmarshal(raw, &names) != nil {
			return raw, false, nil
		}
		kept := []string{}
		for _, n := range names {
			if !drop[n] {
				kept = append(kept, n)
			}
		}
		if len(kept) == len(names) {
			return raw, false, nil
		}
		out, err := marshalNoEscape(kept)
		return out, true, err
	}

	top, order, err := spec.DecodeOrdered(data)
	if err != nil {
		return err
	}
	changed := false
	if v, ok, err := filter(top["disabledMcpServers"]); err != nil {
		return err
	} else if ok {
		top["disabledMcpServers"], changed = v, true
	}
	if len(top["projects"]) > 0 {
		projects, porder, err := spec.DecodeOrdered(top["projects"])
		if err != nil {
			return err
		}
		if len(projects[projectKey]) > 0 {
			entry, eorder, err := spec.DecodeOrdered(projects[projectKey])
			if err != nil {
				return err
			}
			if v, ok, err := filter(entry["disabledMcpServers"]); err != nil {
				return err
			} else if ok {
				entry["disabledMcpServers"] = v
				if projects[projectKey], err = spec.MarshalOrdered(entry, eorder); err != nil {
					return err
				}
				if top["projects"], err = spec.MarshalOrdered(projects, porder); err != nil {
					return err
				}
				changed = true
			}
		}
	}
	if !changed {
		os.Remove(backup)
		return nil
	}
	out, err := spec.MarshalOrdered(top, order)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, out, fsutil.ModeOf(path, 0o600))
}

// writeNew writes a file that must not exist yet.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// pruneBackups keeps the newest keep backups of path. Each is a full copy of a
// file that can run to tens of megabytes.
func pruneBackups(path string, keep int) {
	matches, err := filepath.Glob(path + ".mcpick-bak-*")
	if err != nil || len(matches) <= keep {
		return
	}
	sort.Strings(matches) // the UTC timestamp suffix sorts chronologically
	for _, m := range matches[:len(matches)-keep] {
		os.Remove(m)
	}
}

// --- plugins ----------------------------------------------------------------

type pluginServer struct {
	Name   string
	Spec   map[string]any
	Source string
	// ClaudeName is what Claude Code calls the server, and so what it writes
	// into disabledMcpServers: plugin:<plugin>:<server>.
	ClaudeName string
}

// pluginServers reads the MCP servers of every enabled Claude Code plugin.
// --strict-mcp-config drops plugin servers along with everything else, so a
// user with a plugin-provided server would lose it the moment they used
// mcpick unless the catalog carries it.
//
// Plugins are listed in plugins/installed_plugins.json and enabled in
// settings.json's enabledPlugins. A plugin declares servers in .mcp.json at its
// root or inline in .claude-plugin/plugin.json, and may refer to its own
// directory as ${CLAUDE_PLUGIN_ROOT}.
func pluginServers(cat *Catalog) []pluginServer {
	dir := ClaudeDir()
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		cat.warnf("%s: %v; plugin servers skipped", fsutil.ShortenHome(filepath.Join(dir, "plugins", "installed_plugins.json")), err)
		return nil
	}
	enabled := enabledPlugins(dir)

	var out []pluginServer
	for _, id := range spec.SortedKeys(installed.Plugins) {
		if !enabled[id] {
			continue
		}
		for _, inst := range installed.Plugins[id] {
			if inst.InstallPath == "" {
				continue
			}
			plugin, _, _ := strings.Cut(id, "@")
			for _, s := range readPluginServers(inst.InstallPath) {
				s.ClaudeName = "plugin:" + plugin + ":" + s.Name
				out = append(out, s)
			}
		}
	}
	return out
}

func enabledPlugins(dir string) map[string]bool {
	out := map[string]bool{}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || json.Unmarshal(data, &settings) != nil {
			continue
		}
		for k, v := range settings.EnabledPlugins {
			out[k] = v
		}
	}
	return out
}

func readPluginServers(root string) []pluginServer {
	var servers map[string]map[string]any
	source := filepath.Join(root, ".mcp.json")
	if data, err := os.ReadFile(source); err == nil {
		var doc struct {
			McpServers map[string]map[string]any `json:"mcpServers"`
		}
		if json.Unmarshal(data, &doc) == nil {
			servers = doc.McpServers
		}
	}
	if servers == nil {
		source = filepath.Join(root, ".claude-plugin", "plugin.json")
		if data, err := os.ReadFile(source); err == nil {
			var doc struct {
				McpServers map[string]map[string]any `json:"mcpServers"`
			}
			if json.Unmarshal(data, &doc) == nil {
				servers = doc.McpServers
			}
		}
	}
	var out []pluginServer
	for _, n := range spec.SortedKeys(servers) {
		out = append(out, pluginServer{
			Name:   n,
			Spec:   replaceInStrings(servers[n], "${CLAUDE_PLUGIN_ROOT}", root),
			Source: source,
		})
	}
	return out
}

func replaceInStrings(v map[string]any, old, repl string) map[string]any {
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			return strings.ReplaceAll(t, old, repl)
		case map[string]any:
			m := make(map[string]any, len(t))
			for k, val := range t {
				m[k] = walk(val)
			}
			return m
		case []any:
			s := make([]any, len(t))
			for i, val := range t {
				s[i] = walk(val)
			}
			return s
		}
		return x
	}
	return walk(v).(map[string]any)
}
