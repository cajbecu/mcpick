package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// DeleteFromClaudeJSON removes one server from ~/.claude.json, from the
// top-level map (origin global) or from the project entry (origin project).
// Everything else is round-tripped as raw JSON in its original order, under a
// lock, after a timestamped backup.
func DeleteFromClaudeJSON(path, projectKey, name, origin string) error {
	if path == "" {
		return fmt.Errorf("no home directory, cannot locate .claude.json")
	}
	unlock, err := fsutil.Lock(path, 2*time.Second, 30*time.Second)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	backup := fmt.Sprintf("%s.mcpick-bak-%s", path, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return fmt.Errorf("writing backup: %w", err)
	}
	pruneBackups(path, 5)

	top, order, err := spec.DecodeOrdered(data)
	if err != nil {
		return err
	}

	dropFrom := func(raw json.RawMessage, where string) (json.RawMessage, error) {
		servers, sorder, err := spec.DecodeOrdered(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := servers[name]; !ok {
			return nil, fmt.Errorf("%s not found in %s mcpServers", name, where)
		}
		delete(servers, name)
		return spec.MarshalOrdered(servers, sorder)
	}

	switch origin {
	case OriginGlobal:
		if top["mcpServers"], err = dropFrom(top["mcpServers"], "global"); err != nil {
			return err
		}
	case OriginProject:
		projects, porder, err := spec.DecodeOrdered(top["projects"])
		if err != nil {
			return err
		}
		entry, eorder, err := spec.DecodeOrdered(projects[projectKey])
		if err != nil {
			return err
		}
		if entry["mcpServers"], err = dropFrom(entry["mcpServers"], "project"); err != nil {
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
	unlock, err := fsutil.Lock(path, 2*time.Second, 30*time.Second)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	backup := fmt.Sprintf("%s.mcpick-bak-%s", path, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return fmt.Errorf("writing backup: %w", err)
	}
	pruneBackups(path, 5)

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
