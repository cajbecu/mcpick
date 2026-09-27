package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/state"
)

// A check is consent for one server: to hand it to the agent, and — for a
// remote server the repository's catalog defines — to measure it, which
// sends the user's environment, expanded into URL and headers, to the
// author's host. The saved selection used to record the name alone, and a
// name is not a server: a repository could define a server with the name of
// a user server that was checked and inherit the check, and a checked
// repository server could change its URL or headers and keep it. A check is
// therefore recorded with the server's origin and a fingerprint of its spec
// as written (state.Check), and a project server counts as checked only
// while both are what they were. The user's own servers — local, user,
// plugin — are theirs to edit and keep their check; only the origin is kept
// for them, so that a check given to one of them never reaches a project
// server of the same name.

// SpecFingerprint identifies a spec as the catalog wrote it: the full
// SHA-256 of its canonical JSON, `${VAR}` and `{UUID}` unexpanded, so a
// rotated secret behind a placeholder changes nothing and no value is ever
// stored. Empty for a spec that cannot be encoded.
func SpecFingerprint(sp map[string]any) string {
	data, err := json.Marshal(sp) // map keys sorted: canonical
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Checks records what is being checked, for the selection about to be
// saved: one Check per selected server in the catalog.
func Checks(servers []catalog.Server, sel map[string]bool) map[string]state.Check {
	out := map[string]state.Check{}
	for _, s := range servers {
		if sel[s.Name] {
			out[s.Name] = state.Check{Origin: s.Origin, Spec: SpecFingerprint(s.Spec)}
		}
	}
	return out
}

// Unconfirmed is a saved check that no longer covers the server of that
// name: Why says what changed, worded to follow the name ("github now comes
// from this repository's catalog").
type Unconfirmed struct {
	Name string
	Why  string
}

// Confirm reconciles a saved selection with the catalog as it is now. Every
// saved name is selected, except a server the repository's catalog defines
// whose check does not cover it: none was recorded (a selection saved before
// checks were), it was recorded for a server of another origin (a user
// server the repository has since shadowed), or the spec has changed since.
// Those are returned unconfirmed, in catalog order, so the picker can say so
// on the row and the command line on stderr; checking one again is the new
// consent.
func Confirm(servers []catalog.Server, selected []string, checks map[string]state.Check) (sel map[string]bool, unconfirmed []Unconfirmed) {
	sel = map[string]bool{}
	for _, n := range selected {
		sel[n] = true
	}
	for _, s := range servers {
		if !sel[s.Name] || s.Origin != catalog.OriginProject {
			continue
		}
		c, ok := checks[s.Name]
		var why string
		switch {
		case !ok:
			why = "was checked before mcpick recorded what it checked"
		case c.Origin != catalog.OriginProject:
			why = "now comes from this repository's catalog"
		case c.Spec != SpecFingerprint(s.Spec):
			why = "changed in this repository's catalog since it was checked"
		default:
			continue
		}
		delete(sel, s.Name)
		unconfirmed = append(unconfirmed, Unconfirmed{Name: s.Name, Why: why})
	}
	return sel, unconfirmed
}
