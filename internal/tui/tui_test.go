package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cajbecu/mcpick/internal/backend"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/trust"
)

func httpSpec(u string) map[string]any {
	return map[string]any{"type": "http", "url": u}
}

func sampleCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Path:       filepath.Join(testRoot, ".mcp.yaml"),
		ClaudePath: filepath.Join(testRoot, ".claude.json"),
		ProjectKey: "/src/app",
		Profiles:   map[string][]string{"review": {"github", "sentry"}},
		Servers: []catalog.Server{
			{Name: "github", Origin: catalog.OriginProject, Spec: httpSpec("https://api.githubcopilot.com/mcp/")},
			{Name: "local-tool", Origin: catalog.OriginProject, Spec: map[string]any{
				"type": "stdio", "command": "uvx",
				"args": []any{"some-mcp", "--session", "{UUID}"}}},
			{Name: "notion", Origin: catalog.OriginLocal, Spec: httpSpec("https://mcp.notion.com/mcp")},
			{Name: "linear", Origin: catalog.OriginLocal, Spec: httpSpec("https://mcp.linear.app/mcp")},
			{Name: "playwright-mcp", Origin: catalog.OriginLocal, Spec: httpSpec("http://playwright-mcp:8931/mcp")},
			{Name: "sentry", Origin: catalog.OriginUser, Spec: httpSpec("https://mcp.sentry.dev/mcp")},
			{Name: "browser", Origin: catalog.OriginUser, Spec: httpSpec("http://127.0.0.1:9010/mcp?session={UUID}")},
		},
	}
}

func newPicker(t *testing.T) *picker {
	t.Helper()
	return &picker{
		cat:       sampleCatalog(),
		sel:       map[string]bool{"github": true, "playwright-mcp": true},
		uid:       "deploy-1",
		uidGiven:  true,
		tgt:       mustTarget(t, "claude"),
		argv:      []string{"claude", "--dangerously-skip-permissions"},
		version:   "1.2.3",
		cursor:    2,
		width:     100,
		height:    40,
		cache:     &mcp.Cache{Entries: map[string]mcp.Measurement{}},
		measuring: map[string]bool{},
		profiles:  profile.New(filepath.Join(t.TempDir(), "profiles.yaml")),
		tr:        newTrustState(trust.Open(filepath.Join(t.TempDir(), "trust.json")), "/src/app"),
	}
}

// widen gives the frame room for the paths a test asserts on. A temporary
// directory is as long as the OS and the test name make it — 60 columns
// and more on macOS, on Windows and in the Nix sandbox — and a heading or a
// question that names a file is cut to the width otherwise, so a test that
// looks for the file name in the frame would depend on where it runs.
func widen(p *picker, paths ...string) {
	for _, path := range paths {
		p.width = max(p.width, len(path)+100)
	}
}

func TestRenderFrame(t *testing.T) {
	p := newPicker(t)
	frame := p.View().Content
	if frame == "" {
		t.Fatal("empty frame")
	}
	for _, want := range []string{"mcpick 1.2.3", "Project", "Local", "User", "github", "deploy-1"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame is missing %q", want)
		}
	}
	t.Log("\n" + frame)
}

func TestGroupCounts(t *testing.T) {
	p := newPicker(t)
	for _, tc := range []struct {
		origin     string
		sel, total int
	}{
		{catalog.OriginProject, 1, 2},
		{catalog.OriginLocal, 1, 3},
		{catalog.OriginUser, 0, 2},
	} {
		if got := p.groupCount(tc.origin); got != tc.sel {
			t.Errorf("%s selected = %d, want %d", tc.origin, got, tc.sel)
		}
		if got := p.groupTotal(tc.origin); got != tc.total {
			t.Errorf("%s total = %d, want %d", tc.origin, got, tc.total)
		}
	}
}

func TestFilterNarrowsList(t *testing.T) {
	p := newPicker(t)
	p.filter = "lin"
	vis := p.visible()
	if len(vis) != 1 || p.cat.Servers[vis[0]].Name != "linear" {
		t.Fatalf("filter returned %d rows, want linear only", len(vis))
	}

	// A filter on the target string matches too.
	p.filter = "127.0.0.1"
	vis = p.visible()
	if len(vis) != 1 || p.cat.Servers[vis[0]].Name != "browser" {
		t.Fatalf("target filter returned %d rows, want browser", len(vis))
	}
}

func TestFilteredAllTouchesOnlyVisible(t *testing.T) {
	p := newPicker(t)
	p.sel = map[string]bool{}
	p.filter = "playwright"
	p.updateList("a")
	if p.sel["github"] {
		t.Error("github is filtered out but got selected by 'a'")
	}
	if !p.sel["playwright-mcp"] {
		t.Error("playwright-mcp matches the filter and should be selected")
	}
}

func TestCursorStaysOnVisibleRows(t *testing.T) {
	p := newPicker(t)
	p.filter = "o"
	vis := p.visible()
	p.cursor = vis[0]
	for i := 0; i < 10; i++ {
		p.moveCursor(1)
	}
	found := false
	for _, idx := range vis {
		if idx == p.cursor {
			found = true
		}
	}
	if !found {
		t.Fatalf("cursor %d left the filtered set %v", p.cursor, vis)
	}
}

func TestViewFitsWindow(t *testing.T) {
	p := newPicker(t)
	p.height = 12
	lines := strings.Count(p.View().Content, "\n")
	if lines > p.height {
		t.Fatalf("frame is %d lines in a %d-line window", lines, p.height)
	}
}

// A server disabled in Claude Code toggles like any other; the words beside it
// say what launching will do: nothing while unchecked, re-enable once checked.
func TestDisabledServerSaysWhatWillHappen(t *testing.T) {
	p := newPicker(t)
	p.cat.Servers[0].Disabled = true
	p.cursor = 0
	p.sel = map[string]bool{}
	row := func() string {
		for _, line := range strings.Split(p.View().Content, "\n") {
			if strings.Contains(line, p.cat.Servers[0].Name) {
				return plain(line)
			}
		}
		return ""
	}
	if r := row(); !strings.Contains(r, "[ ]") || !strings.Contains(r, "disabled in Claude Code") {
		t.Fatalf("unchecked = %q", r)
	}
	p.updateList("space")
	if r := row(); !strings.Contains(r, "[x]") || !strings.Contains(r, "will be enabled in Claude Code") {
		t.Fatalf("checked = %q", r)
	}
	p.updateList("space")
	if r := row(); !strings.Contains(r, "[ ]") || !strings.Contains(r, "disabled in Claude Code") {
		t.Fatalf("unchecked again = %q", r)
	}
}

// "All" must not re-enable servers in Claude Code in bulk.
func TestSelectAllSkipsDisabled(t *testing.T) {
	p := newPicker(t)
	p.sel = map[string]bool{}
	p.cat.Servers[0].Disabled = true
	p.updateList("a")
	if p.sel[p.cat.Servers[0].Name] {
		t.Error("a disabled server was checked by 'a'")
	}
	if !p.sel[p.cat.Servers[1].Name] {
		t.Error("an ordinary server was not checked by 'a'")
	}
	if !strings.Contains(p.status, "left unchecked") {
		t.Errorf("status = %q; the user should know why some stayed off", p.status)
	}
}

// Claude's disabled list means nothing to another agent: for codex a server
// disabled in Claude Code is an ordinary one, with no label and no special
// case in "all".
func TestDisabledInClaudeIsOrdinaryForOtherAgents(t *testing.T) {
	p := newPicker(t)
	p.tgt, p.argv = mustTarget(t, "codex"), []string{"codex"}
	p.cat.Servers[0].Disabled = true
	p.sel = map[string]bool{}
	p.updateList("a")
	if !p.sel[p.cat.Servers[0].Name] {
		t.Error("'a' skipped a server only Claude has disabled")
	}
	if strings.Contains(plain(p.View().Content), "Claude Code") {
		t.Error("a codex picker talks about Claude Code's settings")
	}
}

// An ordinary server still just toggles.
func TestOrdinaryServerToggles(t *testing.T) {
	p := newPicker(t)
	p.cursor, p.sel = 1, map[string]bool{}
	p.updateList("space")
	p.updateList("space")
	if p.sel[p.cat.Servers[1].Name] {
		t.Error("two presses should leave an ordinary server off")
	}
}

func TestSelectedTokensSummary(t *testing.T) {
	p := newPicker(t)
	spec := p.cat.Servers[0].Spec
	p.cache.Entries["github"] = mcp.Measurement{Tokens: 4200, OK: true, Spec: mcp.Fingerprint(spec)}
	total, complete := p.selectedTokens()
	if total != 4200 {
		t.Errorf("total = %d, want 4200", total)
	}
	if complete {
		t.Error("playwright-mcp is unmeasured, so the total is not complete")
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	if got := truncate("ăăăăă", 3); len([]rune(got)) != 3 {
		t.Errorf("truncate returned %q (%d runes), want 3 runes", got, len([]rune(got)))
	}
}

// Bubble Tea may call View any number of times; it must not move state.
func TestViewIsPure(t *testing.T) {
	p := newPicker(t)
	p.height = 8
	p.cursor = len(p.cat.Servers) - 1
	p.offset = 0
	first := p.View().Content
	if p.offset != 0 {
		t.Fatal("View changed the scroll offset")
	}
	if p.View().Content != first {
		t.Fatal("two Views of the same state differ")
	}
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	if p.offset == 0 {
		t.Error("Update should scroll the window to keep the cursor visible")
	}
}

func TestPluginServerCannotBeDeleted(t *testing.T) {
	p := newPicker(t)
	p.cat.Servers = append(p.cat.Servers, catalog.Server{Name: "cf", Origin: catalog.OriginPlugin, Spec: httpSpec("https://cf")})
	p.cursor = len(p.cat.Servers) - 1
	p.updateList("d")
	if p.mode != modeList || !strings.Contains(p.status, "plugin") {
		t.Errorf("mode = %v, status = %q; a plugin server has no file mcpick may edit", p.mode, p.status)
	}
}

// A failed measurement shows its kind in the row and the full reason, with
// the fix, under the list when the cursor is on it.
func TestFailedMeasurementIsExplained(t *testing.T) {
	p := newPicker(t)
	s := p.cat.Servers[0]
	p.cache.Entries[s.Name] = mcp.Measurement{OK: false, Err: "HTTP 401: Unauthorized", Spec: mcp.Fingerprint(s.Spec)}
	p.cursor = 0
	frame := p.View().Content
	if !strings.Contains(frame, "401") {
		t.Error("the row should say 401")
	}
	if !strings.Contains(frame, "HTTP 401: Unauthorized") || !strings.Contains(frame, "mcpick login "+s.Name) {
		t.Errorf("the footer should explain the failure and name the fix:\n%s", frame)
	}
	p.cursor = 1
	if strings.Contains(p.View().Content, "HTTP 401: Unauthorized") {
		t.Error("the explanation belongs to the row under the cursor only")
	}
}

// The marks in the list are explained on the screen itself.
func TestLegendExplainsMarks(t *testing.T) {
	p := newPicker(t)
	if frame := plain(p.View().Content); strings.Contains(frame, "HTTP error") {
		t.Error("nothing failed yet, and the legend explains failures")
	}
	p.Update(measuredMsg{name: "github", spec: p.cat.Servers[0].Spec, res: mcp.Result{Err: "401 Unauthorized"}})
	frame := plain(p.View().Content)
	for _, want := range []string{"Legend:", "[x]", "context cost", "HTTP error"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the legend should mention %q", want)
		}
	}
	for _, width := range []int{120, 80, 50, 30} {
		p.width = width
		line := plain(p.legend())
		if w := lipgloss.Width(line); w > width {
			t.Errorf("legend is %d wide in a %d-column terminal", w, width)
		}
		if !strings.Contains(line, "Legend:") {
			t.Errorf("at %d columns the legend lost its label", width)
		}
	}
}

func mustTarget(t *testing.T, name string) backend.Backend {
	t.Helper()
	tgt, err := backend.Pick(name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tgt
}

func TestHeaderShowsAgentBadge(t *testing.T) {
	p := newPicker(t)
	frame := p.View().Content
	if !strings.Contains(frame, "✻ claude") {
		t.Errorf("the header should name the agent with its glyph:\n%s", frame)
	}

	// An agent mcpick has no adapter for must not look like a supported one.
	p.tgt, p.argv = mustTarget(t, "generic"), []string{"my-wrapper", "--x"}
	frame = p.View().Content
	if !strings.Contains(frame, "my-wrapper (unknown agent)") {
		t.Errorf("an unknown agent should be named and flagged:\n%s", frame)
	}
}

// The command under the header is what will run: for claude, the flags mcpick
// adds in front of the user's own.
func TestHeaderShowsFinalCommand(t *testing.T) {
	p := newPicker(t)
	frame := p.View().Content
	want := "→ claude --mcp-config <rendered config> --strict-mcp-config --dangerously-skip-permissions"
	if !strings.Contains(frame, want) {
		t.Errorf("frame lacks %q:\n%s", want, frame)
	}
}

// For gemini the command names the servers, so it has to follow the boxes.
func TestFinalCommandFollowsSelection(t *testing.T) {
	p := newPicker(t)
	p.tgt, p.argv = mustTarget(t, "gemini"), []string{"gemini"}
	p.sel = map[string]bool{"github": true}
	if !strings.Contains(p.commandLine(), "--allowed-mcp-server-names github") {
		t.Errorf("command = %q", p.commandLine())
	}
	p.sel["sentry"] = true
	if !strings.Contains(p.commandLine(), "--allowed-mcp-server-names github,sentry") {
		t.Errorf("command did not follow the selection: %q", p.commandLine())
	}
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain drops colour codes, so tests compare what a reader sees.
func plain(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// Pressing m measures every server again, including ones already measured
// this session: what is on screen is always from the latest press.
func TestMeasureAlwaysMeasuresAgain(t *testing.T) {
	p := newPicker(t)
	s := p.cat.Servers[0]
	p.cache.Entries[s.Name] = mcp.Measurement{OK: true, Tokens: 1, Spec: mcp.Fingerprint(s.Spec)}
	if cmd := p.measureCmd(); cmd == nil {
		t.Fatal("m did nothing")
	}
	if _, ok := p.cache.Entries[s.Name]; ok {
		t.Error("the old measurement should be dropped when measuring again")
	}
	if !p.measuring[s.Name] {
		t.Error("the server should be measuring")
	}
}

// A server the repository defines is not contacted until it is checked: its
// command or URL is its author's choice, with the environment expanded into
// it. Servers from the user's own config are measured as before.
func TestMeasureSkipsUncheckedRepositoryServers(t *testing.T) {
	p := newPicker(t)
	p.sel = map[string]bool{"github": true} // project, checked
	p.measureCmd()
	if !p.measuring["github"] {
		t.Error("a checked project server should be measured")
	}
	if p.measuring["local-tool"] {
		t.Error("an unchecked project server must not be run")
	}
	if !p.measuring["sentry"] {
		t.Error("a server from the user's own config should be measured")
	}
	if !strings.Contains(p.status, "skipped") {
		t.Errorf("status = %q; the user should know some were skipped and why", p.status)
	}
}

// The filter narrows the list as it is typed, and shows itself above the
// list rather than in a prompt at the bottom.
func TestFilterIsLiveAndInPlace(t *testing.T) {
	p := newPicker(t)
	p.updateList("/")
	for _, k := range []string{"l", "i", "n"} {
		p.key(k)
	}
	if len(p.visible()) != 1 || p.cat.Servers[p.visible()[0]].Name != "linear" {
		t.Fatalf("typing did not narrow the list: %v", p.visible())
	}
	frame := plain(p.View().Content)
	lines := strings.Split(frame, "\n")
	filterAt, listAt := -1, -1
	for i, l := range lines {
		if strings.HasPrefix(l, "/ lin") && filterAt < 0 {
			filterAt = i
		}
		if strings.Contains(l, "linear") && listAt < 0 {
			listAt = i
		}
	}
	if filterAt < 0 || listAt < 0 || filterAt > listAt {
		t.Fatalf("the filter should sit above the list:\n%s", frame)
	}
	if strings.Contains(frame, "filter: ") {
		t.Error("no prompt at the bottom any more")
	}
	// The cursor follows onto what remains.
	if p.cat.Servers[p.cursor].Name != "linear" {
		t.Errorf("cursor on %s", p.cat.Servers[p.cursor].Name)
	}
	p.key("enter")
	if p.filter != "lin" || p.mode != modeList {
		t.Error("enter keeps the filter and returns to the list")
	}
	p.key("esc")
	if p.filter != "" {
		t.Error("esc in the list drops a kept filter before anything else")
	}
}

// The detail line and the command preview carry text from the catalog and
// from the server's own error message; an escape in either must not reach the
// terminal, or it could hide part of what is about to launch.
func TestDetailAndCommandLineNeutraliseEscapes(t *testing.T) {
	p := newPicker(t)
	evil := "safe\x1b[8m-evil\x1b[0m"
	p.cat.Servers[0].Name = evil
	p.cache.Entries[evil] = mcp.Measurement{Err: "boom\x1b[8m hidden", Spec: mcp.Fingerprint(p.cat.Servers[0].Spec)}
	if got := p.detailFor(p.cat.Servers[0]); strings.Contains(got, "\x1b[8m") || !strings.Contains(plain(got), "-evil") {
		t.Errorf("detail = %q", got)
	}
	p.tgt, p.argv = mustTarget(t, "gemini"), []string{"gemini"}
	if got := p.commandLineFor([]string{evil}); strings.Contains(got, "\x1b[8m") || !strings.Contains(plain(got), "-evil") {
		t.Errorf("command line = %q", got)
	}
}

// A line wider than the window is wrapped by the terminal, one line more than
// the picker drew; each redraw then leaves the header behind, repeated up the
// screen. Nothing may be wider than the window or the frame taller: a long
// group heading at 60 columns, a long error on the status line, a short window.
func TestFrameNeverOutgrowsTheWindow(t *testing.T) {
	for _, size := range [][2]int{{60, 40}, {80, 24}, {100, 8}, {40, 5}} {
		p := newPicker(t)
		p.width, p.height = size[0], size[1]
		p.Update(measuredMsg{name: "github", spec: p.cat.Servers[0].Spec,
			res: mcp.Result{Err: "connecting to https://example.com/a/very/long/path: " + strings.Repeat("x", 300)}})
		c := p.View().Content
		lines := strings.Split(c, "\n")
		if len(lines) > p.height {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > p.width {
				t.Errorf("%dx%d: line %d is %d wide: %q", size[0], size[1], i, w, plain(l))
			}
		}
	}
}

// Both .mcp.yaml and .mcp.json are read. A server defined in .mcp.json is
// deleted from .mcp.json, not looked for in .mcp.yaml — where it is not, so
// the delete "succeeded" and the server came back on the next start.
func TestDeleteRemovesAProjectServerFromItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	yml, jsn := filepath.Join(dir, ".mcp.yaml"), filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(yml, []byte("servers:\n  a: {type: http, url: \"https://a\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsn, []byte(`{"mcpServers": {"b": {"type": "http", "url": "https://b"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := newPicker(t)
	p.cat = &catalog.Catalog{Path: yml, ClaudePath: filepath.Join(dir, ".claude.json"), Servers: []catalog.Server{
		{Name: "a", Origin: catalog.OriginProject, Source: yml, Spec: map[string]any{"type": "http", "url": "https://a"}},
		{Name: "b", Origin: catalog.OriginProject, Source: jsn, Spec: map[string]any{"type": "http", "url": "https://b"}},
	}}
	p.sel, p.cursor = map[string]bool{}, 1
	widen(p, jsn)
	p.updateList("d")
	if !strings.Contains(plain(p.View().Content), ".mcp.json") {
		t.Errorf("the question does not name .mcp.json:\n%s", plain(p.View().Content))
	}
	p.updateConfirmYAML("y")
	if data, _ := os.ReadFile(jsn); strings.Contains(string(data), `"b"`) {
		t.Errorf(".mcp.json still has b: %s", data)
	}
	if data, _ := os.ReadFile(yml); !strings.Contains(string(data), "a:") {
		t.Errorf(".mcp.yaml lost a: %s", data)
	}
}

// Every screen is drawn on the alternate screen: inline, a frame as tall as
// the window scrolled the terminal, and going back from the profile screen
// left its top lines above the list.
func TestEveryScreenUsesTheAlternateScreen(t *testing.T) {
	p := newPicker(t)
	for _, keys := range [][]string{nil, {"p"}, {"esc"}} {
		for _, k := range keys {
			p.Update(keyMsg(k))
		}
		if !p.View().AltScreen {
			t.Errorf("after %v: not on the alternate screen", keys)
		}
	}
}

// On a terminal of ordinary width every key is on screen: the key line wraps
// over up to three lines rather than dropping + add, d delete, v move or T.
// Only a narrower one drops keys, and never enter or q.
func TestKeyLineWrapsBeforeDroppingKeys(t *testing.T) {
	p := newPicker(t)
	for _, w := range []int{80, 100, 200} {
		p.width = w
		h := p.hint()
		for _, want := range []string{"+ add", "d delete", "v move", "T trust repo", "enter launch", "q abort"} {
			if !strings.Contains(h, want) {
				t.Errorf("at %d columns the key line lost %q:\n%s", w, want, h)
			}
		}
		for _, line := range strings.Split(h, "\n") {
			if lipgloss.Width(line) > w {
				t.Errorf("at %d columns a key line is %d wide", w, lipgloss.Width(line))
			}
		}
		if n := strings.Count(h, "\n") + 1; n > 3 {
			t.Errorf("at %d columns the key line takes %d lines", w, n)
		}
	}
	p.width = 40
	if h := p.hint(); strings.Contains(h, "\n") || !strings.Contains(h, "enter launch") {
		t.Errorf("at 40 columns: %q", h)
	}
}

// firstRunPicker is a picker built as a first run: no saved selection, a
// user server disabled in Claude Code, a local one hidden here.
func firstRunPicker(t *testing.T, agent string) *picker {
	t.Helper()
	t.Setenv("MCPICK_HOME", t.TempDir())
	root := t.TempDir()
	if err := state.SaveHidden(root, map[string]bool{"linear": true}); err != nil {
		t.Fatal(err)
	}
	cat := sampleCatalog()
	cat.Servers[2].Disabled = true // notion, local
	cat.Servers = append(cat.Servers, catalog.Server{Name: "cf", Origin: catalog.OriginPlugin, Spec: httpSpec("https://cf/mcp")})
	p := build(cat, map[string]bool{}, Options{
		UID: "deploy-1", Target: mustTarget(t, agent), Argv: []string{agent}, Root: root, FirstRun: true,
		Profiles: profile.New(filepath.Join(t.TempDir(), "profiles.yaml")),
	})
	p.tr = newTrustState(trust.Open(filepath.Join(t.TempDir(), "trust.json")), root)
	p.width, p.height = 120, 40
	return p
}

// The first run in a project opens with what the agent would load without
// mcpick checked — the local, user and plugin servers, minus those
// disabled in Claude Code and those hidden here — and never a project
// server: Claude asks before loading those, and a tick is the consent.
// Nothing checked and a bare enter used to launch the agent with no
// servers at all.
func TestFirstRunStartsFromWhatTheAgentLoads(t *testing.T) {
	p := firstRunPicker(t, "claude")
	for name, want := range map[string]bool{
		"github": false, "local-tool": false, // project
		"notion":         false, // disabled in Claude Code
		"linear":         false, // hidden here
		"playwright-mcp": true, "sentry": true, "browser": true, "cf": true,
	} {
		if p.sel[name] != want {
			t.Errorf("%s checked = %v, want %v", name, p.sel[name], want)
		}
	}
	if !strings.Contains(plain(p.status), "first run") || !strings.Contains(plain(p.status), "claude") {
		t.Errorf("status = %q; the first run should be said", plain(p.status))
	}
	// A later run starts from the saved selection, whatever it holds.
	q := build(sampleCatalog(), map[string]bool{}, Options{UID: "deploy-1", Target: mustTarget(t, "claude"), Root: p.root,
		Profiles: profile.New(filepath.Join(t.TempDir(), "profiles.yaml"))})
	if len(q.sel) != 0 {
		t.Errorf("a run with a saved (empty) selection checked %v", q.sel)
	}
	// For an agent that does not read Claude's settings, a server disabled
	// there is an ordinary one, and loads.
	c := firstRunPicker(t, "codex")
	if !c.sel["notion"] || c.sel["linear"] || c.sel["github"] {
		t.Errorf("codex first run: sel = %v", c.sel)
	}
	// codex does not read ~/.claude.json: the status says whose servers
	// these are rather than claiming they are what codex loads.
	if got := plain(c.status); !strings.Contains(got, "checked the servers Claude Code loads (local, user, plugins)") {
		t.Errorf("codex first run status = %q", got)
	}
}

// With nothing checked the header says so, and what enter would do.
func TestNothingCheckedIsSaidInTheHeader(t *testing.T) {
	p := newPicker(t)
	p.width = 160
	if frame := plain(p.View().Content); strings.Contains(frame, "nothing checked") {
		t.Errorf("two servers are checked:\n%s", frame)
	}
	p.updateList("n")
	frame := plain(p.View().Content)
	if !strings.Contains(strings.SplitN(frame, "\n", 2)[0], "nothing checked — enter launches claude with no MCP servers") {
		t.Errorf("the header should say what enter does:\n%s", frame)
	}
	p.tgt, p.argv = mustTarget(t, ""), []string{"my-agent"}
	if frame := plain(p.View().Content); !strings.Contains(frame, "enter launches my-agent with no MCP servers") {
		t.Errorf("an unknown agent is named by its command:\n%s", frame)
	}
}

// The header leads with what matters: the agent, the count, the context
// total, the warnings, and only then the uid — when --uid was given — and
// the version, which a narrow terminal may cut.
func TestHeaderOrderAndUIDOnlyWhenGiven(t *testing.T) {
	p := newPicker(t)
	p.width = 160
	p.hidden = map[string]bool{"github": true}
	p.cache.Entries["github"] = mcp.Measurement{OK: true, Tokens: 4200, Spec: mcp.Fingerprint(p.cat.Servers[0].Spec)}
	head := strings.SplitN(plain(p.View().Content), "\n", 2)[0]
	want := []string{"✻ claude", "2/7 selected", "~4.2k+ context", "1 hidden checked", "uid=deploy-1", "mcpick 1.2.3"}
	last := -1
	for _, w := range want {
		i := strings.Index(head, w)
		if i < 0 {
			t.Fatalf("header lacks %q: %s", w, head)
		}
		if i < last {
			t.Errorf("%q comes before what should precede it: %s", w, head)
		}
		last = i
	}
	p.uidGiven = false
	if head := strings.SplitN(plain(p.View().Content), "\n", 2)[0]; strings.Contains(head, "uid=") {
		t.Errorf("the default uid is not worth a column: %s", head)
	}
}

// + with a name the catalog already has, in any group, is refused at the
// prompt with where it is; it used to duplicate the row and write over the
// file's entry.
func TestAddRefusesAnExistingName(t *testing.T) {
	p := newPicker(t)
	p.Update(keyMsg("+"))
	for _, k := range []string{"s", "e", "n", "t", "r", "y"} {
		p.Update(keyMsg(k))
	}
	p.Update(keyMsg("enter"))
	if p.mode != modeAddName || p.input != "sentry" {
		t.Fatalf("mode = %v input = %q; the prompt should stay open with the name", p.mode, p.input)
	}
	if plain(p.status) != "sentry already exists (user); pick another name" {
		t.Errorf("status = %q", plain(p.status))
	}
	if n := 0; true {
		for _, s := range p.cat.Servers {
			if s.Name == "sentry" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("sentry appears %d times", n)
		}
	}
}
