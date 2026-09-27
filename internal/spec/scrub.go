package spec

import (
	"net/url"
	"os"
	"sort"
	"strings"
)

// A connection is made with the spec expanded: `${TOKEN}` in a header is
// the token itself by the time it is sent, and a server, a resolver or the
// HTTP stack may echo it back in an error — `HTTP 401: invalid credential
// Bearer <token>`, `lookup nonexistent-<token>.invalid: no such host`, a
// stdio server's stderr. Error text goes to the terminal, --json, the
// picker's status line, serve's JSON-RPC errors and measurements.json, so
// before it goes anywhere the values the expansion put in are put back as
// the references they came from (Scrubber).

// minScrub is the shortest value scrubbed: a shorter one (a port, `1`,
// `on`) would rewrite ordinary words of the message and hide nothing.
const minScrub = 6

// Scrubber replaces the expanded values of one spec in text.
type Scrubber struct {
	pairs [][2]string // value, what stands in for it; longest value first
}

// NewScrubber records what expanding raw — the spec as the catalog wrote
// it — put into it: each `${NAME}` reference's value from the environment
// stands for `${NAME}`. Headers that final — the spec as sent — has and
// raw does not (a token mcpick attached, see oauth.Attach) stand for ***.
// Either spec may be nil.
func NewScrubber(raw, final map[string]any) *Scrubber {
	s := &Scrubber{}
	walkStrings(raw, func(t string) {
		for _, g := range envRef.FindAllStringSubmatch(t, -1) {
			if g[1] != "" {
				continue // $${VAR} is literal text
			}
			if val, ok := os.LookupEnv(g[2]); ok {
				s.add(val, "${"+g[2]+"}")
			}
		}
	})
	written := ViewOf(raw).Headers
	for k, v := range ViewOf(final).Headers {
		if _, ok := written[k]; ok {
			continue
		}
		if g := credScheme.FindStringSubmatch(v); g != nil {
			v = strings.TrimSpace(g[2])
		}
		s.add(v, "***")
	}
	sort.SliceStable(s.pairs, func(i, j int) bool { return len(s.pairs[i][0]) > len(s.pairs[j][0]) })
	return s
}

// add records value, and the forms a URL or a resolver may give it, as
// stood for by ref.
func (s *Scrubber) add(value, ref string) {
	if len(value) < minScrub {
		return
	}
	seen := map[string]bool{}
	for _, v := range []string{value, strings.ToLower(value), url.QueryEscape(value), url.PathEscape(value)} {
		if !seen[v] {
			seen[v] = true
			s.pairs = append(s.pairs, [2]string{v, ref})
		}
	}
}

// Scrub returns text with every recorded value replaced. A nil Scrubber
// returns text as it is.
func (s *Scrubber) Scrub(text string) string {
	if s == nil {
		return text
	}
	for _, p := range s.pairs {
		text = strings.ReplaceAll(text, p[0], p[1])
	}
	return text
}

// walkStrings calls fn on every string in v.
func walkStrings(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		fn(t)
	case map[string]any:
		for _, e := range t {
			walkStrings(e, fn)
		}
	case []any:
		for _, e := range t {
			walkStrings(e, fn)
		}
	}
}
