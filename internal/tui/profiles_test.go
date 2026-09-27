package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/profile"
)

// profilesPicker is a picker with two personal profiles, one of them naming
// a server this project does not have, and the sample catalog's `review`.
func profilesPicker(t *testing.T) *picker {
	t.Helper()
	p := newPicker(t)
	if err := p.profiles.Add("browser", []string{"playwright-mcp", "browser"}); err != nil {
		t.Fatal(err)
	}
	if err := p.profiles.Add("elsewhere", []string{"github", "ghost"}); err != nil {
		t.Fatal(err)
	}
	if err := p.profiles.Save(); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *picker) press(keys ...string) {
	for _, k := range keys {
		p.key(k)
		p.scrollProfiles()
	}
}

func rowNames(rows []profileRow) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.name)
	}
	return strings.Join(out, ",")
}

func fileText(t *testing.T, p *picker) string {
	t.Helper()
	data, err := os.ReadFile(p.profiles.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The screen must always offer default, then the user's own in their order,
// then the catalog's — so nothing a repository ships disappears, and the one
// the user edits most is what the cursor lands on.
func TestPOpensProfilesWithDefaultFirst(t *testing.T) {
	p := profilesPicker(t)
	p.press("p")
	if p.mode != modeProfiles {
		t.Fatalf("mode = %v", p.mode)
	}
	if p.pm.cursor != 1 {
		t.Errorf("cursor = %d, want the first personal profile", p.pm.cursor)
	}
	p.press("j") // elsewhere, which names a server this project lacks
	rows := p.profileRows()
	if got := rowNames(rows); got != "default,browser,elsewhere,review" {
		t.Errorf("rows = %s", got)
	}
	if rows[3].kind != rowCatalog {
		t.Error("review comes from the catalog and should be tagged so")
	}
	frame := plain(p.View().Content)
	for _, want := range []string{"Profiles", "default", "browser", "catalog", "ghost", "Missing here"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	t.Log("\n" + p.View().Content)
}

// Launching with a profile must skip what this project lacks and what Claude
// Code has disabled — and say so, because a silent skip looks like a bug.
func TestEnterLaunchesWithProfileSkippingMissingAndDisabled(t *testing.T) {
	p := profilesPicker(t)
	p.cat.Servers[2].Disabled = true // notion
	_ = p.profiles.Put("elsewhere", []string{"github", "ghost", "notion"})
	p.press("p", "j") // elsewhere
	p.press("enter")
	if !p.launch {
		t.Fatal("enter should launch")
	}
	if len(p.sel) != 1 || !p.sel["github"] {
		t.Errorf("sel = %v, want github only", p.sel)
	}
	if !strings.Contains(p.status, "1 missing here") || !strings.Contains(p.status, "1 disabled in Claude Code") {
		t.Errorf("status = %q", p.status)
	}
}

// default means "everything available": all but the servers disabled in
// Claude Code when launching claude; for codex, all of them.
func TestDefaultIsEverythingAvailable(t *testing.T) {
	p := profilesPicker(t)
	p.cat.Servers[2].Disabled = true
	p.press("p", "g", "enter")
	if len(p.sel) != len(p.cat.Servers)-1 || p.sel["notion"] {
		t.Errorf("sel = %v", p.sel)
	}

	p = profilesPicker(t)
	p.tgt, p.argv = mustTarget(t, "codex"), []string{"codex"}
	p.cat.Servers[2].Disabled = true
	p.press("p", "g", "enter")
	if len(p.sel) != len(p.cat.Servers) {
		t.Errorf("for codex sel = %v, want everything", p.sel)
	}
}

// `s` still saves the selection as a profile — now into the user's file, in
// catalog order, never into the repository's catalog.
func TestAddSavesCurrentSelection(t *testing.T) {
	p := newPicker(t)
	p.sel = map[string]bool{"playwright-mcp": true, "github": true}
	p.press("s")
	if p.mode != modeProfiles || p.pm.sub != pmAdd {
		t.Fatalf("s should open the screen with the name prompt: mode %v sub %v", p.mode, p.pm.sub)
	}
	p.press("r", "e", "v", "enter")
	if p.pm.sub != pmNone {
		t.Fatalf("status = %q", p.status)
	}
	if !strings.Contains(fileText(t, p), "servers: [github, playwright-mcp]") {
		t.Errorf("file:\n%s", fileText(t, p))
	}
	if !strings.Contains(p.status, `profile "rev" saved to`) {
		t.Errorf("status = %q", p.status)
	}
	if p.profileRows()[p.pm.cursor].name != "rev" {
		t.Error("the cursor should land on the new profile")
	}
	if _, ok := p.cat.Profiles["rev"]; ok {
		t.Error("the catalog must not gain a profile")
	}
}

// A profile shipped in the catalog is made personal with one key; from then
// on the personal one is what every lookup sees.
func TestCopyMakesCatalogProfilePersonal(t *testing.T) {
	p := profilesPicker(t)
	p.press("p", "G") // review, from the catalog
	if p.currentProfile().kind != rowCatalog {
		t.Fatal("cursor should be on the catalog profile")
	}
	p.press("c", "enter")
	if p.pm.sub != pmNone {
		t.Fatalf("copy failed: %q", p.status)
	}
	rows := p.profileRows()
	if got := rowNames(rows); got != "default,browser,elsewhere,review" {
		t.Errorf("rows = %s", got)
	}
	if rows[3].kind != rowPersonal {
		t.Error("the copy should shadow the catalog profile")
	}
	if !strings.Contains(fileText(t, p), "name: review") {
		t.Error("the copy was not saved")
	}
	if got := p.profileRows()[3].servers; strings.Join(got, ",") != "github,sentry" {
		t.Errorf("copied servers = %v", got)
	}
}

// Read-only profiles must not be changed by a stray key, and the status has
// to say why nothing happened. A catalog profile can be deleted from its file,
// but only through the confirmation, which n cancels.
func TestDefaultAndCatalogAreReadOnly(t *testing.T) {
	for _, nav := range []string{"g", "G"} {
		p := profilesPicker(t)
		before := fileText(t, p)
		p.press("p", nav)
		for _, k := range []string{"d", "r", "J", "K"} {
			p.status = ""
			p.press(k)
			if k == "d" && nav == "G" {
				if p.pm.sub != pmConfirmDelete {
					t.Errorf("d on a catalog profile: sub %v, want the confirmation", p.pm.sub)
				}
				p.press("n")
				if p.pm.sub != pmNone || p.status != "cancelled" {
					t.Errorf("n: sub %v, status %q", p.pm.sub, p.status)
				}
				continue
			}
			if p.pm.sub != pmNone || p.status == "" {
				t.Errorf("%s on %s: sub %v, status %q", k, p.currentProfile().name, p.pm.sub, p.status)
			}
		}
		p.press("tab")
		for _, k := range []string{"space", "a", "n"} {
			p.status = ""
			p.press(k)
			if p.status == "" {
				t.Errorf("%s on %s changed nothing and said nothing", k, p.currentProfile().name)
			}
		}
		if fileText(t, p) != before {
			t.Error("the file changed")
		}
		if len(p.cat.Profiles["review"]) != 2 {
			t.Error("the catalog profile changed")
		}
	}
}

// Deleting a profile is confirmed and removes the profile only; the servers
// it named stay in the catalog.
func TestDeleteAsksAndKeepsServers(t *testing.T) {
	p := profilesPicker(t)
	n := len(p.cat.Servers)
	p.press("p", "d")
	if p.pm.sub != pmConfirmDelete || !strings.Contains(plain(p.View().Content), `delete profile "browser"?`) {
		t.Fatalf("no confirmation: sub %v", p.pm.sub)
	}
	p.press("n")
	if _, _, ok := p.profiles.Find("browser"); !ok {
		t.Fatal("anything but y must keep the profile")
	}
	p.press("d", "y")
	if _, _, ok := p.profiles.Find("browser"); ok {
		t.Error("y should delete")
	}
	if strings.Contains(fileText(t, p), "browser") {
		t.Error("the file still has the profile")
	}
	if len(p.cat.Servers) != n {
		t.Error("deleting a profile must not touch the servers")
	}
	if p.currentProfile().name != "elsewhere" {
		t.Errorf("cursor on %s", p.currentProfile().name)
	}
}

// J/K reorder, and the file follows, so the order survives the session.
func TestReorderPersists(t *testing.T) {
	p := profilesPicker(t)
	p.press("p", "J")
	if got := rowNames(p.profileRows()); got != "default,elsewhere,browser,review" {
		t.Errorf("rows = %s", got)
	}
	if p.currentProfile().name != "browser" {
		t.Error("the cursor should follow the moved profile")
	}
	text := fileText(t, p)
	if strings.Index(text, "elsewhere") > strings.Index(text, "browser") {
		t.Errorf("file order did not follow:\n%s", text)
	}
	p.press("J") // already last: no-op, no error
	if got := rowNames(p.profileRows()); got != "default,elsewhere,browser,review" {
		t.Errorf("rows = %s", got)
	}
	p.press("K", "K")
	if got := rowNames(p.profileRows()); got != "default,browser,elsewhere,review" {
		t.Errorf("rows = %s", got)
	}
}

// Toggling in the right pane writes at once; a missing row toggles off by
// leaving the profile, while other missing names are kept.
func TestToggleInRightPaneSaves(t *testing.T) {
	p := profilesPicker(t)
	p.press("p", "j", "tab") // elsewhere: github, ghost
	p.press("space")         // github off
	if !strings.Contains(fileText(t, p), "servers: [ghost]") {
		t.Errorf("file:\n%s", fileText(t, p))
	}
	p.press("j", "space") // local-tool on
	if !strings.Contains(fileText(t, p), "servers: [local-tool, ghost]") {
		t.Errorf("present first, missing last:\n%s", fileText(t, p))
	}
	p.press("G", "space") // the missing row: ghost
	if !strings.Contains(fileText(t, p), "servers: [local-tool]") {
		t.Errorf("space on a missing row should remove it:\n%s", fileText(t, p))
	}
	p.press("a")
	pr, _, _ := p.profiles.Find("elsewhere")
	if len(pr.Servers) != len(p.cat.Servers) {
		t.Errorf("a should check everything: %v", pr.Servers)
	}
	p.press("n")
	pr, _, _ = p.profiles.Find("elsewhere")
	if len(pr.Servers) != 0 {
		t.Errorf("n should clear: %v", pr.Servers)
	}
}

// `l` applies the profile and returns to the list without launching.
func TestLoadReturnsWithoutLaunching(t *testing.T) {
	p := profilesPicker(t)
	p.press("p", "l") // browser
	if p.mode != modeList || p.launch {
		t.Fatalf("mode %v launch %v", p.mode, p.launch)
	}
	if len(p.sel) != 2 || !p.sel["playwright-mcp"] || !p.sel["browser"] {
		t.Errorf("sel = %v", p.sel)
	}
	if !strings.Contains(p.status, `profile "browser" loaded`) {
		t.Errorf("status = %q", p.status)
	}
}

// Editing profiles is not selecting: esc leaves the picker's boxes as they
// were, and q goes back rather than aborting mcpick.
func TestEscLeavesSelectionAlone(t *testing.T) {
	p := profilesPicker(t)
	p.press("p", "tab", "space", "space", "tab", "J", "esc")
	if p.mode != modeList || p.launch {
		t.Fatalf("mode %v", p.mode)
	}
	if len(p.sel) != 2 || !p.sel["github"] || !p.sel["playwright-mcp"] {
		t.Errorf("sel = %v", p.sel)
	}
	p.press("p", "q")
	if p.mode != modeList {
		t.Error("q should go back, not quit")
	}
}

// Nothing may wrap or overflow: a wrapped line breaks the whole layout.
func TestProfilesViewFitsWindow(t *testing.T) {
	for _, h := range []int{12, 24} {
		for _, w := range []int{40, 60, 100} {
			p := profilesPicker(t)
			p.cat.Servers[2].Disabled = true
			p.cache.Entries["github"] = mcp.Measurement{Tokens: 4200, OK: true, Spec: mcp.Fingerprint(p.cat.Servers[0].Spec)}
			p.width, p.height = w, h
			p.press("p", "j", "tab", "G")
			frame := p.View().Content
			if lines := strings.Count(frame, "\n"); lines > h {
				t.Errorf("%dx%d: %d lines:\n%s", w, h, lines, frame)
			}
			for _, line := range strings.Split(frame, "\n") {
				if lw := lipgloss.Width(plain(line)); lw > w {
					t.Errorf("%dx%d: line is %d wide: %q", w, h, lw, plain(line))
				}
			}
			if h == 24 && w == 100 {
				t.Log("\n" + frame)
			}
		}
	}
	// Narrower than 40 columns the panes stack and still fit.
	p := profilesPicker(t)
	p.width, p.height = 30, 14
	p.press("p")
	frame := p.View().Content
	if lines := strings.Count(frame, "\n"); lines > p.height {
		t.Errorf("stacked: %d lines:\n%s", lines, frame)
	}
	for _, line := range strings.Split(frame, "\n") {
		if lw := lipgloss.Width(plain(line)); lw > p.width {
			t.Errorf("stacked: line is %d wide: %q", lw, plain(line))
		}
	}
}

// Bubble Tea may call View any number of times; it must not move state.
func TestProfilesViewIsPure(t *testing.T) {
	p := profilesPicker(t)
	p.height = 10
	p.press("p", "tab", "G")
	off := p.pm
	first := p.View().Content
	if p.pm != off {
		t.Fatal("View changed the screen state")
	}
	if p.View().Content != first {
		t.Fatal("two Views of the same state differ")
	}
	if p.pm.offR == 0 {
		t.Error("the right pane should have scrolled to the last row")
	}
}

// A profiles file that does not parse is reported and never overwritten; the
// rest of the screen (default, the catalog's profiles) keeps working.
func TestCorruptProfilesFileIsReported(t *testing.T) {
	p := newPicker(t)
	t.Setenv("MCPICK_HOME", t.TempDir())
	if err := os.WriteFile(profile.Path(), []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.profiles = profile.Load()
	widen(p, p.profiles.Path())
	if p.profiles.Err() == nil {
		t.Fatal("the set should carry the parse error")
	}
	p.press("p")
	if !strings.Contains(plain(p.View().Content), "could not be read") {
		t.Errorf("the frame should warn:\n%s", plain(p.View().Content))
	}
	p.press("+", "x", "enter")
	if !strings.Contains(plain(p.status), "could not be read") {
		t.Errorf("status = %q", p.status)
	}
	data, _ := os.ReadFile(p.profiles.Path())
	if string(data) != "profiles: [\n" {
		t.Error("the file was overwritten")
	}
	if got := rowNames(p.profileRows()); !strings.HasPrefix(got, "default,") || !strings.HasSuffix(got, "review") {
		t.Errorf("rows = %s", got)
	}
}

// Copying default must copy what its boxes show, not the whole catalog: a
// server disabled in Claude Code is unchecked there, and a copy that held it
// would run it the next time the copy is used headlessly with claude. For an
// agent that does not read Claude's settings the same server is ordinary.
func TestCopyDefaultCopiesOnlyWhatIsChecked(t *testing.T) {
	p := profilesPicker(t)
	p.cat.Servers[2].Disabled = true // notion
	p.press("p", "g", "c", "m", "i", "n", "e", "enter")
	if p.pm.sub != pmNone {
		t.Fatalf("copy failed: %q", plain(p.status))
	}
	pr, _, _ := p.profiles.Find("mine")
	if contains(pr.Servers, "notion") || len(pr.Servers) != len(p.cat.Servers)-1 {
		t.Errorf("copy of default for claude = %v", pr.Servers)
	}

	p = profilesPicker(t)
	p.tgt, p.argv = mustTarget(t, "codex"), []string{"codex"}
	p.cat.Servers[2].Disabled = true
	p.press("p", "g", "c", "m", "i", "n", "e", "enter")
	pr, _, _ = p.profiles.Find("mine")
	if !contains(pr.Servers, "notion") || len(pr.Servers) != len(p.cat.Servers) {
		t.Errorf("copy of default for codex = %v", pr.Servers)
	}
}

// A catalog from before "default" was reserved may define one. Both rows are
// listed, told apart by their tag, each does what it says, a warning explains,
// and copying the catalog's one asks for a new name since default is taken.
func TestCatalogDefaultIsToldApart(t *testing.T) {
	p := profilesPicker(t)
	widen(p, p.cat.Path)
	p.cat.Profiles["default"] = []string{"github"}
	p.press("p")
	rows := p.profileRows()
	if got := rowNames(rows); got != "default,browser,elsewhere,default,review" {
		t.Fatalf("rows = %s", got)
	}
	frame := plain(p.View().Content)
	for _, want := range []string{"built-in", "catalog", `also defines "default"`, "rename it there"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	p.press("j", "j") // the catalog's default
	if r := p.currentProfile(); r.kind != rowCatalog || r.name != "default" {
		t.Fatalf("cursor on %+v", r)
	}
	p.press("c")
	if p.pm.input != "" {
		t.Errorf("copy prefilled %q, a name that cannot be used", p.pm.input)
	}
	p.press("esc", "enter")
	if len(p.sel) != 1 || !p.sel["github"] {
		t.Errorf("the catalog's default selected %v, want only what it lists", p.sel)
	}
}

// Two pickers open on the same file: each one's edits must survive the
// other's, since every edit is written at once and neither knows of the other.
func TestTwoPickersKeepEachOthersProfiles(t *testing.T) {
	a := profilesPicker(t)
	b := newPicker(t)
	b.profiles = profile.New(a.profiles.Path())
	a.press("p", "+", "o", "n", "e", "enter")
	b.press("p", "+", "t", "w", "o", "enter")
	if a.pm.sub != pmNone || b.pm.sub != pmNone {
		t.Fatalf("add failed: %q / %q", plain(a.status), plain(b.status))
	}
	if got := rowNames(b.profileRows()); got != "default,browser,elsewhere,one,two,review" {
		t.Errorf("b sees %s", got)
	}
	// a has not seen "two" yet; deleting browser from a must neither lose
	// "two" nor bring browser back.
	a.press("g", "j", "d", "y")
	if got := rowNames(a.profileRows()); got != "default,elsewhere,one,two,review" {
		t.Errorf("a sees %s", got)
	}
	if !strings.Contains(fileText(t, a), "name: two") || strings.Contains(fileText(t, a), "name: browser") {
		t.Errorf("file:\n%s", fileText(t, a))
	}
}

// Whatever state the screen is in — a long profile name, a prompt being
// typed, the delete question, an error — no line may exceed the width and no
// frame the height; a wrapped line moves everything below it.
func TestProfilesViewFitsInEveryState(t *testing.T) {
	long := "my-very-long-profile-name-that-goes-on"
	states := map[string][]string{
		"idle":     {"p"},
		"right":    {"p", "tab", "G"},
		"add":      {"p", "+", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"},
		"rename":   {"p", "r"}, // the cursor opens on the long name
		"copy":     {"p", "c"},
		"delete":   {"p", "d"},
		"error":    {"p", "+", "e", "l", "s", "e", "w", "h", "e", "r", "e", "enter"},
		"readonly": {"p", "g", "tab", "space"},
	}
	check := func(t *testing.T, p *picker, w, h int, must ...string) {
		t.Helper()
		frame := p.View().Content
		if lines := strings.Count(frame, "\n"); lines > h {
			t.Errorf("%dx%d: %d lines:\n%s", w, h, lines, frame)
		}
		for _, line := range strings.Split(frame, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("%dx%d: line is %d wide: %q", w, h, lw, plain(line))
			}
		}
		for _, m := range must {
			if !strings.Contains(plain(frame), m) {
				t.Errorf("%dx%d: frame lacks %q:\n%s", w, h, m, plain(frame))
			}
		}
	}
	for name, keys := range states {
		for _, h := range []int{6, 8, 12, 24} {
			for _, w := range []int{16, 24, 40, 47, 60} {
				p := profilesPicker(t)
				if err := p.profiles.Rename("browser", long); err != nil {
					t.Fatal(err)
				}
				_ = p.profiles.Save()
				p.cache.Entries["github"] = mcp.Measurement{Tokens: 4200, OK: true, Spec: mcp.Fingerprint(p.cat.Servers[0].Spec)}
				p.width, p.height = w, h
				p.press(keys...)
				var must []string
				switch name {
				case "add":
					must = []string{"xyz"} // the end of what is being typed
				case "delete":
					must = []string{"[y/N]"}
				case "rename", "copy":
					must = []string{"goes-on"} // the input, prefilled with the name, keeps its tail
				case "error":
					if h >= 8 && w >= 40 { // shorter, the prompt keeps the last line; narrower, the text is cut
						must = []string{"already exists"}
					}
				}
				check(t, p, w, h, must...)
				if name == "delete" && w == 40 && h == 12 {
					t.Log("\n" + p.View().Content)
				}
			}
		}
	}
}

// clip is the last line of defence for every drawn line: it must count
// cells, not runes, keep escape sequences whole, and end a cut with a reset.
func TestClipCountsCellsAndKeepsEscapes(t *testing.T) {
	if got := clip("abcdef", 4); got != "abc…\x1b[0m" {
		t.Errorf("clip = %q", got)
	}
	if got := clip("abc", 3); got != "abc" {
		t.Errorf("clip of a fitting string = %q", got)
	}
	wide := "日本語のサーバー" // two cells each
	if got := lipgloss.Width(clip(wide, 7)); got > 7 {
		t.Errorf("wide characters: %d cells", got)
	}
	styled := styErr.Render("save failed: " + strings.Repeat("x", 50))
	got := clip(styled, 20)
	if lipgloss.Width(got) > 20 {
		t.Errorf("styled: %d cells: %q", lipgloss.Width(got), got)
	}
	if !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("styled clip should keep the opening sequence and end with a reset: %q", got)
	}
	if got := clip("abc", 0); got != "" {
		t.Errorf("clip to 0 = %q", got)
	}
}

// Every edit reloads the file, and the rows may have shifted meanwhile (a
// profile deleted by another picker): the cursor must stay on the profile it
// was on, by name, or the next key edits — or launches — a different one.
func TestCursorFollowsProfileAfterAnotherPickerDeletes(t *testing.T) {
	a := profilesPicker(t)
	b := newPicker(t)
	b.profiles = profile.New(a.profiles.Path())
	if err := b.profiles.Update(func(*profile.Set) error { return nil }); err != nil { // b reads the file
		t.Fatal(err)
	}
	a.press("p", "j", "tab") // elsewhere: github, ghost; right pane on github
	b.press("p", "d", "y")   // browser, the row above elsewhere, is gone
	if _, _, ok := b.profiles.Find("browser"); ok {
		t.Fatalf("b did not delete: %q", plain(b.status))
	}
	a.press("space") // github off; a now sees the shifted rows
	if got := a.currentProfile().name; got != "elsewhere" {
		t.Fatalf("cursor on %q after the reload, want elsewhere", got)
	}
	a.press("space") // github back on, in the same profile
	if !strings.Contains(fileText(t, a), "servers: [github, ghost]") {
		t.Errorf("the second toggle hit another row:\n%s", fileText(t, a))
	}
	if a.pm.cursor != 1 || a.pm.srv != 0 {
		t.Errorf("cursor %d srv %d", a.pm.cursor, a.pm.srv)
	}
}

// A profile name of wide characters counts two cells a rune; the prompt must
// fit by cells, with the input's tail or [y/N] kept whatever the name.
func TestPromptLineFitsWideNames(t *testing.T) {
	wide := strings.Repeat("日本語", 8) // 24 runes, 48 cells
	for _, w := range []int{30, 40, 60} {
		line := promptLine("delete profile ", wide, "? ", "[y/N]", w)
		if lw := lipgloss.Width(line); lw > w || !strings.HasSuffix(line, "[y/N]") {
			t.Errorf("w=%d: %d cells, %q", w, lw, line)
		}
		line = promptLine("rename ", wide, " to: ", "new-name", w)
		if lw := lipgloss.Width(line); lw > w || !strings.HasSuffix(line, "new-name") {
			t.Errorf("w=%d: %d cells, %q", w, lw, line)
		}
	}
	if got := truncateCells("日本語", 4); got != "日…" {
		t.Errorf("truncateCells = %q", got)
	}
	if got := truncateCells("abc", 3); got != "abc" {
		t.Errorf("truncateCells of a fitting string = %q", got)
	}
	// And the frame itself, at the width the delete question first fails.
	p := profilesPicker(t)
	if err := p.profiles.Rename("browser", wide); err != nil {
		t.Fatal(err)
	}
	_ = p.profiles.Save()
	p.width, p.height = 40, 12
	p.press("p", "d")
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "[y/N]") {
		t.Errorf("the question lost its keys:\n%s", frame)
	}
}

// Warnings take lines from the panes; more of them than the window has rows
// must be summarised, not drawn past the bottom.
func TestProfilesWarningsFitTheHeight(t *testing.T) {
	setup := func(t *testing.T, w, h int) *picker {
		t.Helper()
		p := newPicker(t)
		t.Setenv("MCPICK_HOME", t.TempDir())
		if err := os.WriteFile(profile.Path(), []byte("profiles: [\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		p.profiles = profile.Load()
		p.profiles.Warnings = []string{"one was dropped", "two was dropped", "three was dropped"}
		p.cat.Profiles["default"] = []string{"github"}
		p.width, p.height = w, h
		p.press("p")
		if n := len(p.profilesWarnings()); n != 5 {
			t.Fatalf("%d warnings, want 5", n)
		}
		return p
	}
	for _, size := range [][2]int{{40, 6}, {60, 8}, {100, 12}, {100, 24}} {
		w, h := size[0], size[1]
		p := setup(t, w, h)
		frame := p.View().Content
		if lines := strings.Count(frame, "\n"); lines > h {
			t.Errorf("%dx%d: %d lines:\n%s", w, h, lines, frame)
		}
		for _, line := range strings.Split(frame, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("%dx%d: line is %d wide: %q", w, h, lw, plain(line))
			}
		}
		shown := strings.Count(plain(frame), "\n! ")
		switch {
		case h >= 12 && shown != 5:
			t.Errorf("%dx%d: %d warning lines, want all 5:\n%s", w, h, shown, plain(frame))
		case h == 8 && (shown != 3 || !strings.Contains(plain(frame), "more warnings")):
			t.Errorf("%dx%d: %d warning lines, want 2 and a summary:\n%s", w, h, shown, plain(frame))
		}
		if h == 8 {
			t.Log("\n" + frame)
		}
	}
}

// A reported catalog: a prompt an older picker saved as a profile name, long
// enough that YAML writes it as a complex key (? ... : []). d deletes it from
// the file it came from, after naming the file; the servers, their headers
// and the rest of the file stay.
func TestDeleteCatalogProfileFromItsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(root, ".mcp.yaml")
	long := `tigatethefailingcheckouttestinhttps://example.com/issues/4321("Payments:9ordersstuckafterthefraudcheck").Describetheproblem,thelikelycauseandthefilesinvolved.DoNOTchangecode.`
	yml := "servers:\n" +
		"  search:\n    type: http\n    url: https://mcp.example.com/search\n    headers:\n      Authorization: \"Bearer xxx\"\n" +
		"  docs:\n    type: http\n    url: https://mcp.example.com/docs\n" +
		"profiles:\n  ? " + long + "\n  : []\n"
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(path, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Profiles[long]; !ok {
		t.Fatalf("profiles = %v", cat.Profiles)
	}
	p := newPicker(t)
	p.cat, p.sel, p.cursor = cat, map[string]bool{}, 0
	widen(p, path)
	p.press("p", "j") // default, then the catalog profile
	if row := p.currentProfile(); row.name != long {
		t.Fatalf("cursor on %q", row.name)
	}
	p.press("d")
	if p.pm.sub != pmConfirmDelete || !strings.Contains(plain(p.View().Content), ".mcp.yaml") {
		t.Fatalf("no confirmation naming the file: sub %v\n%s", p.pm.sub, plain(p.View().Content))
	}
	p.press("y")
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "tigate") || strings.Contains(string(data), "profiles") {
		t.Errorf("the profile is still in the file:\n%s", data)
	}
	for _, want := range []string{"search:", "Authorization: \"Bearer xxx\"", "docs:"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lost %s:\n%s", want, data)
		}
	}
	if _, ok := p.cat.Profiles[long]; ok {
		t.Error("the profile is still in the catalog")
	}
	for _, r := range p.profileRows() {
		if r.name == long {
			t.Error("the profile is still listed")
		}
	}
	if !strings.Contains(p.status, "deleted profile") {
		t.Errorf("status = %q", p.status)
	}
}
