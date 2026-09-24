// Package oauth implements the authorization flow the MCP specification
// describes for remote servers — protected-resource discovery, dynamic client
// registration, PKCE and a loopback redirect — and stores the tokens.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
)

// Remote MCP servers increasingly want OAuth rather than a static bearer token.
// mcpick implements the flow the MCP specification describes: discover the
// authorization server from the resource, register dynamically, then run an
// authorization-code exchange with PKCE against a loopback redirect.
//
// Tokens live in the state directory, keyed by server name and URL, and are
// attached as an Authorization header when a selection is rendered or probed.

type Token struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	TokenType     string    `json:"token_type,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	ClientID      string    `json:"client_id,omitempty"`
	ClientSecret  string    `json:"client_secret,omitempty"`
	TokenEndpoint string    `json:"token_endpoint,omitempty"`
}

func (t Token) header() string {
	typ := t.TokenType
	if typ == "" {
		typ = "Bearer"
	}
	return typ + " " + t.AccessToken
}

func (t Token) expiringWithin(d time.Duration) bool {
	return !t.ExpiresAt.IsZero() && time.Now().Add(d).After(t.ExpiresAt)
}

type Store struct {
	path   string
	Tokens map[string]Token `json:"tokens"`
}

// Key ties a token to the URL it was issued for, so pointing a catalog
// entry at a different host does not silently reuse the old credential.
func Key(name, rawURL string) string {
	return name + "@" + fsutil.ShortHash(rawURL)
}

func LoadStore() *Store {
	s := &Store{path: filepath.Join(fsutil.StateDir(), "tokens.json"), Tokens: map[string]Token{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	if s.Tokens == nil {
		s.Tokens = map[string]Token{}
	}
	return s
}

func (s *Store) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.path, append(data, '\n'), 0o600)
}

// Attach adds a stored Authorization header to every selected server that
// has one and is not already carrying its own. A catalog header always wins:
// the user wrote it on purpose.
func Attach(sel spec.Selection) []string {
	store := LoadStore()
	if len(store.Tokens) == 0 {
		return nil
	}
	var refreshed []string
	for _, n := range sel.Names {
		v := spec.ViewOf(sel.Specs[n])
		if !v.Remote() || hasAuthHeader(v.Headers) {
			continue
		}
		key := Key(n, v.URL)
		tok, ok := store.Tokens[key]
		if !ok {
			continue
		}
		if tok.expiringWithin(time.Minute) && tok.RefreshToken != "" {
			newTok, err := refreshToken(context.Background(), tok)
			if err == nil {
				store.Tokens[key] = newTok
				tok = newTok
				refreshed = append(refreshed, n)
			}
		}
		headers, _ := sel.Specs[n]["headers"].(map[string]any)
		if headers == nil {
			headers = map[string]any{}
			sel.Specs[n]["headers"] = headers
		}
		headers["Authorization"] = tok.header()
	}
	if len(refreshed) > 0 {
		_ = store.Save()
	}
	return refreshed
}

func hasAuthHeader(h map[string]string) bool {
	for k, v := range h {
		if strings.EqualFold(k, "Authorization") && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// --- discovery ------------------------------------------------------------

type authMeta struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
}

type resourceMeta struct {
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

func getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// wellKnown builds an RFC 8414 style URL: the well-known segment goes between
// the host and the resource path, not at the end of it.
func wellKnown(base *url.URL, suffix string) string {
	path := strings.TrimSuffix(base.Path, "/")
	u := *base
	u.Path = "/.well-known/" + suffix + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func discoverAuth(ctx context.Context, resourceURL string) (authMeta, []string, error) {
	base, err := url.Parse(resourceURL)
	if err != nil {
		return authMeta{}, nil, err
	}

	var scopes []string
	issuers := []string{}
	var rm resourceMeta
	if err := getJSON(ctx, wellKnown(base, "oauth-protected-resource"), &rm); err == nil {
		issuers = append(issuers, rm.AuthorizationServers...)
		scopes = rm.ScopesSupported
	}
	// A server that publishes no resource metadata is assumed to be its own
	// authorization server, which is what most single-tenant deployments are.
	origin := *base
	origin.Path, origin.RawQuery, origin.Fragment = "", "", ""
	issuers = append(issuers, origin.String())

	var lastErr error
	for _, issuer := range issuers {
		iu, err := url.Parse(issuer)
		if err != nil {
			lastErr = err
			continue
		}
		for _, suffix := range []string{"oauth-authorization-server", "openid-configuration"} {
			var meta authMeta
			if err := getJSON(ctx, wellKnown(iu, suffix), &meta); err != nil {
				lastErr = err
				continue
			}
			if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
				lastErr = fmt.Errorf("%s: metadata has no endpoints", issuer)
				continue
			}
			if len(scopes) == 0 {
				scopes = meta.ScopesSupported
			}
			return meta, scopes, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no authorization server found for %s", resourceURL)
	}
	return authMeta{}, nil, lastErr
}

func registerClient(ctx context.Context, meta authMeta, redirectURI string) (string, string, error) {
	if meta.RegistrationEndpoint == "" {
		return "", "", fmt.Errorf("the server offers no dynamic client registration; " +
			"register mcpick manually and put the credentials in the catalog")
	}
	body, err := json.Marshal(map[string]any{
		"client_name":                "mcpick",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("registration failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", "", err
	}
	if out.ClientID == "" {
		return "", "", fmt.Errorf("registration returned no client_id")
	}
	return out.ClientID, out.ClientSecret, nil
}

// --- the flow -------------------------------------------------------------

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type callbackResult struct {
	code  string
	state string
	err   string
}

// loopbackServer waits for the authorization server to redirect the browser
// back. Binding port 0 and registering that exact URI is what lets the flow
// work without the user configuring anything.
func loopbackServer(ctx context.Context) (net.Listener, string, <-chan callbackResult, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, err
	}
	ch := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := callbackResult{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != "" || res.code == "" {
			fmt.Fprintf(w, "<h1>mcpick</h1><p>Authorization failed: %s</p>", html.EscapeString(res.err))
		} else {
			fmt.Fprint(w, "<h1>mcpick</h1><p>Authorized. You can close this tab.</p>")
		}
		select {
		case ch <- res:
		default:
		}
	})
	// A bare http.Serve has no header timeout, so a client that connects and
	// says nothing would hold the listener for the whole login.
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) //nolint:errcheck // ends when the caller closes ln
	return ln, fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port), ch, nil
}

func exchange(ctx context.Context, meta authMeta, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Token{}, fmt.Errorf("token endpoint: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Token{}, err
	}
	if out.AccessToken == "" {
		return Token{}, fmt.Errorf("token endpoint returned no access_token")
	}
	tok := Token{
		AccessToken:   out.AccessToken,
		RefreshToken:  out.RefreshToken,
		TokenType:     out.TokenType,
		Scope:         out.Scope,
		TokenEndpoint: meta.TokenEndpoint,
	}
	if out.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return tok, nil
}

func refreshToken(ctx context.Context, old Token) (Token, error) {
	if old.TokenEndpoint == "" || old.RefreshToken == "" {
		return old, fmt.Errorf("nothing to refresh with")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {old.RefreshToken},
	}
	if old.ClientID != "" {
		form.Set("client_id", old.ClientID)
	}
	if old.ClientSecret != "" {
		form.Set("client_secret", old.ClientSecret)
	}
	tok, err := exchange(ctx, authMeta{TokenEndpoint: old.TokenEndpoint}, form)
	if err != nil {
		return old, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = old.RefreshToken
	}
	tok.ClientID, tok.ClientSecret = old.ClientID, old.ClientSecret
	return tok, nil
}

// Login runs the whole flow for one server and stores the result.
func Login(ctx context.Context, name string, v spec.View, open func(string) error) (Token, error) {
	if !v.Remote() {
		return Token{}, fmt.Errorf("%s is a local process; OAuth applies to remote servers", name)
	}
	meta, scopes, err := discoverAuth(ctx, v.URL)
	if err != nil {
		return Token{}, err
	}

	ln, redirectURI, callbacks, err := loopbackServer(ctx)
	if err != nil {
		return Token{}, err
	}
	defer ln.Close()

	clientID, clientSecret, err := registerClient(ctx, meta, redirectURI)
	if err != nil {
		return Token{}, err
	}

	verifier, err := randomString(32)
	if err != nil {
		return Token{}, err
	}
	stateParam, err := randomString(16)
	if err != nil {
		return Token{}, err
	}

	authURL, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return Token{}, err
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", stateParam)
	q.Set("code_challenge", challenge(verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("resource", v.URL)
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	authURL.RawQuery = q.Encode()

	if err := open(authURL.String()); err != nil {
		return Token{}, err
	}

	var res callbackResult
	select {
	case <-ctx.Done():
		return Token{}, ctx.Err()
	case res = <-callbacks:
	}
	if res.err != "" {
		return Token{}, fmt.Errorf("authorization denied: %s", res.err)
	}
	// Without this check a crafted redirect could inject someone else's code.
	if res.state != stateParam {
		return Token{}, fmt.Errorf("state mismatch: the callback did not come from this flow")
	}

	tok, err := exchange(ctx, meta, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
		"resource":      {v.URL},
	})
	if err != nil {
		return Token{}, err
	}
	tok.ClientID, tok.ClientSecret = clientID, clientSecret
	return tok, nil
}

// PrintURL is the default "browser": mcpick often runs inside a container where
// there is no browser to open, so the URL is printed and the user opens it.
func PrintURL(u string) error {
	fmt.Fprintln(os.Stderr, "\nOpen this URL to authorize mcpick:\n\n  "+u+"\n")
	return nil
}

// httpClient bounds every request of the flow; a hung authorization server
// must not hang a launch, which refreshes tokens on the way.
var httpClient = &http.Client{Timeout: 30 * time.Second}
