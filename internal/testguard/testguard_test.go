package testguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sample = `{
  "history": ["first"],
  "mcpServers": {"gl": {"type": "http", "url": "https://x/gl"}},
  "disabledMcpServers": [],
  "projects": {
    "/src/app": {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}, "disabledMcpServers": ["pr"], "history": []}
  },
  "tipsHistory": {"a": 1}
}`

func guarded(t *testing.T) (string, func() error) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, ClaudeJSON()
}

// Claude Code rewrites ~/.claude.json while it runs — a new history entry,
// a tip counter — and a guard that watched the file's mtime and size failed
// every test run started from inside a Claude Code session. What is
// watched is the MCP content: a rewrite that leaves the servers alone,
// reorders keys or only touches the mtime is not a finding.
func TestUnrelatedRewriteIsNotReported(t *testing.T) {
	path, check := guarded(t)
	rewritten := `{
  "tipsHistory": {"a": 2},
  "projects": {
    "/src/app": {"history": ["x"], "disabledMcpServers": ["pr"], "mcpServers": {"pr": {"url": "https://x/pr", "type": "http"}}}
  },
  "disabledMcpServers": [],
  "mcpServers": {"gl": {"url": "https://x/gl", "type": "http"}},
  "history": ["first", "second", "third"]
}`
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := check(); err != nil {
		t.Errorf("a rewrite of unrelated keys was reported: %v", err)
	}
}

// A change to what mcpick manages is reported wherever it is: the user's
// servers, a project's servers, or a project's disabled list.
func TestServerChangesAreReported(t *testing.T) {
	for name, edited := range map[string]string{
		"user server edited":     `{"mcpServers": {"gl": {"type": "http", "url": "https://evil/gl"}}, "projects": {"/src/app": {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}, "disabledMcpServers": ["pr"]}}}`,
		"user server removed":    `{"mcpServers": {}, "projects": {"/src/app": {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}, "disabledMcpServers": ["pr"]}}}`,
		"project server added":   `{"mcpServers": {"gl": {"type": "http", "url": "https://x/gl"}}, "projects": {"/src/app": {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}, "new": {"type": "http", "url": "https://x/new"}}, "disabledMcpServers": ["pr"]}}}`,
		"project enabled":        `{"mcpServers": {"gl": {"type": "http", "url": "https://x/gl"}}, "projects": {"/src/app": {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}, "disabledMcpServers": []}}}`,
		"another project's list": `{"mcpServers": {"gl": {"type": "http", "url": "https://x/gl"}}, "projects": {"/src/app": {"mcpServers": {"pr": {"type": "http", "url": "https://x/pr"}}, "disabledMcpServers": ["pr"]}, "/src/other": {"mcpServers": {"o": {"type": "http", "url": "https://x/o"}}}}}`,
		"not JSON any more":      `{"mcpServers": `,
	} {
		t.Run(name, func(t *testing.T) {
			path, check := guarded(t)
			if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := check(); err == nil {
				t.Error("the change went unreported")
			}
		})
	}
}

// Every write mcpick makes leaves a backup next to the file; a write that
// puts the same servers back is caught by that, and so is one that renames
// or removes a backup.
func TestBackupsAreReported(t *testing.T) {
	path, check := guarded(t)
	if err := os.WriteFile(path+".mcpick-bak-20260926T000000.000000000Z", []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Error("a new backup went unreported")
	}

	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path = filepath.Join(dir, ".claude.json")
	old := path + ".mcpick-bak-20260101T000000.000000000Z"
	for _, p := range []string{path, old} {
		if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	check = ClaudeJSON()
	if err := os.Rename(old, path+".mcpick-bak-20260102T000000.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Error("a renamed backup went unreported: the count alone is not enough")
	}
}

// Creating the file where there was none, or removing it, is a change too.
func TestPresenceIsReported(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".claude.json")
	check := ClaudeJSON()
	if err := check(); err != nil {
		t.Fatalf("no file, no change: %v", err)
	}
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Error("a file that appeared went unreported")
	}
	check = ClaudeJSON()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Error("a file that vanished went unreported")
	}
}
