package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/config"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/trust"
)

// Where the features meet: hiding (h) against profiles, scrolling and trust.

// The built-in default profile is what --all selects, and --all skips hidden
// servers; the screen's default must say the same, and a hidden server that a
// profile does name is tagged, so a checked box there is not a surprise.
func TestDefaultProfileSkipsHiddenAndTagsThem(t *testing.T) {
	p := hidePicker(t)
	p.hidden["sentry"] = true
	p.press("p")
	rows := p.profileRows()
	if rows[0].kind != rowDefault {
		t.Fatalf("first row = %+v, want default", rows[0])
	}
	if contains(rows[0].servers, "sentry") {
		t.Errorf("default lists a hidden server: %v", rows[0].servers)
	}
	if !contains(rows[0].servers, "browser") {
		t.Errorf("default lost an active server: %v", rows[0].servers)
	}
	p.press("tab")
	frame := plain(p.View().Content)
	found := false
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "sentry") && strings.Contains(line, "hidden") {
			found = true
		}
	}
	if !found {
		t.Errorf("the hidden server's row in the right pane should say so:\n%s", frame)
	}
}

// config.yaml's max_rows bounds the profile screen's panes as it bounds the
// list: the left pane pages that many profiles, the right that many servers
// under their headings, whatever the terminal's height.
func TestProfilePanesRespectMaxRows(t *testing.T) {
	p := profilesPicker(t)
	p.height = 60
	p.press("p")
	before := p.profilesLayout()
	p.maxRows = 3
	after := p.profilesLayout()
	if after.hL != 3 {
		t.Errorf("left pane = %d rows with max_rows 3 (was %d)", after.hL, before.hL)
	}
	if want := 3 + p.rightHeadings(); after.hR != want {
		t.Errorf("right pane = %d rows, want %d (3 servers + %d headings; was %d)", after.hR, want, p.rightHeadings(), before.hR)
	}
	if lines := strings.Count(p.View().Content, "\n"); lines > p.height {
		t.Errorf("frame is %d lines in a %d-line window", lines, p.height)
	}
}

// m and M leave hidden servers alone while their section is folded: a
// hidden stdio server's command must not run from a key pressed with its row
// out of sight, and the M screen must not ask about it. Unfolding the section
// (H) puts them back in reach of both.
func TestMeasureLeavesHiddenServersAloneUntilUnfolded(t *testing.T) {
	p := hidePicker(t)
	p.hidden["sentry"] = true
	p.hidden["local-tool"] = true // a stdio command, untrusted
	press(p, "m")
	if p.measuring["sentry"] {
		t.Error("a hidden remote server was measured while folded")
	}
	if _, skipped := p.tr.skipped["local-tool"]; skipped {
		t.Error("a hidden command was gated, so it was considered for running")
	}
	if !p.measuring["github"] {
		t.Error("an active server was not measured")
	}
	if !strings.Contains(p.status, "2 hidden left out") {
		t.Errorf("status = %q; the user should know hidden servers were skipped", p.status)
	}
	if names := p.untrusted(); contains(names, "local-tool") {
		t.Errorf("M would ask about a hidden command: %v", names)
	}
	press(p, "H") // unfold: hidden rows are in view and measured with the rest
	press(p, "m")
	if !p.measuring["sentry"] {
		t.Error("an unfolded hidden server was not measured")
	}
	if v, ok := p.tr.skipped["local-tool"]; !ok || !v.NeedsTrust {
		t.Error("an unfolded hidden command should be gated like any other")
	}
	if names := p.untrusted(); !contains(names, "local-tool") {
		t.Errorf("M should now ask about the unfolded command: %v", names)
	}
}

// The key line fits the terminal at every width and always keeps how to
// launch and abort, dropping the least needed keys first rather than being
// cut wherever the width falls.
func TestHintFitsAndKeepsLaunchKeys(t *testing.T) {
	p := newPicker(t)
	for _, width := range []int{160, 120, 100, 80, 60, 40, 25} {
		p.width = width
		line := p.hint()
		if w := lipgloss.Width(line); w > width {
			t.Errorf("hint is %d wide in a %d-column terminal: %s", w, width, line)
		}
		for _, want := range []string{"enter launch", "q abort"} {
			if !strings.Contains(line, want) {
				t.Errorf("at %d columns the hint lost %q: %s", width, want, line)
			}
		}
	}
	p.width = 160
	if line := p.hint(); !strings.Contains(line, "M review commands") || !strings.Contains(line, "H hidden") || !strings.Contains(line, "p profiles") {
		t.Errorf("a wide terminal should show every key: %s", line)
	}
	p.width = 80
	if line := p.hint(); !strings.Contains(line, "space toggle") || !strings.Contains(line, "m measure") {
		t.Errorf("at 80 columns the hint should still teach the main keys: %s", line)
	}
}

// At 80 columns — the common width — the legend keeps every mark, in the
// short wording, rather than dropping the ones that puzzle most (skipped,
// measuring) to keep long words for the rest.
func TestLegendKeepsEveryMarkAtEightyColumns(t *testing.T) {
	p := newPicker(t)
	p.width = 80
	p.Update(measuredMsg{name: "github", spec: p.cat.Servers[0].Spec, res: mcp.Result{Err: "401 Unauthorized"}})
	p.measuring[p.cat.Servers[2].Name] = true
	p.tr.skipped[p.cat.Servers[1].Name] = trust.Verdict{NeedsTrust: true}
	line := plain(p.legend())
	for _, want := range []string{"401", "4.2k", "[x]", "run?", "···"} {
		if !strings.Contains(line, want) {
			t.Errorf("at 80 columns the legend lost %q: %s", want, line)
		}
	}
	if w := lipgloss.Width(line); w > 80 {
		t.Errorf("legend is %d wide", w)
	}
}

// With max_rows the right pane is taller than the left (its headings sit
// on top of that many server rows), and the side-by-side drawing must draw
// it whole: at the production default of ten, End on a thirty-server
// catalog puts the cursor on the last row, and that row and the "… a-b of
// n" line are on the screen, not cut where the left pane stops.
func TestRightPaneIsDrawnPastTheLeftPanesHeight(t *testing.T) {
	p := newPicker(t)
	p.width, p.height = 100, 40
	p.maxRows = config.DefaultMaxRows
	p.cat.Servers = nil
	origins := []string{catalog.OriginProject, catalog.OriginLocal, catalog.OriginUser}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("srv%02d", i)
		p.cat.Servers = append(p.cat.Servers, catalog.Server{Name: name, Origin: origins[i/10], Spec: httpSpec("https://x/" + name)})
	}
	p.press("p", "tab", "end")
	if p.pm.pane != 1 || p.pm.srv != 29 {
		t.Fatalf("pane %d, cursor on row %d; want the right pane's last row", p.pm.pane, p.pm.srv)
	}
	layout := p.profilesLayout()
	if layout.hR <= layout.hL {
		t.Fatalf("hL %d, hR %d: the test needs a right pane taller than the left", layout.hL, layout.hR)
	}
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "> [x] srv29") {
		t.Errorf("the cursor row is not drawn:\n%s", frame)
	}
	if !strings.Contains(frame, "of 33") {
		t.Errorf("the right pane's scroll indicator is not drawn:\n%s", frame)
	}
	if lines := strings.Count(p.View().Content, "\n"); lines > p.height {
		t.Errorf("frame is %d lines in a %d-line window", lines, p.height)
	}
}

// The profile screen shows the catalog's and the profiles file's text as
// text, as the list and the trust screen do: an escape in a server name, an
// address, a profile name or a name a profile lists that this project lacks
// is spelled out, and no cell reaches the terminal concealed.
func TestProfileScreenNeutralisesTerminalEscapes(t *testing.T) {
	p := profilesPicker(t)
	p.cat.Servers[0].Name = "safe\x1b[8m-evil\x1b[0m"
	p.cat.Servers[0].Spec = httpSpec("https://x/\x1b[8mconcealed\x1b[0m")
	p.cat.Profiles["cat\x1b[8malog"] = []string{"safe\x1b[8m-evil\x1b[0m"}
	// Such a name fails the name rule now; one stored before it still loads.
	p.profiles.Profiles = append(p.profiles.Profiles, profile.Profile{Name: "pro\x1b[8mfile", Servers: []string{"gh\x1b[8most"}})
	if err := p.profiles.Save(); err != nil {
		t.Fatal(err)
	}
	p.press("p")
	frames := map[string]string{}
	frames["left pane"] = p.View().Content
	p.press("j", "j") // the personal profile that lists the missing name
	if got := p.currentProfile().name; got != "pro\x1b[8mfile" {
		t.Fatalf("cursor on %q", got)
	}
	p.press("tab", "G")
	frames["missing row"] = p.View().Content
	p.press("g")
	frames["server row"] = p.View().Content
	for which, frame := range frames {
		if strings.Contains(frame, "\x1b[8m") {
			t.Errorf("%s: a conceal escape reached the frame:\n%q", which, frame)
		}
		if !strings.Contains(frame, `\x1b[8m`) {
			t.Errorf("%s: the escape should be spelled out:\n%s", which, plain(frame))
		}
		buf := uv.NewScreenBuffer(p.width, p.height)
		uv.NewStyledString(frame).Draw(buf, buf.Bounds())
		for y := 0; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); x++ {
				if c := buf.CellAt(x, y); c != nil && c.Style.Attrs&uv.AttrConceal != 0 {
					t.Errorf("%s: cell %d,%d is concealed on the terminal", which, x, y)
				}
			}
		}
		screen := plain(buf.Render())
		for _, want := range []string{"-evil", `pro\x1b[8mfile`, `cat\x1b[8malog`} {
			if !strings.Contains(screen, want) {
				t.Errorf("%s: %q is not on the terminal:\n%s", which, want, screen)
			}
		}
	}
	if screen := plain(frames["missing row"]); !strings.Contains(screen, `gh\x1b[8most`) {
		t.Errorf("the missing name should be spelled out:\n%s", screen)
	}
	if screen := plain(frames["server row"]); !strings.Contains(screen, `\x1b[8mconcealed`) {
		t.Errorf("the address should be spelled out:\n%s", screen)
	}
}
