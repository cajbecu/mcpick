package trust

import (
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
)

// The fingerprint is over the spec as written: stable across map order,
// different for any change, and blind to what a placeholder expands to.
func TestSpecFingerprint(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "one")
	a := remote("https://api.example.com/mcp", map[string]any{"Authorization": "Bearer ${GITHUB_TOKEN}"})
	b := map[string]any{"headers": map[string]any{"Authorization": "Bearer ${GITHUB_TOKEN}"}, "url": "https://api.example.com/mcp", "type": "http"}
	if SpecFingerprint(a) != SpecFingerprint(b) || len(SpecFingerprint(a)) != 64 {
		t.Errorf("the same spec fingerprints differently: %s vs %s", SpecFingerprint(a), SpecFingerprint(b))
	}
	t.Setenv("GITHUB_TOKEN", "two")
	if SpecFingerprint(a) != SpecFingerprint(b) {
		t.Error("a rotated secret behind the placeholder changed the fingerprint")
	}
	for _, changed := range []map[string]any{
		remote("https://evil.example.com/mcp", map[string]any{"Authorization": "Bearer ${GITHUB_TOKEN}"}),
		remote("https://api.example.com/mcp", map[string]any{"Authorization": "Bearer ${OTHER}"}),
		remote("https://api.example.com/mcp", nil),
	} {
		if SpecFingerprint(changed) == SpecFingerprint(a) {
			t.Errorf("%v fingerprints like the original", changed)
		}
	}
}

// A saved check covers a repository server only while it is the server
// that was checked. A user server the repository has since shadowed, a
// repository server whose spec changed, and a selection saved before
// checks were recorded are all unconfirmed; the user's own servers, and a
// repository server that is as it was, keep their check.
func TestConfirmBindsTheCheckToOriginAndSpec(t *testing.T) {
	gh := remote("https://api.githubcopilot.com/mcp/", map[string]any{"Authorization": "Bearer ${GITHUB_TOKEN}"})
	user := []catalog.Server{
		{Name: "github", Origin: catalog.OriginUser, Spec: gh},
		{Name: "sentry", Origin: catalog.OriginUser, Spec: remote("https://mcp.sentry.dev/mcp", nil)},
	}
	checks := Checks(user, map[string]bool{"github": true, "sentry": true})
	if checks["github"].Origin != catalog.OriginUser || checks["github"].Spec != SpecFingerprint(gh) {
		t.Fatalf("Checks = %+v", checks)
	}

	// The same catalog again: everything stands.
	sel, un := Confirm(user, []string{"github", "sentry"}, checks)
	if !sel["github"] || !sel["sentry"] || len(un) != 0 {
		t.Errorf("unchanged catalog: sel=%v unconfirmed=%v", sel, un)
	}

	// The repository defines github, with the very same spec: the user's
	// check was for the user's server, not for the repository's.
	shadowed := []catalog.Server{{Name: "github", Origin: catalog.OriginProject, Spec: gh}, user[1]}
	sel, un = Confirm(shadowed, []string{"github", "sentry"}, checks)
	if sel["github"] || !sel["sentry"] {
		t.Errorf("shadowed: sel=%v", sel)
	}
	if len(un) != 1 || un[0].Name != "github" || un[0].Why != "now comes from this repository's catalog" {
		t.Errorf("shadowed: unconfirmed=%+v", un)
	}

	// A repository server that was checked keeps the check while it is as it
	// was, and loses it when its URL or headers change.
	repo := []catalog.Server{{Name: "github", Origin: catalog.OriginProject, Spec: gh}}
	checks = Checks(repo, map[string]bool{"github": true})
	if sel, un = Confirm(repo, []string{"github"}, checks); !sel["github"] || len(un) != 0 {
		t.Errorf("repository server as it was: sel=%v unconfirmed=%v", sel, un)
	}
	moved := []catalog.Server{{Name: "github", Origin: catalog.OriginProject,
		Spec: remote("https://evil.example.com/mcp", map[string]any{"Authorization": "Bearer ${GITHUB_TOKEN}"})}}
	sel, un = Confirm(moved, []string{"github"}, checks)
	if sel["github"] || len(un) != 1 || un[0].Why != "changed in this repository's catalog since it was checked" {
		t.Errorf("changed spec: sel=%v unconfirmed=%+v", sel, un)
	}

	// A selection from before checks were recorded: its repository servers
	// are unconfirmed, the user's own stand.
	sel, un = Confirm(shadowed, []string{"github", "sentry", "gone"}, nil)
	if sel["github"] || !sel["sentry"] || !sel["gone"] {
		t.Errorf("legacy selection: sel=%v", sel)
	}
	if len(un) != 1 || un[0].Why != "was checked before mcpick recorded what it checked" {
		t.Errorf("legacy selection: unconfirmed=%+v", un)
	}

	// The user's own server changing is the user's business.
	edited := []catalog.Server{{Name: "sentry", Origin: catalog.OriginUser, Spec: remote("https://mcp.sentry.dev/v2/mcp", nil)}}
	if sel, un = Confirm(edited, []string{"sentry"}, Checks(user, map[string]bool{"sentry": true})); !sel["sentry"] || len(un) != 0 {
		t.Errorf("edited user server: sel=%v unconfirmed=%v", sel, un)
	}
}
