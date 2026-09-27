package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
)

// longPicker has more servers than fit in a window of maxRows, spread over
// the three origins so headings scroll along with the rows.
func longPicker(t *testing.T, n, maxRows int) *picker {
	t.Helper()
	p := hidePicker(t)
	p.cat.Servers = nil
	p.sel = map[string]bool{}
	for i := 0; i < n; i++ {
		origin := []string{catalog.OriginProject, catalog.OriginLocal, catalog.OriginUser}[i*3/n]
		p.cat.Servers = append(p.cat.Servers, catalog.Server{
			Name: fmt.Sprintf("srv%02d", i), Origin: origin, Spec: httpSpec(fmt.Sprintf("https://x/%d", i))})
	}
	p.cursor = 0
	p.maxRows = maxRows
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return p
}

// The edge lines, as distinct from the "↑↓ move" in the key hints.
var (
	moreAboveRe = regexp.MustCompile(`↑ \d+ more`)
	moreBelowRe = regexp.MustCompile(`↓ \d+ more`)
)

// rows are the server rows drawn in a frame, in order.
func rows(frame string) []string {
	var out []string
	for _, line := range strings.Split(plain(frame), "\n") {
		if strings.Contains(line, "] srv") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func cursorInFrame(t *testing.T, p *picker) {
	t.Helper()
	name := p.cat.Servers[p.cursor].Name
	for _, r := range rows(p.View().Content) {
		if strings.HasPrefix(r, "> ") && strings.Contains(r, name) {
			return
		}
	}
	t.Fatalf("cursor row %s is not in view:\n%s", name, plain(p.View().Content))
}

// A long catalog must not push the legend and keys off the screen: the list
// is a window of max_rows, with the rest counted at the edge.
func TestListIsCappedAtMaxRows(t *testing.T) {
	p := longPicker(t, 15, 4)
	frame := plain(p.View().Content)
	if got := rows(frame); len(got) != 4 {
		t.Fatalf("%d rows in view, want max_rows = 4:\n%s", len(got), frame)
	}
	if !strings.Contains(frame, "↓ 11 more") || moreAboveRe.MatchString(frame) {
		t.Errorf("at the top, only the rows below are out of view:\n%s", frame)
	}
	cursorInFrame(t, p)
}

// The window follows the cursor, and the edge lines say how much is on
// each side, so the user never wonders whether the list ends there.
func TestWindowFollowsCursorWithEdgeCounts(t *testing.T) {
	p := longPicker(t, 15, 4)
	p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "↑ 11 more") || moreBelowRe.MatchString(frame) {
		t.Errorf("at the end, only the rows above are out of view:\n%s", frame)
	}
	cursorInFrame(t, p)
	for i := 0; i < 7; i++ {
		p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	frame = plain(p.View().Content)
	// Moving up puts the cursor on the window's first row: srv07..srv10.
	if !strings.Contains(frame, "↑ 7 more") || !strings.Contains(frame, "↓ 4 more") {
		t.Errorf("in the middle, both edges count:\n%s", frame)
	}
	cursorInFrame(t, p)
	if len(rows(frame)) != 4 {
		t.Errorf("%d rows in view while scrolled, want 4", len(rows(frame)))
	}
}

// Whatever key moved it, the cursor row is drawn: a cursor that scrolled out
// of view would make space, h and d act on a row the user cannot see.
func TestCursorIsAlwaysInView(t *testing.T) {
	p := longPicker(t, 23, 5)
	for _, k := range strings.Split("j j j j j j j pgdown pgdown j G k k pgup g j pgdown pgdown pgdown pgdown k k end home", " ") {
		p.key(k) // as Update would, with the key's name
		p.scroll()
		cursorInFrame(t, p)
	}
}

// A short terminal shows fewer rows than max_rows rather than a frame that
// does not fit; it still scrolls, and the cursor is still in view.
func TestShortTerminalShrinksTheWindow(t *testing.T) {
	p := longPicker(t, 15, 10)
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	frame := p.View().Content
	if lines := strings.Count(frame, "\n"); lines > 14 {
		t.Fatalf("frame is %d lines in a 14-line window:\n%s", lines, plain(frame))
	}
	if n := len(rows(frame)); n >= 10 || n < 1 {
		t.Errorf("%d rows in a 14-line window, want fewer than max_rows and at least one", n)
	}
	p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	cursorInFrame(t, p)
	if lines := strings.Count(p.View().Content, "\n"); lines > 14 {
		t.Errorf("frame is %d lines after scrolling to the end", lines)
	}
}

// The window is over what is in view: the filter's matches, and the hidden
// rows once that section is unfolded.
func TestScrollingCountsFilteredAndHiddenRows(t *testing.T) {
	p := longPicker(t, 15, 4)
	p.filter = "srv1" // srv10..srv14
	p.keepCursorVisible()
	p.scroll()
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "↓ 1 more") {
		t.Errorf("5 matches in a window of 4 should say 1 more:\n%s", frame)
	}
	p.filter = ""
	for i := 10; i < 15; i++ {
		p.hidden[fmt.Sprintf("srv%02d", i)] = true
	}
	p.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	frame = plain(p.View().Content)
	if !strings.Contains(frame, "Hidden (5)") || !strings.Contains(frame, "↑ 11 more") {
		t.Errorf("the last hidden row should be in view with 11 rows above:\n%s", frame)
	}
	cursorInFrame(t, p)
}

// A picker built without a config still gets a cap: a window the size of
// the terminal is exactly what max_rows exists to prevent.
func TestWindowRowsArithmetic(t *testing.T) {
	p := &picker{maxRows: 10}
	for _, tc := range []struct{ share, total, want int }{
		{30, 7, 10},  // room for all: the cap is not felt
		{30, 20, 10}, // scrolling: the cap holds, the edge lines fit in the share
		{5, 20, 3},   // short terminal: share minus the two edge lines
		{2, 20, 1},   // never nothing
		{5, 5, 5},    // fits exactly: no edge lines needed
	} {
		if got := p.windowRows(tc.share, tc.total); got != tc.want {
			t.Errorf("windowRows(%d, %d) = %d, want %d", tc.share, tc.total, got, tc.want)
		}
	}
}

// frameLines is how many terminal lines a frame takes: each "\n" ends one.
func frameLines(p *picker) int { return strings.Count(p.View().Content, "\n") }

// A window shorter than the frame around one row must not push lines off
// the top: the optional parts go (blank lines, legend, hints, headings), the
// header with its "hidden checked" warning and the cursor row stay. This is
// the reviewer's 100×10 case: 15 servers, one folded hidden, a status.
func TestTinyTerminalKeepsTheFrameInsideTheWindow(t *testing.T) {
	p := longPicker(t, 15, 10)
	p.hidden["srv07"] = true
	p.sel["srv07"] = true
	p.status = "hid srv07"
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 10})
	frame := p.View().Content
	if n := frameLines(p); n > 10 {
		t.Fatalf("%d lines in a 10-line window:\n%s", n, plain(frame))
	}
	cursorInFrame(t, p)
	head := strings.SplitN(plain(frame), "\n", 2)[0]
	if !strings.Contains(head, "1 hidden checked") {
		t.Errorf("the hidden-checked warning went with the header: %q", head)
	}
	if !strings.Contains(plain(frame), "hid srv07") {
		t.Error("the status line was dropped")
	}
}

// Whatever is on screen — folded or unfolded Hidden section, a filter, a
// status, a confirmation prompt — the frame fits any window of six lines or
// more, and the cursor row is drawn.
func TestFrameFitsSmallWindowsInEveryConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(p *picker)
	}{
		{"folded hidden", func(p *picker) {
			p.hidden["srv00"] = true
			p.hidden["srv09"] = true
			p.keepCursorVisible() // as loadHidden does after reading the set
		}},
		{"unfolded hidden, cursor inside", func(p *picker) {
			p.hidden["srv00"] = true
			p.hidden["srv09"] = true
			p.showHidden = true
			p.cursor = 0
		}},
		{"filter", func(p *picker) { p.filter = "srv1"; p.keepCursorVisible() }},
		{"typing a filter", func(p *picker) { p.mode = modeFilter; p.filter = "s" }},
		{"status", func(p *picker) { p.status = "measured" }},
		{"everything hidden", func(p *picker) {
			for _, s := range p.cat.Servers {
				p.hidden[s.Name] = true
			}
		}},
		{"delete prompt", func(p *picker) { p.mode = modeConfirmJSON; p.input = "ye" }},
	} {
		for height := 6; height <= 16; height++ {
			p := longPicker(t, 15, 10)
			tc.setup(p)
			p.Update(tea.WindowSizeMsg{Width: 100, Height: height})
			if n := frameLines(p); n > height {
				t.Errorf("%s at height %d: %d lines:\n%s", tc.name, height, n, plain(p.View().Content))
				continue
			}
			if len(p.visible()) > 0 {
				cursorInFrame(t, p)
			}
			p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"}) // scrolled to the end, both edges may show
			if n := frameLines(p); n > height {
				t.Errorf("%s at height %d after G: %d lines:\n%s", tc.name, height, n, plain(p.View().Content))
			}
		}
	}
}

// The full frame comes back as soon as there is room for it: the compact
// layout is for short windows only, not a new look.
func TestFullFrameOnARoomyTerminal(t *testing.T) {
	p := longPicker(t, 15, 10)
	l := p.layout()
	if !l.blanks || !l.legend || !l.hints || !l.headings {
		t.Errorf("layout at 40 lines = %+v, want everything drawn", l)
	}
	if !strings.Contains(plain(p.View().Content), "Legend:") {
		t.Error("no legend on a 40-line terminal")
	}
}
