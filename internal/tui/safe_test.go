package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/trust"
)

// hostile is what a catalog or a server can put in text that reaches the
// terminal: OSC 52 (writes the clipboard), ESC[2J (clears the screen), the
// same CSI as one C1 byte in UTF-8, and a right-to-left override. The frame
// carries the picker's own styling escapes, so the check is for these.
const hostile = "\x1b]52;c;aGk=\x07\x1b[2J\u009b2J\u202e"

func hasHostile(s string) bool {
	return strings.Contains(s, "\x1b]52") || strings.Contains(s, "\x1b[2J") ||
		strings.ContainsAny(s, "\u009b\u202e\x07")
}

// The status line and the catalog warnings are drawn from text the catalog
// or a server wrote — a name, an error body — and spell every escape out.
func TestStatusLineAndWarningsNeutraliseTerminalEscapes(t *testing.T) {
	p := newPicker(t)
	p.root = t.TempDir()
	p.cat.Warnings = append(p.cat.Warnings, "profile review lists mis"+hostile+"sing, not in the catalog")
	name := "gh" + hostile + "ost"
	p.cat.Servers[0].Name = name
	p.cursor = 0

	frames := map[string]string{}
	p.Update(measuredMsg{name: name, spec: p.cat.Servers[0].Spec, res: mcp.Result{Name: name, Err: "HTTP 500: boom " + hostile}})
	frames["measurement error"] = p.View().Content
	p.hideKey() // the row folds into Hidden; the name is now on the status line alone
	frames["hid"] = p.View().Content

	for which, frame := range frames {
		if hasHostile(frame) {
			t.Errorf("%s: a raw terminal escape reached the frame:\n%q", which, frame)
		}
		screen := plain(frame)
		for _, want := range []string{`mis\x1b]52;c;aGk=\a\x1b[2J\u009b2J\u202esing`, `gh\x1b]52`} {
			if !strings.Contains(screen, want) {
				t.Errorf("%s: %q should be spelled out:\n%s", which, want, screen)
			}
		}
	}
	if screen := plain(frames["measurement error"]); !strings.Contains(screen, `HTTP 500: boom \x1b]52`) {
		t.Errorf("the error should be spelled out on the status line:\n%s", screen)
	}
	if screen := plain(frames["hid"]); !strings.Contains(screen, `hid gh\x1b]52`) {
		t.Errorf("the hide status should be spelled out:\n%s", screen)
	}
}

// The status line's two newer sources of text from outside — the servers a
// saved check no longer covers, named at start, and the note a measurement
// carries about a refreshed OAuth token — go through the same sanitiser.
func TestUnconfirmedAndOAuthNoteNeutraliseTerminalEscapes(t *testing.T) {
	name := "gh" + hostile + "ost"
	cat := sampleCatalog()
	cat.Servers[0].Name = name
	p := build(cat, map[string]bool{name: true}, Options{
		UID: "deploy-1", Target: mustTarget(t, "claude"), Root: "/src/app",
		Profiles:    profile.New(filepath.Join(t.TempDir(), "profiles.yaml")),
		Unconfirmed: []trust.Unconfirmed{{Name: name, Why: "now comes from this repository's catalog"}},
	})
	p.tr = newTrustState(trust.Open(filepath.Join(t.TempDir(), "trust.json")), "/src/app")
	p.width, p.height = 160, 40
	p.hold = false
	p.cursor = 0

	frames := map[string]string{"unconfirmed status": p.View().Content}
	p.status = "" // the row's own line
	frames["unconfirmed detail"] = p.View().Content
	p.Update(measuredMsg{name: name, spec: cat.Servers[0].Spec, res: mcp.Result{Name: name, OK: true},
		note: "could not save the refreshed OAuth token for " + name + ": " + hostile})
	frames["oauth note"] = p.View().Content

	for which, frame := range frames {
		if hasHostile(frame) {
			t.Errorf("%s: a raw terminal escape reached the frame:\n%q", which, frame)
		}
		if screen := plain(frame); !strings.Contains(screen, `gh\x1b]52`) {
			t.Errorf("%s: the name should be spelled out:\n%s", which, screen)
		}
	}
	if screen := plain(frames["unconfirmed status"]); !strings.Contains(screen, "check again") {
		t.Errorf("the unconfirmed note is missing from the status line:\n%s", screen)
	}
	if screen := plain(frames["oauth note"]); !strings.Contains(screen, `refreshed OAuth token for gh\x1b]52`) {
		t.Errorf("the note should be spelled out on the status line:\n%s", screen)
	}
}
