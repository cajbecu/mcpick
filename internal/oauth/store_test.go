package oauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// rotatingServer is a token endpoint that rotates the refresh token on every
// use and honours each one once, as many authorization servers do. It is
// what makes a lost save a lost login: the refresh token still in the file
// is the one the server has already retired.
type rotatingServer struct {
	*httptest.Server
	mu        sync.Mutex
	current   map[string]string // token family (the first refresh token) -> the one valid now
	refreshes int
}

func newRotatingServer(t *testing.T, families ...string) *rotatingServer {
	rs := &rotatingServer{current: map[string]string{}}
	for _, f := range families {
		rs.current[f] = f
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(20 * time.Millisecond) // long enough for parallel callers to overlap
		rs.mu.Lock()
		defer rs.mu.Unlock()
		got := r.Form.Get("refresh_token")
		family, _, _ := strings.Cut(got, "/")
		if rs.current[family] != got {
			http.Error(w, "refresh token already used", http.StatusBadRequest)
			return
		}
		rs.refreshes++
		next := fmt.Sprintf("%s/%d", family, rs.refreshes)
		rs.current[family] = next
		writeJSON(w, map[string]any{
			"access_token": "access-" + next, "refresh_token": next,
			"token_type": "Bearer", "expires_in": 3600,
		})
	})
	rs.Server = httptest.NewServer(mux)
	t.Cleanup(rs.Close)
	return rs
}

func (rs *rotatingServer) valid(family string) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.current[family]
}

// The picker measures every server at once, and each measurement attaches
// its own token. Two tokens refreshed in parallel must both end up in the
// file as rotated: a measurement that saved its stale copy of the other's
// token would leave a refresh token the server has retired, and the next
// launch would be logged out of that server. And one token measured twice
// at once is refreshed once: the second use of a rotated refresh token is
// refused by the server.
func TestParallelRefreshesKeepEveryRotatedToken(t *testing.T) {
	rs := newRotatingServer(t, "ra", "rb")
	t.Setenv("MCPICK_HOME", t.TempDir())
	urls := map[string]string{"a": "https://a.example.com/mcp", "b": "https://b.example.com/mcp"}
	for name, family := range map[string]string{"a": "ra", "b": "rb"} {
		err := Put(Key(name, urls[name]), Token{
			AccessToken: "stale-" + name, RefreshToken: family, TokenType: "Bearer",
			ExpiresAt: time.Now().Add(-time.Minute), TokenEndpoint: rs.URL + "/token",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Each server measured twice, all four at once.
	names := []string{"a", "b", "a", "b"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, len(names))
	for _, n := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			sel := oneSel(n, map[string]any{"type": "http", "url": urls[n]})
			<-start
			if _, err := Attach(sel); err != nil {
				errs <- err
			}
			headers, _ := sel.Specs[n]["headers"].(map[string]any)
			if got, _ := headers["Authorization"].(string); !strings.HasPrefix(got, "Bearer access-r") {
				errs <- fmt.Errorf("%s: Authorization = %q, want a refreshed token attached", n, got)
			}
		}(n)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	stored := LoadStore().Tokens
	for name, family := range map[string]string{"a": "ra", "b": "rb"} {
		tok := stored[Key(name, urls[name])]
		if want := rs.valid(family); tok.RefreshToken != want {
			t.Errorf("%s: stored refresh token %q, the server honours %q: a rotated token was lost", name, tok.RefreshToken, want)
		}
	}
	if rs.refreshes != 2 {
		t.Errorf("%d refreshes, want one per token", rs.refreshes)
	}
}

// A refreshed token that cannot be saved is still attached — it is good
// for this run — and the failure is returned, not dropped: the next launch
// will find the retired refresh token and the user has to know why.
func TestAttachReportsASaveItCouldNotMake(t *testing.T) {
	if !unixPerms || os.Getuid() == 0 {
		t.Skip("needs a directory this user cannot write")
	}
	rs := newRotatingServer(t, "ra")
	home := t.TempDir()
	t.Setenv("MCPICK_HOME", home)
	url := "https://a.example.com/mcp"
	err := Put(Key("a", url), Token{
		AccessToken: "stale", RefreshToken: "ra", TokenType: "Bearer",
		ExpiresAt: time.Now().Add(-time.Minute), TokenEndpoint: rs.URL + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	state := LoadStore().path[:strings.LastIndex(LoadStore().path, string(os.PathSeparator))]
	if err := os.Chmod(state, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(state, 0o700) })

	sel := oneSel("a", map[string]any{"type": "http", "url": url})
	refreshed, err := Attach(sel)
	if err == nil || !strings.Contains(err.Error(), "could not save") {
		t.Fatalf("err = %v, want the failed save reported", err)
	}
	if len(refreshed) != 1 {
		t.Errorf("refreshed = %v, want the token refreshed and attached anyway", refreshed)
	}
	if headers, _ := sel.Specs["a"]["headers"].(map[string]any); headers["Authorization"] != "Bearer access-ra/1" {
		t.Errorf("Authorization = %v, want the refreshed token", headers["Authorization"])
	}
}

// login and logout edit the file as it is, so a token stored by another
// process between the two is not lost, and forgetting says how many went.
func TestPutAndForgetMergeIntoTheFile(t *testing.T) {
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := Put("a@1", Token{AccessToken: "a1"}); err != nil {
		t.Fatal(err)
	}
	stale := LoadStore() // another process's view, from before b was stored
	if err := Put("b@1", Token{AccessToken: "b1"}); err != nil {
		t.Fatal(err)
	}
	if err := stale.Update(func(s *Store) error { s.Tokens["a@2"] = Token{AccessToken: "a2"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := LoadStore().Tokens; len(got) != 3 {
		t.Errorf("tokens = %v, want a@1, b@1 and a@2: the stale view wrote over b@1", got)
	}
	if len(stale.Tokens) != 3 {
		t.Errorf("after Update the store should hold the file's tokens, has %d", len(stale.Tokens))
	}
	n, err := Forget("a")
	if err != nil || n != 2 {
		t.Errorf("Forget(a) = %d, %v; want 2 tokens forgotten", n, err)
	}
	n, err = Forget("a")
	if err != nil || n != 0 {
		t.Errorf("Forget(a) again = %d, %v; want nothing to forget", n, err)
	}
	if got := LoadStore().Tokens; len(got) != 1 || got["b@1"].AccessToken != "b1" {
		t.Errorf("tokens = %v, want b@1 alone", got)
	}
}

// Two processes refreshing the same token — here two Stores on one file,
// without the in-process mutex between them — hold the token's lock file
// from the re-read to the save: the refresh token is spent once, the
// second finds the first's token in the file, and the file keeps the
// refresh token the server honours.
func TestTwoProcessesSpendARefreshTokenOnce(t *testing.T) {
	rs := newRotatingServer(t, "rt")
	t.Setenv("MCPICK_HOME", t.TempDir())
	key := Key("a", "https://a.example.com/mcp")
	old := Token{AccessToken: "stale", RefreshToken: "rt", TokenType: "Bearer",
		ExpiresAt: time.Now().Add(-time.Minute), TokenEndpoint: rs.URL + "/token"}
	if err := Put(key, old); err != nil {
		t.Fatal(err)
	}
	procs := []*Store{openStore(tokensPath()), openStore(tokensPath())}
	got := make([]Token, len(procs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, s := range procs {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			<-start
			tok, ok, err := refreshAt(s, key, old)
			if !ok || err != nil {
				t.Errorf("process %d: refreshed=%v err=%v", i, ok, err)
			}
			got[i] = tok
		}(i, s)
	}
	close(start)
	wg.Wait()
	if rs.refreshes != 1 {
		t.Errorf("%d refreshes, want one: the refresh token was spent twice", rs.refreshes)
	}
	if got[0].AccessToken != got[1].AccessToken || !strings.HasPrefix(got[0].AccessToken, "access-rt/") {
		t.Errorf("tokens = %q, %q; want both the one refresh's", got[0].AccessToken, got[1].AccessToken)
	}
	if stored := LoadStore().Tokens[key]; stored.RefreshToken != rs.valid("rt") {
		t.Errorf("stored refresh token %q, the server honours %q", stored.RefreshToken, rs.valid("rt"))
	}
	if left, _ := filepath.Glob(tokensPath() + ".refresh-*"); len(left) != 0 {
		t.Errorf("lock files left behind: %v", left)
	}
}
