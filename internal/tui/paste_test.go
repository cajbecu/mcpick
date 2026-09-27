package tui

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cajbecu/mcpick/internal/profile"
)

// keyMsg is the key event the terminal sends for k: a character, "space",
// "enter" or "esc".
func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

// fakeClock gives the picker a clock the test moves by hand.
func fakeClock(p *picker) *time.Time {
	at := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return at }
	return &at
}

// typeText sends every rune of text as its own key press, step apart, the
// way a terminal without bracketed paste delivers a paste (or a hand types).
func typeText(p *picker, at *time.Time, step time.Duration, text string) {
	for _, r := range text {
		*at = at.Add(step)
		k := string(r)
		if r == ' ' {
			k = "space"
		}
		p.Update(keyMsg(k))
	}
}

func noProfilesFile(t *testing.T, p *picker) {
	t.Helper()
	if _, err := os.Stat(p.profiles.Path()); err == nil {
		data, _ := os.ReadFile(p.profiles.Path())
		t.Errorf("a profiles file was written:\n%s", data)
	}
}

const prompt = "Investigate the failing checkout test in https://example.com/issues/4321 (\"Payments: 9 orders stuck after the fraud check\")"

// A bracketed paste where keys are commands is text nobody meant for the
// picker: nothing is toggled, no prompt opens, no profile is saved, and the
// status says why nothing happened.
func TestBracketedPasteIsIgnoredWhereKeysAreCommands(t *testing.T) {
	p := newPicker(t)
	before := len(p.sel)
	p.Update(tea.PasteMsg{Content: prompt})
	if p.mode != modeList || len(p.sel) != before {
		t.Errorf("mode %v, %d selected (was %d): the paste was read as keys", p.mode, len(p.sel), before)
	}
	if p.status != pastedStatus {
		t.Errorf("status = %q", p.status)
	}
	noProfilesFile(t, p)

	p.press("p") // the profile screen: + c r d are commands there too
	p.status = ""
	p.Update(tea.PasteMsg{Content: prompt})
	if p.mode != modeProfiles || p.pm.sub != pmNone || p.status != pastedStatus {
		t.Errorf("profiles: sub %v status %q", p.pm.sub, p.status)
	}
	noProfilesFile(t, p)

	p.press("esc")
	p.cursor = 0 // a workspace server: d asks [y/N]
	p.press("d")
	p.Update(tea.PasteMsg{Content: "yes"})
	if p.mode != modeConfirmYAML || len(p.cat.Servers) != 7 {
		t.Errorf("a paste answered the delete question: mode %v, %d servers", p.mode, len(p.cat.Servers))
	}
}

// Where the picker is taking text, a paste is text: its first line, without
// control characters, goes into the input.
func TestBracketedPasteGoesIntoTextInputs(t *testing.T) {
	p := newPicker(t)
	p.press("/")
	p.Update(tea.PasteMsg{Content: "lin\tear\r\nsecond line"})
	if p.filter != "linear" {
		t.Errorf("filter = %q", p.filter)
	}
	if vis := p.visible(); len(vis) != 1 || p.cat.Servers[vis[0]].Name != "linear" {
		t.Errorf("the list did not follow the pasted filter: %v", vis)
	}
	if p.cat.Servers[p.cursor].Name != "linear" {
		t.Error("the cursor should follow onto the match")
	}
	p.press("esc")

	p.press("+")
	p.Update(tea.PasteMsg{Content: "my server\n"})
	if p.mode != modeAddName || p.input != "my server" {
		t.Errorf("add name: mode %v input %q", p.mode, p.input)
	}
	p.press("esc")

	p.press("s")
	p.Update(tea.PasteMsg{Content: "rev\x00iew\n"})
	if p.pm.sub != pmAdd || p.pm.input != "review" {
		t.Errorf("profile name: sub %v input %q", p.pm.sub, p.pm.input)
	}
	p.press("enter")
	if p.pm.sub != pmNone || !strings.Contains(fileText(t, p), "name: review") {
		t.Errorf("status = %q", p.status)
	}
	p.press("esc")

	p.cursor = 5 // a global server: d asks for the word yes
	p.press("d")
	p.Update(tea.PasteMsg{Content: "yes\n"})
	if p.mode != modeConfirmJSON || p.input != "yes" {
		t.Errorf("json confirm: mode %v input %q", p.mode, p.input)
	}
}

// Keys that arrive right after start were typed before the picker had drawn
// — at the shell, or at the agent the user believed was running — and are
// not for it. Once the grace period is over, keys work.
func TestKeysInTheStartupGraceAreDropped(t *testing.T) {
	p := newPicker(t)
	at := fakeClock(p)
	p.started = *at
	before := len(p.sel)
	*at = at.Add(startupGrace / 2)
	p.Update(keyMsg("n"))
	if len(p.sel) != before {
		t.Error("n inside the grace period unchecked everything")
	}
	if p.status != earlyStatus {
		t.Errorf("status = %q", p.status)
	}
	*at = at.Add(startupGrace)
	p.Update(keyMsg("n"))
	if len(p.sel) != 0 {
		t.Error("n after the grace period should uncheck everything")
	}

	// A picker made without a start time (as tests make them) has no grace.
	q := newPicker(t)
	q.Update(keyMsg("n"))
	if len(q.sel) != 0 {
		t.Error("with no start time every key should count")
	}
}

// A paste without bracketed paste arrives as keys, hundreds a second. The
// reported case: the letters of a prompt, in the list. n has unchecked
// everything and v opened the move chooser (which the letters after it do
// not answer: its keys are p, l and u) before the burst is long enough to
// be sure; from there on the keys are dropped — the s that would have
// opened the save prompt among them — and nothing is saved.
func TestBurstOfKeysIsPastedText(t *testing.T) {
	p := newPicker(t)
	at := fakeClock(p)
	p.started = *at
	*at = at.Add(time.Second)
	typeText(p, at, time.Millisecond, prompt)
	if p.mode != modeMoveTo || p.pm.sub != pmNone || p.pm.input != "" {
		t.Errorf("mode %v sub %v input %q: the paste ran on as keys", p.mode, p.pm.sub, p.pm.input)
	}
	if p.status != pastedStatus {
		t.Errorf("status = %q", p.status)
	}
	noProfilesFile(t, p)
	p.Update(keyMsg("enter")) // still inside the burst: not a launch
	if p.launch {
		t.Error("an enter in the burst launched")
	}

	// Once the burst is over, keys are keys again.
	*at = at.Add(time.Second)
	p.Update(keyMsg("esc"))
	p.Update(keyMsg("a"))
	if p.mode != modeList || len(p.sel) == 0 {
		t.Errorf("after the burst: mode %v, %d selected", p.mode, len(p.sel))
	}
}

// Typing, even fast, is not a burst: twelve j's at a typist's pace all move.
func TestTypedKeysAreNotABurst(t *testing.T) {
	p := newPicker(t)
	at := fakeClock(p)
	p.cursor = 0
	typeText(p, at, 60*time.Millisecond, "jjjjjjjjjjjj")
	if p.cursor != len(p.cat.Servers)-1 {
		t.Errorf("cursor = %d; typed keys were dropped", p.cursor)
	}
	if p.status == pastedStatus {
		t.Error("typing was called a paste")
	}
}

// A fast paste into a prompt the user opened is what it looks like: text for
// that prompt.
func TestBurstIntoAnOpenPromptIsText(t *testing.T) {
	p := newPicker(t)
	at := fakeClock(p)
	p.Update(keyMsg("/"))
	*at = at.Add(time.Second)
	typeText(p, at, time.Millisecond, "linear")
	if p.filter != "linear" {
		t.Errorf("filter = %q", p.filter)
	}
	p.press("esc", "s")
	*at = at.Add(time.Second)
	typeText(p, at, time.Millisecond, "review-profile")
	if p.pm.sub != pmAdd || p.pm.input != "review-profile" {
		t.Errorf("sub %v input %q", p.pm.sub, p.pm.input)
	}
}

// A name that breaks the rule is refused at the prompt, which stays open with
// the reason, and the file is untouched.
func TestProfilePromptRefusesBadNames(t *testing.T) {
	p := newPicker(t)
	p.press("s")
	for _, r := range "tigate Jira ticket https://x (ER" {
		k := string(r)
		if r == ' ' {
			k = "space"
		}
		p.press(k)
	}
	p.press("enter")
	if p.pm.sub != pmAdd {
		t.Fatalf("the prompt should stay open; status = %q", p.status)
	}
	if !strings.Contains(plain(p.status), "1-40 characters") {
		t.Errorf("status = %q", plain(p.status))
	}
	noProfilesFile(t, p)
}

// A profile named before the rule is still listed, escaped as before, and can
// be renamed; its servers are not rewritten under the old name.
func TestLegacyProfileNameIsShownRenamedNotRewritten(t *testing.T) {
	p := newPicker(t)
	if err := os.WriteFile(p.profiles.Path(), []byte("profiles:\n  - {name: 'old name', servers: [github]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.profiles.Update(func(*profile.Set) error { return nil }); err != nil {
		t.Fatal(err)
	}
	p.press("p")
	if !strings.Contains(plain(p.View().Content), "old name") {
		t.Error("the legacy profile should be listed")
	}
	p.press("tab", "space")
	if !strings.Contains(plain(p.status), "rename it first") || !strings.Contains(fileText(t, p), "servers: [github]") {
		t.Errorf("status = %q file:\n%s", plain(p.status), fileText(t, p))
	}
	p.press("tab", "r")
	for range len("old name") {
		p.press("backspace")
	}
	p.press("n", "e", "w", "enter")
	if p.pm.sub != pmNone || !strings.Contains(fileText(t, p), "name: new") || strings.Contains(fileText(t, p), "old name") {
		t.Errorf("status = %q file:\n%s", plain(p.status), fileText(t, p))
	}
}

// With keys held, as a real picker holds them, the first keys of a burst do
// nothing either: the reported paste leaves the selection, the hidden set and
// the catalog exactly as they were.
func TestHeldKeysOfABurstNeverAct(t *testing.T) {
	p := newPicker(t)
	p.hold = true
	at := fakeClock(p)
	p.started = *at
	*at = at.Add(time.Second)
	before := len(p.sel)
	typeText(p, at, time.Millisecond, "nhhdy"+prompt)
	p.Update(releaseMsg{gen: p.heldGen})
	if len(p.sel) != before || p.mode != modeList || len(p.hidden) != 0 {
		t.Errorf("after the burst: %d selected (want %d), mode %v, hidden %v", len(p.sel), before, p.mode, p.hidden)
	}
	noProfilesFile(t, p)
}

// A held key acts once it has waited with no burst behind it, and keys keep
// their order: n then a checks everything, a then n nothing. Each key's
// deadline is its own: the first key's tick releases it and not the one
// that arrived after it.
func TestHeldKeysActInOrderAfterTheWait(t *testing.T) {
	p := newPicker(t)
	p.hold = true
	at := fakeClock(p)
	*at = at.Add(time.Second)
	p.Update(keyMsg("n"))
	if len(p.sel) == 0 {
		t.Fatal("a held key acted before its wait")
	}
	*at = at.Add(100 * time.Millisecond)
	p.Update(keyMsg("a"))
	p.Update(releaseMsg{gen: p.heldGen - 1}) // n's own deadline: n acts, a keeps waiting
	if len(p.held) != 1 || p.held[0].key != "a" || len(p.sel) != 0 {
		t.Fatalf("after n's deadline: held %v, %d selected", p.held, len(p.sel))
	}
	p.Update(releaseMsg{gen: p.heldGen})
	if len(p.held) != 0 || len(p.sel) == 0 {
		t.Errorf("after release: held %v, %d selected", p.held, len(p.sel))
	}
	p.Update(keyMsg("n"))
	p.Update(releaseMsg{gen: p.heldGen})
	if len(p.sel) != 0 {
		t.Errorf("n after release: %d selected", len(p.sel))
	}
}

// A key held down repeats at about 30 Hz, faster than burstWindow, so a
// wait restarted by every repeat froze the cursor until the key was let
// go. Each repeat has its own deadline: the cursor moves within two ticks
// of the first repeat, and keeps moving, while a paste is still a burst.
func TestHeldDownKeyMovesTheCursorAsItRepeats(t *testing.T) {
	p := newPicker(t)
	p.hold = true
	p.cursor = 0
	at := fakeClock(p)
	*at = at.Add(time.Second)
	const period = 33 * time.Millisecond // 30 Hz
	type event struct {
		at  time.Time
		gen int // a tick for this held key, or 0 for a key press
	}
	var events []event
	start := *at
	for i := 1; i <= 6; i++ {
		pressAt := start.Add(time.Duration(i-1) * period)
		events = append(events, event{at: pressAt}, event{at: pressAt.Add(burstWindow), gen: i})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
	// Every tick that fires moves the cursor by one: the key it is for,
	// and no other, is released by it.
	fired := 0
	for _, e := range events {
		*at = e.at
		if e.gen == 0 {
			p.Update(keyMsg("j"))
		} else {
			p.Update(releaseMsg{gen: e.gen})
			fired++
		}
		if p.cursor != fired {
			t.Fatalf("at +%s (tick %d): cursor = %d, want %d; held %v", e.at.Sub(start), e.gen, p.cursor, fired, p.held)
		}
	}
	if p.status == pastedStatus {
		t.Error("key repeat was called a paste")
	}
}

// Adding a server: the URL is usually pasted. The terminal sends it as a
// bracketed paste, which the picker once dropped, so the prompt stayed empty.
// Typed non-ASCII characters are accepted and backspace takes one whole
// character off, not one byte.
func TestAddServerTakesAPastedURL(t *testing.T) {
	p := newPicker(t)
	p.Update(keyMsg("+"))
	for _, k := range []string{"n", "e", "w"} {
		p.Update(keyMsg(k))
	}
	p.Update(keyMsg("enter")) // name
	p.Update(keyMsg("enter")) // type: http
	if p.mode != modeAddTarget {
		t.Fatalf("mode = %v, want the target prompt", p.mode)
	}
	url := "https://mcp.example.com/some/path?x=1"
	p.Update(tea.PasteMsg{Content: url})
	if p.input != url {
		t.Fatalf("input = %q, want the pasted URL", p.input)
	}
	p.Update(keyMsg("ș"))
	if p.input != url+"ș" {
		t.Errorf("typed ș: input = %q", p.input)
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.input != url {
		t.Errorf("after backspace: input = %q, want %q", p.input, url)
	}
}
