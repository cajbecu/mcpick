package trust

import (
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
)

func remote(u string, headers map[string]any) map[string]any {
	sp := map[string]any{"type": "http", "url": u}
	if headers != nil {
		sp["headers"] = headers
	}
	return sp
}

// A `${VAR}` anywhere in the URL or a header value means the user's
// environment would leave with the request; `$${` is the escape for a
// literal and is allowed, as is any literal the file already holds — an
// Authorization the author wrote is the author's, not the user's.
func TestExposurePlaceholders(t *testing.T) {
	for name, tc := range map[string]struct {
		sp   map[string]any
		want string
	}{
		"placeholder in the url":             {remote("https://api.example.com/mcp?key=${API_KEY}", nil), "sends ${API_KEY} to its host"},
		"placeholder with a default":         {remote("https://api.example.com/${REGION:-eu}/mcp", nil), "sends ${REGION} to its host"},
		"placeholder in a header":            {remote("https://api.example.com/mcp", map[string]any{"Authorization": "Bearer ${TOKEN}"}), "sends ${TOKEN} to its host"},
		"several, named once each":           {remote("https://api.example.com/${A}/${B}", map[string]any{"X": "${A}"}), "sends ${A}, ${B} to its host"},
		"malformed placeholder still counts": {remote("https://api.example.com/mcp?x=${not valid", nil), "sends ${not valid} to its host"},
		"escaped placeholder is a literal":   {remote("https://api.example.com/mcp?x=$${NOT_EXPANDED}", nil), ""},
		"escaped in a header":                {remote("https://api.example.com/mcp", map[string]any{"X-Note": "$${LITERAL}"}), ""},
		"literal authorization header":       {remote("https://api.example.com/mcp", map[string]any{"Authorization": "Bearer xxx"}), ""},
		"plain public url":                   {remote("https://api.example.com/mcp", nil), ""},
		"stdio is not remote":                {stdio("uvx", []any{"${X}"}, nil), ""},
	} {
		if got := Exposure(tc.sp); got != tc.want {
			t.Errorf("%s: Exposure = %q, want %q", name, got, tc.want)
		}
	}
	// A placeholder name is catalog text, and goes through Safe.
	if got := Exposure(remote("https://api.example.com/${X\x1b[2J}", nil)); strings.Contains(got, "\x1b") {
		t.Errorf("Exposure = %q carries a terminal escape", got)
	}
}

// Every form of a local or private host is refused on the literal, before
// any lookup: a name in the suffixes that never resolve publicly, a name
// with no dot, and an IP literal in any of the ranges — IPv4-mapped forms
// included.
func TestPrivateHostForms(t *testing.T) {
	private := []string{
		"localhost", "LOCALHOST", "localhost.", "app.localhost", "printer.local", "db.internal", "nas.lan",
		"router.home.arpa", "intranet", "x",
		"127.0.0.1", "127.8.9.10", "10.0.0.5", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "100.127.255.254", "0.0.0.0", "0.1.2.3", "255.255.255.255", "224.0.0.1",
		"::1", "::", "fc00::1", "fd12:3456::1", "fe80::1", "fe80::1%eth0", "[fe80::1]", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.1.2.3", "::ffff:192.168.0.1", "::ffff:169.254.1.1", "::ffff:100.64.0.1",
		"::127.0.0.1", "64:ff9b::7f00:1", "64:ff9b::10.0.0.1", "",
		"192.0.0.1", "192.0.0.255", "198.18.0.1", "198.19.255.255", "240.0.0.1", "250.1.2.3",
		"fec0::1", "feff::1", "64:ff9b:1::1", "64:ff9b:1:ffff::8.8.8.8", "::ffff:198.18.0.1", "64:ff9b::c612:1",
		"239.255.255.255",
	}
	for _, h := range private {
		if !PrivateHost(h) {
			t.Errorf("PrivateHost(%q) = false, want true", h)
		}
	}
	public := []string{
		"api.example.com", "example.com.", "mcp.example.internal.example.com", "8.8.8.8", "1.1.1.1", "100.63.255.255",
		"100.128.0.1", "172.15.0.1", "172.32.0.1", "2606:4700:4700::1111", "[2606:4700:4700::1111]",
		"::ffff:8.8.8.8", "64:ff9b::808:808", "local.example.com", "lan.example.org",
		"192.0.1.1", "192.0.2.1", "198.17.255.255", "198.20.0.1", "223.255.255.254", "64:ff9b:2::1", "64:ff9a::1",
	}
	for _, h := range public {
		if PrivateHost(h) {
			t.Errorf("PrivateHost(%q) = true, want false", h)
		}
	}
}

// The same rule on parsed addresses, as the dialer sees them.
func TestPrivateIPMappedForms(t *testing.T) {
	for _, s := range []string{"::ffff:127.0.0.1", "::ffff:172.20.0.1", "64:ff9b::c0a8:1", "::a00:1"} {
		if !PrivateIP(net.ParseIP(s)) {
			t.Errorf("PrivateIP(%s) = false", s)
		}
	}
	if PrivateIP(net.ParseIP("::ffff:93.184.216.34")) || PrivateIP(net.ParseIP("2001:db8::1")) {
		t.Error("a mapped public address, or a public IPv6 one, was called private")
	}
	if !PrivateIP(nil) {
		t.Error("nil is not an address to contact")
	}
}

// Through a proxy the address reached cannot be checked, so a remote the
// environment routes through one falls back to "check to measure".
func TestExposureProxyFallsBackToCheck(t *testing.T) {
	sp := remote("https://api.example.com/mcp", nil)
	if got := Exposure(sp); got != "" {
		t.Fatalf("Exposure without a proxy = %q", got)
	}
	orig := proxyFor
	t.Cleanup(func() { proxyFor = orig })
	proxyFor = func(req *http.Request) (*url.URL, error) {
		if req.URL.Host == "api.example.com" {
			return url.Parse("http://proxy.corp:3128")
		}
		return nil, nil
	}
	if got := Exposure(sp); got != "through a proxy" {
		t.Errorf("Exposure with a proxy = %q", got)
	}
	if got := Exposure(remote("https://other.example.com/mcp", nil)); got != "" {
		t.Errorf("a host the proxy does not apply to = %q", got)
	}
	// A private host is refused before the proxy is consulted.
	if got := Exposure(remote("http://10.0.0.5/mcp", nil)); got != "private address" {
		t.Errorf("private host with a proxy = %q", got)
	}
}

// The gate on repository remotes: harmless ones run onto public addresses
// only; the others wait for a check or the catalog trust, and once the
// catalog is trusted every one of them runs without the restriction.
// Checked ones run as before.
func TestGateHarmlessRemote(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "trust.json"))
	harmless := catalog.Server{Name: "pub", Origin: catalog.OriginProject, Spec: remote("https://api.example.com/mcp", map[string]any{"Authorization": "Bearer xxx"})}
	envy := catalog.Server{Name: "envy", Origin: catalog.OriginProject, Spec: remote("https://api.example.com/mcp", map[string]any{"X-Token": "${TOKEN}"})}
	lan := catalog.Server{Name: "lan", Origin: catalog.OriginProject, Spec: remote("http://192.168.1.10/mcp", nil)}
	global := catalog.Server{Name: "mine", Origin: catalog.OriginUser, Spec: remote("http://192.168.1.10/mcp", nil)}

	sc := Scope{Store: s, Root: "/proj"}
	if v := Gate(sc, harmless, false); !v.Run || !v.PublicOnly {
		t.Errorf("harmless unchecked = %+v, want run, public only", v)
	}
	if v := Gate(sc, harmless, true); !v.Run || v.PublicOnly {
		t.Errorf("harmless checked = %+v, want run without the restriction", v)
	}
	if v := Gate(sc, envy, false); v.Run || v.NeedsTrust || !strings.Contains(v.Reason, "sends ${TOKEN} to its host") {
		t.Errorf("placeholder unchecked = %+v", v)
	}
	if v := Gate(sc, lan, false); v.Run || !strings.Contains(v.Reason, "private address") || v.CatalogChanged {
		t.Errorf("private unchecked = %+v", v)
	}
	if v := Gate(sc, global, false); !v.Run || v.PublicOnly {
		t.Errorf("the user's own remote = %+v; the rule is about the repository's", v)
	}

	sc.Catalog = CatalogTrusted
	for _, srv := range []catalog.Server{harmless, envy, lan} {
		if v := Gate(sc, srv, false); !v.Run || v.PublicOnly {
			t.Errorf("catalog trusted, %s = %+v, want run without the restriction", srv.Name, v)
		}
	}
	cmd := catalog.Server{Name: "tool", Origin: catalog.OriginProject, Spec: stdio("uvx", nil, nil)}
	if v := Gate(sc, cmd, true); v.Run || !v.NeedsTrust {
		t.Errorf("catalog trusted, stdio = %+v; a command keeps its own gate", v)
	}

	sc.Catalog = CatalogChanged
	if v := Gate(sc, envy, false); v.Run || !v.CatalogChanged {
		t.Errorf("catalog changed, placeholder = %+v, want skipped and flagged", v)
	}
	if v := Gate(sc, harmless, false); !v.Run || !v.PublicOnly || v.CatalogChanged {
		t.Errorf("catalog changed, harmless = %+v, want the harmless rule", v)
	}
}

// The T screen shows a header's name and hides its value, except a value
// that is a placeholder: that is the user's own, and what the consent is
// about.
func TestDescribeRemoteMasksLiteralHeaderValues(t *testing.T) {
	got := DescribeRemote(remote("https://api.example.com/mcp", map[string]any{
		"Authorization": "Bearer literal-secret", "X-Env": "${TOKEN}", "X-Mixed": "Bearer ${TOKEN}"}))
	want := "https://api.example.com/mcp  headers: Authorization: ****, X-Env: ${TOKEN}, X-Mixed: \"Bearer ${TOKEN}\""
	if got != want {
		t.Errorf("DescribeRemote = %q, want %q", got, want)
	}
	if got := DescribeRemote(remote("https://api.example.com/mcp", nil)); got != "https://api.example.com/mcp" {
		t.Errorf("DescribeRemote without headers = %q", got)
	}
}

// {UUID} is the --uid, by default the machine's hostname: sending it to a host
// the repository chose is sending something of the user's.
func TestExposureCountsTheSessionID(t *testing.T) {
	for _, sp := range []map[string]any{
		{"type": "http", "url": "https://mcp.example.com/mcp?session={UUID}"},
		{"type": "http", "url": "https://mcp.example.com/mcp", "headers": map[string]any{"X-Session": "{UUID}"}},
	} {
		if got := Exposure(sp); !strings.Contains(got, "{UUID}") {
			t.Errorf("Exposure(%v) = %q", sp, got)
		}
	}
}
