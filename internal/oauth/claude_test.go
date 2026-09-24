package oauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/spec"
)

func remote(name, url string) spec.Selection {
	return spec.Selection{Names: []string{name}, Specs: map[string]map[string]any{name: {"type": "http", "url": url}}}
}

func authOf(sel spec.Selection, name string) any {
	h, _ := sel.Specs[name]["headers"].(map[string]any)
	return h["Authorization"]
}

func TestClaudeTokenLent(t *testing.T) {
	now := time.Now()
	tokens := []claudeToken{{ServerName: "freshdesk", ServerURL: "https://x/fd", AccessToken: "tok", ExpiresAt: now.Add(time.Hour).UnixMilli()}}
	sel := remote("freshdesk", "https://x/fd")
	notes := attachClaude(sel, map[string]string{"freshdesk": "freshdesk"}, tokens, now)
	if authOf(sel, "freshdesk") != "Bearer tok" || notes["freshdesk"] != AuthClaude {
		t.Errorf("auth = %v, note = %q", authOf(sel, "freshdesk"), notes["freshdesk"])
	}
}

// A token goes only to the host it was issued for: a catalog entry pointing
// the same name at another URL must not receive it.
func TestClaudeTokenNeedsMatchingURL(t *testing.T) {
	tokens := []claudeToken{{ServerName: "freshdesk", ServerURL: "https://x/fd", AccessToken: "tok"}}
	sel := remote("freshdesk", "https://evil.example/fd")
	attachClaude(sel, map[string]string{"freshdesk": "freshdesk"}, tokens, time.Now())
	if authOf(sel, "freshdesk") != nil {
		t.Fatal("a token was sent to a URL it was not issued for")
	}
}

// Plugin servers are stored under their namespaced name.
func TestClaudeTokenForPluginServer(t *testing.T) {
	tokens := []claudeToken{{ServerName: "plugin:cloudflare:cf-api", ServerURL: "https://cf", AccessToken: "tok"}}
	sel := remote("cf-api", "https://cf")
	attachClaude(sel, map[string]string{"cf-api": "plugin:cloudflare:cf-api"}, tokens, time.Now())
	if authOf(sel, "cf-api") != "Bearer tok" {
		t.Error("the plugin server's token was not found under its Claude name")
	}
}

// An expired token is reported, not sent and never refreshed: refreshing a
// rotating refresh token here would sign Claude out.
func TestClaudeTokenExpired(t *testing.T) {
	now := time.Now()
	tokens := []claudeToken{{ServerName: "s", ServerURL: "https://s", AccessToken: "old", ExpiresAt: now.Add(-time.Minute).UnixMilli()}}
	sel := remote("s", "https://s")
	notes := attachClaude(sel, nil, tokens, now)
	if authOf(sel, "s") != nil || notes["s"] != AuthClaudeExpired {
		t.Errorf("auth = %v, note = %q", authOf(sel, "s"), notes["s"])
	}
}

// A header written in the catalog, or attached from `mcpick login`, wins.
func TestClaudeTokenDoesNotOverride(t *testing.T) {
	tokens := []claudeToken{{ServerName: "s", ServerURL: "https://s", AccessToken: "claude"}}
	sel := spec.Selection{Names: []string{"s"}, Specs: map[string]map[string]any{"s": {
		"url": "https://s", "headers": map[string]any{"Authorization": "Bearer mine"},
	}}}
	notes := attachClaude(sel, nil, tokens, time.Now())
	if authOf(sel, "s") != "Bearer mine" || notes["s"] != "" {
		t.Error("Claude's token replaced a more specific one")
	}
}

// With several entries for one server, the longest-lived is the current one.
func TestClaudeTokenPicksNewest(t *testing.T) {
	now := time.Now()
	tokens := []claudeToken{
		{ServerName: "s", ServerURL: "https://s", AccessToken: "old", ExpiresAt: now.Add(time.Minute).UnixMilli()},
		{ServerName: "s", ServerURL: "https://s", AccessToken: "new", ExpiresAt: now.Add(time.Hour).UnixMilli()},
		{ServerName: "s", ServerURL: "https://s", AccessToken: ""}, // an entry without a token
	}
	sel := remote("s", "https://s")
	attachClaude(sel, nil, tokens, now)
	if authOf(sel, "s") != "Bearer new" {
		t.Errorf("auth = %v", authOf(sel, "s"))
	}
}

func TestLoadClaudeTokensFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{
  "claudeAiOauth": {"accessToken": "not-for-mcp"},
  "mcpOAuth": {"fd|abc": {"serverName": "fd", "serverUrl": "https://fd", "accessToken": "t", "expiresAt": 0}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sel := remote("fd", "https://fd")
	if notes := AttachClaude(sel, nil); notes["fd"] != AuthClaude || authOf(sel, "fd") != "Bearer t" {
		t.Errorf("notes = %v, auth = %v", notes, authOf(sel, "fd"))
	}
}

func TestExplainExpired(t *testing.T) {
	if got := ExplainExpired("HTTP 401: Unauthorized"); got != "Claude Code's token for this server has expired; reconnect it in Claude (/mcp)" {
		t.Errorf("got %q", got)
	}
}
