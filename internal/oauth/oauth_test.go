package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/spec"
)

// fakeAuthServer is an authorization server that implements just enough of the
// MCP profile: protected-resource metadata, authorization-server metadata,
// dynamic client registration, and a PKCE authorization-code exchange.
type fakeAuthServer struct {
	*httptest.Server
	t          *testing.T
	challenges map[string]string // code -> code_challenge
	registered int
	refreshes  int
	lastScope  string
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	f := &fakeAuthServer{t: t, challenges: map[string]string{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"authorization_servers": []string{f.URL},
			"scopes_supported":      []string{"mcp:read"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 f.URL,
			"authorization_endpoint": f.URL + "/authorize",
			"token_endpoint":         f.URL + "/token",
			"registration_endpoint":  f.URL + "/register",
			"scopes_supported":       []string{"mcp:read"},
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		uris, _ := body["redirect_uris"].([]any)
		if len(uris) != 1 || !strings.HasPrefix(fmt.Sprint(uris[0]), "http://127.0.0.1:") {
			f.t.Errorf("redirect_uris = %v, want one loopback URI", uris)
		}
		f.registered++
		writeJSON(w, map[string]any{"client_id": "client-123"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			f.t.Errorf("PKCE is missing from the authorization request: %v", q)
		}
		f.lastScope = q.Get("scope")
		code := "code-" + q.Get("state")
		f.challenges[code] = q.Get("code_challenge")

		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rq := redirect.Query()
		rq.Set("code", code)
		rq.Set("state", q.Get("state"))
		redirect.RawQuery = rq.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			code := r.Form.Get("code")
			want, ok := f.challenges[code]
			if !ok {
				http.Error(w, "unknown code", http.StatusBadRequest)
				return
			}
			// The verifier must hash to the challenge sent at /authorize.
			if challenge(r.Form.Get("code_verifier")) != want {
				http.Error(w, "PKCE verification failed", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{
				"access_token": "access-1", "refresh_token": "refresh-1",
				"token_type": "Bearer", "expires_in": 3600, "scope": "mcp:read",
			})
		case "refresh_token":
			f.refreshes++
			if r.Form.Get("refresh_token") != "refresh-1" {
				http.Error(w, "bad refresh token", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{
				"access_token": "access-2", "token_type": "Bearer", "expires_in": 3600,
			})
		default:
			http.Error(w, "unsupported grant", http.StatusBadRequest)
		}
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// visit plays the part of the browser: fetch the authorization URL and let the
// redirect land on mcpick's loopback listener.
func visit(t *testing.T) func(string) error {
	t.Helper()
	return func(raw string) error {
		go func() {
			resp, err := http.Get(raw)
			if err != nil {
				t.Errorf("authorization request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
}

func TestLoginFullFlow(t *testing.T) {
	as := newFakeAuthServer(t)
	v := spec.View{Transport: "http", URL: as.URL + "/mcp"}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tok, err := Login(ctx, "srv", v, visit(t))
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-1" {
		t.Errorf("access token = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "refresh-1" {
		t.Errorf("refresh token = %q", tok.RefreshToken)
	}
	if tok.ClientID != "client-123" {
		t.Errorf("client id = %q, want the dynamically registered one", tok.ClientID)
	}
	if as.registered != 1 {
		t.Errorf("registered %d times, want 1", as.registered)
	}
	// Scopes advertised by the resource must reach the authorization request.
	if as.lastScope != "mcp:read" {
		t.Errorf("scope = %q, want mcp:read", as.lastScope)
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("expires_in should have produced an expiry")
	}
}

func TestLoginRejectsMismatchedState(t *testing.T) {
	as := newFakeAuthServer(t)
	v := spec.View{Transport: "http", URL: as.URL + "/mcp"}

	// A callback that did not come from this flow must not be accepted: it is
	// how an attacker would inject their own authorization code.
	forge := func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		redirect, err := url.Parse(u.Query().Get("redirect_uri"))
		if err != nil {
			return err
		}
		q := redirect.Query()
		q.Set("code", "attacker-code")
		q.Set("state", "not-the-state-we-sent")
		redirect.RawQuery = q.Encode()
		go func() {
			resp, err := http.Get(redirect.String())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := Login(ctx, "srv", v, forge); err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("err = %v, want a state mismatch", err)
	}
}

func TestLoginReportsDenial(t *testing.T) {
	as := newFakeAuthServer(t)
	v := spec.View{Transport: "http", URL: as.URL + "/mcp"}

	deny := func(raw string) error {
		u, _ := url.Parse(raw)
		redirect, _ := url.Parse(u.Query().Get("redirect_uri"))
		q := redirect.Query()
		q.Set("error", "access_denied")
		redirect.RawQuery = q.Encode()
		go func() {
			resp, err := http.Get(redirect.String())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := Login(ctx, "srv", v, deny); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("err = %v, want the denial reported", err)
	}
}

func TestLoginRefusesLocalServer(t *testing.T) {
	_, err := Login(context.Background(), "local", spec.View{Command: "uvx"}, PrintURL)
	if err == nil || !strings.Contains(err.Error(), "local process") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoveryFallsBackToTheOrigin(t *testing.T) {
	// Most single-tenant deployments publish no protected-resource document
	// and are their own authorization server.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"authorization_endpoint": "https://example.com/authorize",
			"token_endpoint":         "https://example.com/token",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	meta, _, err := discoverAuth(context.Background(), srv.URL+"/deep/path/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.TokenEndpoint != "https://example.com/token" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestWellKnownPlacesSegmentBeforeThePath(t *testing.T) {
	u, err := url.Parse("https://example.com/tenant/mcp")
	if err != nil {
		t.Fatal(err)
	}
	got := wellKnown(u, "oauth-protected-resource")
	want := "https://example.com/.well-known/oauth-protected-resource/tenant/mcp"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestAttachTokensAddsHeaderAndRefreshes(t *testing.T) {
	as := newFakeAuthServer(t)
	t.Setenv("MCPICK_HOME", t.TempDir())

	store := LoadStore()
	url := "https://example.com/mcp"
	store.Tokens[Key("srv", url)] = Token{
		AccessToken: "stale", RefreshToken: "refresh-1", TokenType: "Bearer",
		ExpiresAt: time.Now().Add(-time.Minute), TokenEndpoint: as.URL + "/token",
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	sel := oneSel("srv", map[string]any{"type": "http", "url": url})
	refreshed := Attach(sel)
	if len(refreshed) != 1 || refreshed[0] != "srv" {
		t.Fatalf("refreshed = %v, want [srv]", refreshed)
	}
	headers, _ := sel.Specs["srv"]["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer access-2" {
		t.Errorf("Authorization = %v, want the refreshed token", headers["Authorization"])
	}
	if as.refreshes != 1 {
		t.Errorf("refreshed %d times, want 1", as.refreshes)
	}

	// The new token is persisted, so the next launch does not refresh again.
	again := LoadStore().Tokens[Key("srv", url)]
	if again.AccessToken != "access-2" {
		t.Errorf("stored token = %q, want it updated", again.AccessToken)
	}
	if again.RefreshToken != "refresh-1" {
		t.Errorf("a refresh response without a new refresh_token must keep the old one, got %q", again.RefreshToken)
	}
}

// A header the user wrote in the catalog is deliberate and must win.
func TestAttachTokensLeavesCatalogHeaderAlone(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	url := "https://example.com/mcp"
	store := LoadStore()
	store.Tokens[Key("srv", url)] = Token{AccessToken: "stored", TokenType: "Bearer"}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	sel := oneSel("srv", map[string]any{
		"type": "http", "url": url,
		"headers": map[string]any{"Authorization": "Bearer from-catalog"},
	})
	Attach(sel)
	headers := sel.Specs["srv"]["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer from-catalog" {
		t.Errorf("Authorization = %v", headers["Authorization"])
	}
}

func TestTokenKeyChangesWithURL(t *testing.T) {
	if Key("srv", "https://a/mcp") == Key("srv", "https://b/mcp") {
		t.Error("pointing a server at a new host must not reuse the old token")
	}
}

func TestTokenStoreIsPrivate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCPICK_HOME", dir)
	store := LoadStore()
	store.Tokens["a@b"] = Token{AccessToken: "x"}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if unixPerms && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func oneSel(name string, sp map[string]any) spec.Selection {
	return spec.Selection{Names: []string{name}, Specs: map[string]map[string]any{name: sp}}
}

// unixPerms says whether mode bits mean anything here. Windows reports 0666 or
// 0777 whatever was asked for; access there is governed by per-user ACLs.
var unixPerms = runtime.GOOS != "windows"
