package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/cajbecu/mcpick/internal/state"
)

// hidePicker is a picker with a throwaway home and workspace, so hiding can
// save without touching the real ~/.mcpick.
func hidePicker(t *testing.T) *picker {
	t.Helper()
	t.Setenv("MCPICK_HOME", t.TempDir())
	p := newPicker(t)
	p.root = t.TempDir()
	p.hidden = map[string]bool{}
	return p
}

// rowOf is the list row for name: a line with a box on it, not the status
// line that may mention the name too.
func rowOf(frame, name string) string {
	for _, line := range strings.Split(plain(frame), "\n") {
		if strings.Contains(line, "] "+name+" ") {
			return line
		}
	}
	return ""
}

func inVisible(p *picker, name string) bool {
	for _, i := range p.visible() {
		if p.cat.Servers[i].Name == name {
			return true
		}
	}
	return false
}

// A hidden server leaves the origin groups for a folded Hidden section, and
// 'a' leaves it unchecked: "all" is the one key that would otherwise bring
// every never-used server back at once.
func TestHiddenServerLeavesTheListAndIsSkippedByAll(t *testing.T) {
	p := hidePicker(t)
	p.sel = map[string]bool{}
	p.cursor = 5 // sentry
	p.updateList("h")
	if inVisible(p, "sentry") {
		t.Fatal("a hidden server is still among the rows")
	}
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "Hidden (1)") {
		t.Errorf("no Hidden (1) section in:\n%s", frame)
	}
	if rowOf(frame, "sentry") != "" {
		t.Errorf("the hidden row is still drawn while the section is folded:\n%s", frame)
	}
	p.updateList("a")
	if p.sel["sentry"] {
		t.Error("'a' checked a hidden server")
	}
	if !p.sel["browser"] {
		t.Error("'a' skipped an active server")
	}
	if !strings.Contains(p.status, "hidden left unchecked") {
		t.Errorf("status = %q; the user should know why one stayed off", p.status)
	}
}

// H unfolds the section: hidden rows are then rows like any other, with where
// they came from beside them, and h on one brings it back.
func TestHiddenSectionUnfoldsAndUnhides(t *testing.T) {
	p := hidePicker(t)
	p.cursor = 5
	p.updateList("h")
	p.updateList("H")
	if !inVisible(p, "sentry") {
		t.Fatal("H did not unfold the Hidden section")
	}
	frame := plain(p.View().Content)
	row := rowOf(frame, "sentry")
	if row == "" || !strings.Contains(row, "user") {
		t.Errorf("an unfolded hidden row should name its origin: %q\n%s", row, frame)
	}
	if strings.Index(frame, "Hidden (1)") < strings.Index(frame, "browser") {
		t.Errorf("the Hidden section should come after the active groups:\n%s", frame)
	}
	p.cursor = 5
	p.updateList("h")
	if p.hidden["sentry"] || p.hiddenCount() != 0 {
		t.Error("h on a hidden row should unhide it")
	}
	p.updateList("shift+h") // the other spelling of H, and folding an empty section
	if p.showHidden {
		t.Error("folding should work with the shift+h spelling too")
	}
}

// Hiding is not unchecking: a checked server keeps loading, and the header
// says so, since a server that vanished from the list but still ran would
// be exactly the silent surprise the feature must not create.
func TestCheckedHiddenServerStaysCheckedAndHeaderSaysSo(t *testing.T) {
	p := hidePicker(t)
	p.cursor = 0 // github, checked
	p.updateList("h")
	if !p.sel["github"] {
		t.Fatal("hiding unchecked the server")
	}
	if !strings.Contains(p.status, "still checked") {
		t.Errorf("status = %q; hiding a checked server should say it still loads", p.status)
	}
	head := strings.SplitN(plain(p.View().Content), "\n", 2)[0]
	if !strings.Contains(head, "1 hidden checked") {
		t.Errorf("header = %q; a hidden server that loads must be announced", head)
	}
	p.sel = map[string]bool{}
	if strings.Contains(plain(p.View().Content), "hidden checked") {
		t.Error("nothing hidden is checked any more; the note should be gone")
	}
}

// 'n' means none: a hidden server that stayed checked would be a launch the
// user cannot see in the list.
func TestNoneUnchecksHiddenServers(t *testing.T) {
	p := hidePicker(t)
	p.cursor = 0 // github, checked
	p.updateList("h")
	p.updateList("n")
	if p.sel["github"] {
		t.Error("'n' left a folded hidden server checked")
	}
}

// What was hidden is remembered for the next launch in the same project, and
// only there.
func TestHiddenSetIsSavedPerWorkspace(t *testing.T) {
	p := hidePicker(t)
	p.cursor = 5
	p.updateList("h")
	set, err := state.LoadHidden(p.root)
	if err != nil || !set["sentry"] {
		t.Fatalf("hidden set after h = %v, %v", set, err)
	}
	other, _ := state.LoadHidden(t.TempDir())
	if len(other) != 0 {
		t.Errorf("another workspace sees the hidden set: %v", other)
	}
	p.cursor = 5
	p.updateList("H")
	p.updateList("h")
	set, _ = state.LoadHidden(p.root)
	if set["sentry"] {
		t.Error("unhiding was not saved")
	}
}

// The cursor must land on a row still in view after the one under it is
// hidden or folded away — the next one down, so hiding several in a row
// reads on instead of jumping to the top.
func TestCursorStaysInViewAfterHiding(t *testing.T) {
	p := hidePicker(t)
	p.cursor = 3 // linear
	p.updateList("h")
	if p.cat.Servers[p.cursor].Name != "playwright-mcp" || !inVisible(p, p.cat.Servers[p.cursor].Name) {
		t.Errorf("cursor on %s after hiding linear, want playwright-mcp", p.cat.Servers[p.cursor].Name)
	}
	p.updateList("G")
	p.updateList("h") // the last row: nothing below, so the new last one
	if !inVisible(p, p.cat.Servers[p.cursor].Name) {
		t.Errorf("cursor on hidden %s", p.cat.Servers[p.cursor].Name)
	}
	p.updateList("H")
	p.updateList("G") // onto the last hidden row
	p.updateList("H") // fold with the cursor inside the section
	if !inVisible(p, p.cat.Servers[p.cursor].Name) {
		t.Errorf("cursor on folded %s", p.cat.Servers[p.cursor].Name)
	}
}

// The filter narrows both sections, and the section count follows it.
func TestFilterAppliesToHiddenSection(t *testing.T) {
	p := hidePicker(t)
	p.hidden = map[string]bool{"sentry": true, "browser": true}
	p.showHidden = true
	p.filter = "sent"
	if vis := p.visible(); len(vis) != 1 || p.cat.Servers[vis[0]].Name != "sentry" {
		t.Errorf("visible = %v, want sentry only", vis)
	}
	if p.hiddenCount() != 1 {
		t.Errorf("hiddenCount = %d under the filter, want 1", p.hiddenCount())
	}
}

// Hiding everything must say so, not pretend a filter matched nothing.
func TestAllHiddenIsExplained(t *testing.T) {
	p := hidePicker(t)
	for range p.cat.Servers {
		p.updateList("h")
	}
	if len(p.visible()) != 0 {
		t.Fatal("not everything got hidden")
	}
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "every server is hidden") || strings.Contains(frame, "no server matches") {
		t.Errorf("frame:\n%s", frame)
	}
}

// pressSpace is the picker's own path for space: through Update, as a key.
func pressSpace(p *picker) { p.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) }

// The picker opens with the cursor on catalog row 0. When that server was
// hidden in an earlier session, space and h must not act on it from under a
// folded section — the user would toggle, or hide, a row nobody drew.
func TestReopeningWithTheFirstServerHiddenMovesTheCursorOntoARow(t *testing.T) {
	p := hidePicker(t)
	if err := state.SaveHidden(p.root, map[string]bool{"github": true}); err != nil {
		t.Fatal(err)
	}
	p.cursor = 0
	p.loadHidden()
	if !p.hidden["github"] {
		t.Fatal("the saved set was not loaded")
	}
	if !inVisible(p, p.cat.Servers[p.cursor].Name) {
		t.Fatalf("cursor still on hidden %s after loading", p.cat.Servers[p.cursor].Name)
	}
	if s, ok := p.current(); !ok || s.Name != "local-tool" {
		t.Errorf("current = %v, %v; want the first row in view, local-tool", s.Name, ok)
	}
	pressSpace(p)
	if !p.sel["local-tool"] || !p.sel["github"] {
		t.Errorf("space toggled the wrong row: sel = %v", p.sel)
	}
}

// With every server hidden there is no row: current() must say so, so that
// space, h and d do nothing rather than act on the last cursor target.
func TestNoRowUnderTheCursorWhenEverythingIsHidden(t *testing.T) {
	p := hidePicker(t)
	for _, s := range p.cat.Servers {
		p.hidden[s.Name] = true
	}
	p.cursor = 2
	if s, ok := p.current(); ok {
		t.Fatalf("current = %s with nothing in view", s.Name)
	}
	before := len(p.sel)
	pressSpace(p)
	p.updateList("h")
	p.updateList("d")
	if len(p.sel) != before || !p.hidden["notion"] || p.mode != modeList {
		t.Errorf("a key acted on an invisible row: sel=%v hidden=%v mode=%v", p.sel, p.hidden, p.mode)
	}
	p.updateList("H") // unfolded, the rows are back and so is the cursor
	if _, ok := p.current(); !ok {
		t.Error("no current row with the Hidden section unfolded")
	}
}

// Checking a server disabled in Claude Code re-enables it there for good.
// One that is hidden as well must not be checked by a blind space on the
// cursor's start position.
func TestHiddenDisabledServerIsNotReenabledBlindly(t *testing.T) {
	p := hidePicker(t)
	p.cat.Servers[0].Disabled = true // github, disabled in Claude Code
	delete(p.sel, "github")
	if err := state.SaveHidden(p.root, map[string]bool{"github": true}); err != nil {
		t.Fatal(err)
	}
	p.cursor = 0
	p.loadHidden()
	pressSpace(p)
	if p.sel["github"] {
		t.Fatal("space checked a hidden server disabled in Claude Code: launching would re-enable it")
	}
}

// Hidden rows are drawn after the active ones, so the list is not in catalog
// order once the section is unfolded. Arrow keys must step through it as
// drawn: from one hidden row to the next, not back into the active rows.
func TestArrowsTraverseAHiddenSectionOfEarlyCatalogEntries(t *testing.T) {
	p := hidePicker(t)
	p.hidden = map[string]bool{"github": true, "local-tool": true} // catalog 0 and 1
	p.showHidden = true
	name := func() string { return p.cat.Servers[p.cursor].Name }
	p.cursor = 0
	p.updateList("down")
	if name() != "local-tool" {
		t.Errorf("down from the first hidden row went to %s, want local-tool", name())
	}
	p.updateList("down")
	if name() != "local-tool" {
		t.Errorf("down from the last row went to %s, want to stay", name())
	}
	p.updateList("up")
	p.updateList("up")
	if name() != "browser" {
		t.Errorf("up out of the Hidden section went to %s, want browser, the last active row", name())
	}
	p.updateList("G")
	p.updateList("pgup")
	if name() != "notion" {
		t.Errorf("a page up from the last hidden row went to %s, want the first active row", name())
	}
	p.updateList("pgdown")
	if name() != "local-tool" {
		t.Errorf("a page down went to %s, want the last hidden row", name())
	}
	p.cursor = 1
	p.updateList("up")
	if name() != "github" {
		t.Errorf("up inside the Hidden section went to %s, want github", name())
	}
}

// Two pickers on the same project each know the set as of their start. A
// hide in one must not be undone by a hide in the other saving its stale
// copy; each picker should see the other's change once it saves.
func TestTwoPickersDoNotLoseEachOthersHides(t *testing.T) {
	a := hidePicker(t)
	b := newPicker(t)
	b.root, b.hidden = a.root, map[string]bool{}
	a.cursor = 0 // github
	a.updateList("h")
	b.cursor = 1 // local-tool
	b.updateList("h")
	set, err := state.LoadHidden(a.root)
	if err != nil || !set["github"] || !set["local-tool"] {
		t.Fatalf("on disk = %v, %v; want both hides kept", set, err)
	}
	if !b.hidden["github"] {
		t.Error("the second picker did not pick up the first one's hide when it saved")
	}
	if !inVisible(b, b.cat.Servers[b.cursor].Name) {
		t.Errorf("the second picker's cursor is on %s, now hidden", b.cat.Servers[b.cursor].Name)
	}
}

// Deleting the last active row leaves the cursor on whatever moved up into
// its place, which may be a folded hidden server.
func TestCursorLeavesAHiddenRowAfterADelete(t *testing.T) {
	p := hidePicker(t)
	p.hidden = map[string]bool{"browser": true}
	p.cursor = 5 // sentry; browser follows it in the catalog
	p.removeCurrent()
	if _, ok := p.current(); !ok {
		t.Errorf("cursor on %s, a hidden row, after a delete", p.cat.Servers[p.cursor].Name)
	}
}
