package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cajbecu/mcpick/internal/spec"
)

// standIn makes srv answer for a public-looking name: the URL's host is
// mapped to the listener, and — when allowed — the listener passes the
// dial-time check as a public host would. The mapping is undone after the
// test. It returns the URL to use in a spec.
func standIn(t *testing.T, name string, srv *httptest.Server, allowed bool) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort(name, u.Port())
	if publicTest.names == nil {
		publicTest.names, publicTest.allow = map[string]string{}, map[string]bool{}
	}
	publicTest.names[addr] = u.Host
	if allowed {
		publicTest.allow[u.Host] = true
	}
	t.Cleanup(func() {
		delete(publicTest.names, addr)
		delete(publicTest.allow, u.Host)
	})
	return "http://" + addr + u.Path
}

// A public name that resolves to a private address is refused when it is
// dialled, not trusted on its spelling: that is what stops DNS rebinding
// and a *.nip.io-style name from turning a harmless-looking URL into a
// request to the user's own machine. The same stand-in, allowed, is
// reachable — so it is the check that refused, not the harness.
func TestPublicOnlyRefusesNameResolvingToPrivateAddress(t *testing.T) {
	srv := fakeServer(t, "inside", []string{"one"}, false)
	u := standIn(t, "pub.example.test", srv, false)
	res := ProbeWith(context.Background(), "s", map[string]any{"type": "http", "url": u}, 5*time.Second, Options{PublicOnly: true})
	if res.OK || !strings.Contains(res.Err, "private address") {
		t.Fatalf("res = %+v; the dial should have been refused", res)
	}
	if kind := ErrorKind(res.Err); kind != "private" {
		t.Errorf("ErrorKind = %q, want private", kind)
	}
	ok := standIn(t, "pub.example.test", srv, true)
	if res := ProbeWith(context.Background(), "s", map[string]any{"type": "http", "url": ok}, 5*time.Second, Options{PublicOnly: true}); !res.OK || res.Tools != 1 {
		t.Fatalf("the stand-in for a public host was not reachable: %+v", res)
	}
}

// A redirect is a new connection, and the check runs on it: a public host
// that answers 307 into a private address is refused there, while without
// the option the redirect is followed. ProbeAllWith carries the option per
// server.
func TestPublicOnlyRefusesRedirectToPrivateAddress(t *testing.T) {
	inside := fakeServer(t, "inside", []string{"one"}, false)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inside.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(front.Close)
	u := standIn(t, "front.example.test", front, true)
	// plain goes to the front by its real address, over the default
	// transport, where the mapping does not exist and nothing is checked.
	sel := spec.Selection{Names: []string{"guarded", "plain"}, Specs: map[string]map[string]any{
		"guarded": {"type": "http", "url": u}, "plain": {"type": "http", "url": front.URL}}}
	res := ProbeAllWith(context.Background(), sel, 5*time.Second, 2, map[string]Options{"guarded": {PublicOnly: true}})
	if res[0].OK || ErrorKind(res[0].Err) != "private" {
		t.Errorf("guarded = %+v; the redirect into 127.0.0.1 should have been refused", res[0])
	}
	if !res[1].OK || res[1].Server.Name != "inside" {
		t.Errorf("plain = %+v; without the option the redirect is followed", res[1])
	}
}

// The legacy SSE transport opens its stream through the same checked
// transport: the GET to a private address is refused before any header
// leaves.
func TestPublicOnlyCoversSSEStream(t *testing.T) {
	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: endpoint\ndata: /msg\n\n")
	}))
	t.Cleanup(srv.Close)
	u := standIn(t, "sse.example.test", srv, false)
	res := ProbeWith(context.Background(), "s", map[string]any{"type": "sse", "url": u,
		"headers": map[string]any{"Authorization": "Bearer literal"}}, 5*time.Second, Options{PublicOnly: true})
	if res.OK || ErrorKind(res.Err) != "private" {
		t.Fatalf("res = %+v", res)
	}
	select {
	case h := <-seen:
		t.Fatalf("the server was reached (Authorization %q)", h)
	default:
	}
}
