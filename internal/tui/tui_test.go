package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/target"
)

func httpSpec(u string) map[string]any {
	return map[string]any{"type": "http", "url": u}
}

func sampleCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Path:       "/workspace/.mcp.yaml",
		ClaudePath: "/home/agent/.claude.json",
		ProjectKey: "/workspace",
		Profiles:   map[string][]string{"review": {"ahrefs", "freshdesk"}},
		Servers: []catalog.Server{
			{Name: "ahrefs", Origin: catalog.OriginWorkspace, Spec: httpSpec("https://api.ahrefs.com/mcp/mcp")},
			{Name: "local-tool", Origin: catalog.OriginWorkspace, Spec: map[string]any{
				"type": "stdio", "command": "uvx",
				"args": []any{"some-mcp", "--session", "{UUID}"}}},
			{Name: "claude_design", Origin: catalog.OriginProject, Spec: httpSpec("https://api.anthropic.com/v1/design/mcp")},
			{Name: "google-webmaster", Origin: catalog.OriginProject, Spec: httpSpec("https://mcp.example.com/google-webmaster")},
			{Name: "playwright-mcp", Origin: catalog.OriginProject, Spec: httpSpec("http://playwright-mcp:8931/mcp")},
			{Name: "freshdesk", Origin: catalog.OriginGlobal, Spec: httpSpec("https://mcp.example.com/freshdesk")},
			{Name: "neo", Origin: catalog.OriginGlobal, Spec: httpSpec("http://127.0.0.1:9010/mcp?session={UUID}")},
		},
	}
}

func newPicker(t *testing.T) *picker {
	t.Helper()
	return &picker{
		cat:       sampleCatalog(),
		sel:       map[string]bool{"ahrefs": true, "playwright-mcp": true},
		uid:       "deploy-1",
		tgt:       mustTarget(t, "claude"),
		argv:      []string{"claude", "--dangerously-skip-permissions"},
		version:   "1.2.3",
		cursor:    2,
		width:     100,
		height:    40,
		cache:     &mcp.Cache{Entries: map[string]mcp.Measurement{}},
		measuring: map[string]bool{},
	}
}

func TestRenderFrame(t *testing.T) {
	p := newPicker(t)
	frame := p.View().Content
	if frame == "" {
		t.Fatal("empty frame")
	}
	for _, want := range []string{"mcpick 1.2.3", "Workspace", "Project", "Global", "ahrefs", "deploy-1"} {
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
		{catalog.OriginWorkspace, 1, 2},
		{catalog.OriginProject, 1, 3},
		{catalog.OriginGlobal, 0, 2},
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
	p.filter = "goog"
	vis := p.visible()
	if len(vis) != 1 || p.cat.Servers[vis[0]].Name != "google-webmaster" {
		t.Fatalf("filter returned %d rows, want google-webmaster only", len(vis))
	}

	// A filter on the target string matches too.
	p.filter = "127.0.0.1"
	vis = p.visible()
	if len(vis) != 1 || p.cat.Servers[vis[0]].Name != "neo" {
		t.Fatalf("target filter returned %d rows, want neo", len(vis))
	}
}

func TestFilteredAllTouchesOnlyVisible(t *testing.T) {
	p := newPicker(t)
	p.sel = map[string]bool{}
	p.filter = "playwright"
	p.updateList("a")
	if p.sel["ahrefs"] {
		t.Error("ahrefs is filtered out but got selected by 'a'")
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
	p.cache.Entries["ahrefs"] = mcp.Measurement{Tokens: 4200, OK: true, Spec: mcp.Fingerprint(spec)}
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
	frame := plain(p.View().Content)
	for _, want := range []string{"Legend:", "[x]", "context cost", "check failed"} {
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

func mustTarget(t *testing.T, name string) target.Target {
	t.Helper()
	tgt, err := target.Pick(name, nil)
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
	p.sel = map[string]bool{"ahrefs": true}
	if !strings.Contains(p.commandLine(), "--allowed-mcp-server-names ahrefs") {
		t.Errorf("command = %q", p.commandLine())
	}
	p.sel["freshdesk"] = true
	if !strings.Contains(p.commandLine(), "--allowed-mcp-server-names ahrefs,freshdesk") {
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
	p.sel = map[string]bool{"ahrefs": true} // workspace, checked
	p.measureCmd()
	if !p.measuring["ahrefs"] {
		t.Error("a checked workspace server should be measured")
	}
	if p.measuring["local-tool"] {
		t.Error("an unchecked workspace server must not be run")
	}
	if !p.measuring["freshdesk"] {
		t.Error("a server from the user's own config should be measured")
	}
	if !strings.Contains(p.status, "skipped") {
		t.Errorf("status = %q; the user should know some were skipped and why", p.status)
	}
}
