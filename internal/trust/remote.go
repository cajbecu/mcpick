package trust

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/cajbecu/mcpick/internal/spec"
)

// A remote server the repository's catalog defines is measured without
// being checked only when measuring it can send nothing of the user's
// anywhere the user did not choose. Exposure is that test, over the catalog
// spec as written; the dial-time half (a public name that resolves to a
// private address) is mcp's PublicOnly option, which Gate asks for on these
// measurements.

// Exposure says why measuring a remote spec, as the catalog wrote it, would
// expose something of the user's: "" when it would not. The reasons are
// worded for the row's detail line.
//
//   - a `${VAR}` in the URL or in a header value: the user's environment
//     would be expanded into the request. `$${` is the escape for a literal
//     and is allowed, as is any literal the file already holds — nothing of
//     the user's leaves with it.
//   - a local or private host: the request would reach a service on the
//     user's machine or network, where the repository's author has no
//     business, with whatever headers the author wrote.
//   - a proxy: HTTP(S)_PROXY / ALL_PROXY apply to the URL, so the connection
//     goes to the proxy and the address actually reached cannot be checked.
func Exposure(sp map[string]any) string {
	v := spec.ViewOf(sp)
	if !v.Remote() {
		return ""
	}
	var refs []string
	refs = append(refs, placeholders(v.URL)...)
	for _, k := range spec.SortedKeys(v.Headers) {
		refs = append(refs, placeholders(v.Headers[k])...)
	}
	// {UUID} becomes --uid, by default this machine's hostname: something of
	// the user's, like a variable.
	if strings.Contains(v.URL, "{UUID}") || slices.ContainsFunc(spec.SortedKeys(v.Headers), func(k string) bool {
		return strings.Contains(v.Headers[k], "{UUID}")
	}) {
		refs = append(refs, "{UUID}")
	}
	if len(refs) > 0 {
		return "sends " + strings.Join(dedupe(refs), ", ") + " to its host"
	}
	u, err := url.Parse(v.URL)
	if err != nil || u.Host == "" {
		return "address not understood"
	}
	if PrivateHost(u.Hostname()) {
		return "private address"
	}
	if req, err := http.NewRequest(http.MethodGet, v.URL, nil); err == nil { //nolint:noctx // never sent; built for the proxy lookup
		if p, err := proxyFor(req); err == nil && p != nil {
			return "through a proxy"
		}
	}
	return ""
}

// proxyFor is the proxy the environment names for a request, the same
// function the transport asks; tests replace it.
var proxyFor = http.ProxyFromEnvironment

// placeholders lists the `${...}` references in s, each spelled as
// `${NAME}`. Every `${` counts unless the `$` is doubled — the escape Expand
// honours — whether or not the name is well formed: what Expand would not
// recognise it would leave as written, but the rule is about what a reader
// sees, and a `${` that stays in a URL is not worth a special case.
func placeholders(s string) []string {
	var out []string
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '$' || s[i+1] != '{' {
			continue
		}
		if i > 0 && s[i-1] == '$' {
			i++ // $${: an escape, and the { is not the start of another
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		name := s[i+2:]
		if end >= 0 {
			name = s[i+2 : i+end]
		}
		if cut := strings.IndexByte(name, ':'); cut >= 0 {
			name = name[:cut] // ${VAR:-default}, ${VAR:?why}
		}
		out = append(out, "${"+Safe(name)+"}")
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// PrivateHost reports whether a host, as written in a URL, names the user's
// own machine or network rather than a public service: localhost and the
// suffixes that never resolve publicly, a name without a dot (a search
// domain fills it in), and an IP literal PrivateIP refuses. A public name
// can still resolve to a private address; that is checked when it is dialled.
func PrivateHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if h == "" {
		return true
	}
	if zone := strings.IndexByte(h, '%'); zone >= 0 {
		h = h[:zone]
	}
	if ip := net.ParseIP(h); ip != nil {
		return PrivateIP(ip)
	}
	if !strings.Contains(h, ".") {
		return true
	}
	for _, suffix := range []string{"localhost", "local", "internal", "lan", "home.arpa"} {
		if h == suffix || strings.HasSuffix(h, "."+suffix) {
			return true
		}
	}
	return false
}

// PrivateIP reports whether an address is not a public unicast one:
// loopback, private (10/8, 172.16/12, 192.168/16, fc00::/7), link-local
// (169.254/16, fe80::/10), the deprecated site-local fec0::/10,
// carrier-grade NAT (100.64/10), the IETF protocol assignments block
// (192.0.0.0/24), the benchmarking range (198.18.0.0/15), the reserved
// 240.0.0.0/4, unspecified, multicast, broadcast, the local-use NAT64
// prefix 64:ff9b:1::/48, and the IPv6 forms that embed an IPv4 address —
// IPv4-mapped, the deprecated IPv4-compatible and NAT64 — judged by the
// address they embed.
func PrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	} else if len(ip) == net.IPv6len {
		if embedded := embeddedIPv4(ip); embedded != nil {
			ip = embedded
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if len(ip) == net.IPv4len {
		switch {
		case ip[0] == 0: // 0.0.0.0/8, "this network"
			return true
		case ip[0] == 100 && ip[1]&0xc0 == 64: // 100.64.0.0/10
			return true
		case ip[0] == 192 && ip[1] == 0 && ip[2] == 0: // 192.0.0.0/24, IETF protocol assignments
			return true
		case ip[0] == 198 && ip[1]&0xfe == 18: // 198.18.0.0/15, benchmarking
			return true
		case ip[0]&0xf0 == 240: // 240.0.0.0/4, reserved; 255.255.255.255 included
			return true
		}
		return false
	}
	switch {
	case ip[0] == 0xfe && ip[1]&0xc0 == 0xc0: // fec0::/10, site-local (deprecated, still routed locally)
		return true
	case ip[0] == 0 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b && ip[4] == 0 && ip[5] == 1: // 64:ff9b:1::/48, local-use NAT64
		return true
	}
	return false
}

// embeddedIPv4 is the IPv4 address an IPv6 one carries in its low 32 bits:
// ::a.b.c.d (IPv4-compatible, deprecated) and 64:ff9b::a.b.c.d (NAT64). A
// resolver or a gateway turns either into the IPv4 it embeds, so that is
// what must be judged. Nil when the address embeds nothing.
func embeddedIPv4(ip net.IP) net.IP {
	compatible := true
	for _, b := range ip[:12] {
		if b != 0 {
			compatible = false
			break
		}
	}
	nat64 := ip[0] == 0 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b
	if nat64 {
		for _, b := range ip[4:12] {
			if b != 0 {
				nat64 = false
				break
			}
		}
	}
	if compatible || nat64 {
		return net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
	}
	return nil
}

// DescribeRemote renders a remote spec for the catalog trust screen: its
// URL, then the names of its headers with the values masked — a literal in
// the catalog is the author's and not what the user is asked to consent to
// sending — except a value that carries a `${VAR}` placeholder, which is
// shown as written because it is the user's own that would leave. Nothing
// is raw catalog text: it goes through Safe like everything printed.
func DescribeRemote(sp map[string]any) string {
	v := spec.ViewOf(sp)
	out := Safe(v.URL)
	if len(v.Headers) == 0 {
		return out
	}
	var hs []string
	for _, k := range spec.SortedKeys(v.Headers) {
		val := "****"
		if len(placeholders(v.Headers[k])) > 0 {
			val = quoteArg(v.Headers[k])
		}
		hs = append(hs, Safe(k)+": "+val)
	}
	return out + "  headers: " + strings.Join(hs, ", ")
}
