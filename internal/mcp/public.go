package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/cajbecu/mcpick/internal/trust"
)

// Options tune one connection.
type Options struct {
	// PublicOnly refuses every connection to a private, local or otherwise
	// non-public address, at the moment it is dialled and on the address
	// actually dialled — so a public name that resolves to 127.0.0.1, a
	// redirect into the LAN and a DNS answer that changes between two
	// lookups are all refused — and uses no proxy, since through one the
	// address reached could not be seen. It is for measuring a repository's
	// remote server that has not been checked: allowed only because its
	// spec sends nothing of the user's, which a private destination would
	// undo (see trust.Exposure).
	PublicOnly bool
	// Written is the URL as the catalog wrote it, `${VAR}` and `{UUID}`
	// unexpanded. Error text shows it in place of the expanded URL the
	// request was sent to (see redactErr); empty, the expanded URL is
	// shown with its query values and userinfo redacted.
	Written string
	// Raw is the spec as the catalog wrote it. When set, a probe's error
	// text has the values expansion put into the spec — and headers
	// attached since — replaced by their references (spec.Scrubber):
	// a server or a resolver may echo them back.
	Raw map[string]any
}

// publicClient and publicStream are the clients PublicOnly connections use:
// the same timeouts as httpClient and the SSE stream's, over a transport
// whose dialer checks each address. Built once, on first use.
var (
	publicOnce   sync.Once
	publicClient *http.Client
	publicStream *http.Client
)

func publicClients() (client, stream *http.Client) {
	publicOnce.Do(func() {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: refusePrivate}
		tr := &http.Transport{
			Proxy:                 nil, // never through a proxy: the address reached must be the one checked
			DialContext:           publicDial(d),
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
		publicClient = &http.Client{Transport: tr, Timeout: httpClient.Timeout}
		publicStream = &http.Client{Transport: tr}
	})
	return publicClient, publicStream
}

// refusePrivate is the dialer's Control hook: it sees the resolved address
// of every connection attempt, redirects included, and refuses one that is
// not public. The message carries "private address", which ErrorKind
// reduces to `private`.
func refusePrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if publicTest.allow[address] {
		return nil
	}
	if trust.PrivateHost(host) {
		return fmt.Errorf("%s is a private address; not contacted", host)
	}
	return nil
}

// publicDial is the transport's dialer. The name a test maps (publicTest)
// is swapped for its address before the dial, so the Control hook judges
// what would really be reached; outside tests it is the dialer as it is.
func publicDial(d *net.Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if mapped, ok := publicTest.names[address]; ok {
			address = mapped
		}
		return d.DialContext(ctx, network, address)
	}
}

// publicTest lets the tests stand a loopback listener in for a public host:
// names maps a host:port as written in a URL to the address to dial
// instead, and allow lists the ip:port that pass the check although they
// are loopback — the stand-in itself. Both are empty outside tests; the
// check itself is never replaced.
var publicTest struct {
	names map[string]string
	allow map[string]bool
}
