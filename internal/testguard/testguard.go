// Package testguard catches tests that write into the real files of whoever
// runs them. It is imported only from TestMain functions.
package testguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ClaudeJSON records what mcpick could change in the real ~/.claude.json —
// its MCP servers, and the mcpick backups next to it — and returns a check
// that reports a change to either. A picker test over a hand-built catalog
// once pointed at the real file, and a pasted text that spelled v then g
// moved one of the user's servers in it.
//
// The check is on the content that matters, not on the file's size or
// modification time: Claude Code rewrites ~/.claude.json on its own while
// it runs (history, tips, session counts), and a guard on the mtime failed
// every run started from inside it. A change to a server, however small,
// alters the fingerprint; and every write mcpick makes to the file leaves a
// backup behind, so even one that puts the same servers back is seen.
func ClaudeJSON() func() error {
	real := Path()
	before := fingerprint(real)
	backups := backupsOf(real)
	return func() error {
		if fingerprint(real) != before {
			return fmt.Errorf("a test changed the MCP servers of the real %s", real)
		}
		if !slices.Equal(backupsOf(real), backups) {
			return fmt.Errorf("a test wrote a backup of the real %s", real)
		}
		return nil
	}
}

// Path is the real ~/.claude.json, honouring CLAUDE_CONFIG_DIR as mcpick
// does; the tests set that variable to a temporary directory, so a guard
// created after it is set watches nothing real.
func Path() string {
	home, _ := os.UserHomeDir()
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// fingerprint is a hash of the MCP-relevant content of a ~/.claude.json:
// the top-level mcpServers and disabledMcpServers, and for every project
// its mcpServers and disabledMcpServers. A missing file has its own value,
// and a file that is not JSON is hashed whole.
func fingerprint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent"
		}
		return "unreadable: " + err.Error()
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return "raw:" + digest(data)
	}
	var b strings.Builder
	for _, key := range []string{"mcpServers", "disabledMcpServers"} {
		b.WriteString(key + "=" + canonical(top[key]) + "\n")
	}
	var projects map[string]map[string]json.RawMessage
	_ = json.Unmarshal(top["projects"], &projects)
	for _, name := range slices.Sorted(maps.Keys(projects)) {
		for _, key := range []string{"mcpServers", "disabledMcpServers"} {
			b.WriteString(name + "/" + key + "=" + canonical(projects[name][key]) + "\n")
		}
	}
	return "mcp:" + digest([]byte(b.String()))
}

// canonical re-encodes a JSON value with its object keys sorted, so a
// rewrite that only reorders keys reads as the same value.
func canonical(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, _ := json.Marshal(v) // json.Marshal sorts map keys
	return string(out)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// backupsOf lists the mcpick backups of path, sorted by name.
func backupsOf(path string) []string {
	names, _ := filepath.Glob(path + ".mcpick-bak-*")
	slices.Sort(names)
	return names
}
