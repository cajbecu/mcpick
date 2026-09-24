package oauth

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Claude Code keeps an OAuth token per MCP server it has connected to: in
// ~/.claude/.credentials.json on Linux, in the macOS Keychain on a Mac. When
// mcpick measures a server it can borrow that token, so a server Claude is
// signed in to answers instead of returning 401.
//
// Borrowing is read-only and narrow on purpose:
//
//   - the token is used only if the server's name and URL both match the ones
//     it was issued for, so it never reaches another host;
//   - an expired token is reported, never refreshed: servers commonly rotate
//     the refresh token on use, and refreshing here would leave Claude holding
//     a dead one and signed out;
//   - it is used for measuring only. A launch needs nothing: Claude finds its
//     own tokens for the servers mcpick hands it.

// claudeToken is one entry of Claude Code's mcpOAuth store.
type claudeToken struct {
	ServerName  string `json:"serverName"`
	ServerURL   string `json:"serverUrl"`
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt"` // milliseconds since the epoch; 0 when unknown
}

func (t claudeToken) expired(now time.Time) bool {
	return t.ExpiresAt > 0 && now.Add(30*time.Second).UnixMilli() >= t.ExpiresAt
}

// Auth notes returned by AttachClaude, shown beside a measurement.
const (
	AuthClaude        = "Claude Code token"
	AuthClaudeExpired = "Claude Code token expired"
)

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return fsutil.Home(".claude")
}

// readClaudeCredentials returns Claude Code's credentials document, or nil
// when there is none to read. Failure is not an error worth reporting: the
// measurement simply goes ahead without a token.
func readClaudeCredentials() []byte {
	if data, err := os.ReadFile(filepath.Join(claudeDir(), ".credentials.json")); err == nil {
		return data
	}
	if runtime.GOOS != "darwin" {
		return nil
	}
	// On macOS the first read shows the system's permission dialog; "Always
	// Allow" makes it silent from then on, and a refusal just means no token.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security",
		"find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return nil
	}
	return out
}

// ClaudeTokens is one read of Claude Code's token store. Load it once per
// measurement: on a Mac every read goes through the Keychain.
type ClaudeTokens struct{ list []claudeToken }

// LoadClaudeTokens reads Claude Code's tokens; an empty set when there are none
// or they cannot be read.
func LoadClaudeTokens() ClaudeTokens { return ClaudeTokens{list: loadClaudeTokens()} }

// Attach lends the tokens to sel; see AttachClaude.
func (c ClaudeTokens) Attach(sel spec.Selection, claudeNames map[string]string) map[string]string {
	return attachClaude(sel, claudeNames, c.list, time.Now())
}

func loadClaudeTokens() []claudeToken {
	data := readClaudeCredentials()
	if len(data) == 0 {
		return nil
	}
	var doc struct {
		McpOAuth map[string]claudeToken `json:"mcpOAuth"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	out := make([]claudeToken, 0, len(doc.McpOAuth))
	for _, t := range doc.McpOAuth {
		out = append(out, t)
	}
	return out
}

// AttachClaude lends Claude Code's tokens to the remote servers in sel that
// carry no Authorization header yet — neither written in the catalog nor
// attached from `mcpick login`, which both take precedence. claudeNames maps
// each server to the name Claude knows it by. The result says, per server,
// which token was used or that Claude's had expired.
func AttachClaude(sel spec.Selection, claudeNames map[string]string) map[string]string {
	return LoadClaudeTokens().Attach(sel, claudeNames)
}

func attachClaude(sel spec.Selection, claudeNames map[string]string, tokens []claudeToken, now time.Time) map[string]string {
	notes := map[string]string{}
	if len(tokens) == 0 {
		return notes
	}
	for _, n := range sel.Names {
		v := spec.ViewOf(sel.Specs[n])
		if !v.Remote() || hasAuthHeader(v.Headers) {
			continue
		}
		name := claudeNames[n]
		if name == "" {
			name = n
		}
		best, found := claudeToken{}, false
		for _, t := range tokens {
			if t.ServerName != name || t.ServerURL != v.URL || t.AccessToken == "" {
				continue
			}
			// Claude may hold several entries for one server after its
			// config changed; the one that lives longest is the current one.
			if !found || t.ExpiresAt > best.ExpiresAt {
				best, found = t, true
			}
		}
		if !found {
			continue
		}
		if best.expired(now) {
			notes[n] = AuthClaudeExpired
			continue
		}
		headers, _ := sel.Specs[n]["headers"].(map[string]any)
		if headers == nil {
			headers = map[string]any{}
			sel.Specs[n]["headers"] = headers
		}
		headers["Authorization"] = "Bearer " + best.AccessToken
		notes[n] = AuthClaude
	}
	return notes
}

// ExplainExpired rewrites a failure caused by an expired Claude Code token so
// it says so and names the fix, instead of a bare 401.
func ExplainExpired(err string) string {
	if strings.Contains(strings.ToLower(err), "401") || strings.Contains(strings.ToLower(err), "unauthorized") || err == "" {
		return "Claude Code's token for this server has expired; reconnect it in Claude (/mcp)"
	}
	return err + " (Claude Code's token for this server has expired)"
}
