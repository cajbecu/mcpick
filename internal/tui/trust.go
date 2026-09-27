package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/trust"
)

// Measuring a stdio server runs its command, and the catalog is not the
// user's alone: a cloned repository, a plugin or an old entry in
// ~/.claude.json can put any command there. So a bare m runs no command that
// has not been trusted; the row says "run?" and the detail line shows the
// command. A second m on the same row opens the review screen for that one
// command; M (review commands) opens it for every untrusted command. The
// screen shows each command whole — wrapped, scrollable, never cut — and y
// is the consent: it runs and trusts what was listed. Trust is per project
// and per command (see package trust), so a changed command asks again.
//
// The vocabulary, on the rows, the legend, the key line and the screens: a
// command waiting for m m is "run?"; a repository remote waiting for a tick
// or T is "ask", since measuring it would send the user's environment; M
// "reviews commands"; T "trusts the repo" — the repository's catalog — or
// "untrusts" it (decision #62).

type trustState struct {
	store *trust.Store
	root  string
	// skipped holds the rows the last measurement left out, with the reason,
	// so the list can mark them and the detail line can explain them.
	skipped map[string]trust.Verdict
	// armed is the row a first m was pressed on while it was skipped for
	// lack of trust: the next m on the same row opens the trust screen for
	// it. Any cursor move or mode change disarms, so a habitual double press
	// cannot open a question about a row the user never looked at.
	armed string
	// list is what the trust screen asks about: one server after m m, every
	// untrusted one after M. one tells the two apart on y, because m m
	// measures that row alone and M measures everything. offset is the
	// screen's scroll, in lines: a command can be longer than the window.
	list   []string
	one    bool
	offset int
	// catalog is where the project's catalog trust stands, over hash of
	// files — the catalog files as they were last refreshed (see
	// catalogtrust.go). offset is shared with the T screen.
	catalog trust.CatalogStatus
	hash    string
	files   []string
	// drift is set when a write of the picker's own found the catalog on
	// disk holding edits it did not make (resnapshot): the rows are no
	// longer what the files say, so until a reload the catalog counts as
	// changed and no trust is in force for it.
	drift bool
}

func newTrustState(store *trust.Store, root string) trustState {
	return trustState{store: store, root: root, skipped: map[string]trust.Verdict{}}
}

// measureCmd is a full press of m.
func (p *picker) measureCmd() tea.Cmd { return p.measureServers(nil) }

// skipNote is the tail of the status line after a measurement, saying how
// many servers were left out and what lifts each kind; changed says the
// catalog trust lapsed, which is why the unchecked ones are back.
func skipNote(untrusted, unchecked int, changed bool) string {
	var parts []string
	if untrusted > 0 {
		parts = append(parts, fmt.Sprintf("%d command(s) to review (m m, or M for all)", untrusted))
	}
	if unchecked > 0 {
		parts = append(parts, fmt.Sprintf("%d repository remote(s) to tick (or T trusts the repo)", unchecked))
	}
	if len(parts) == 0 {
		return ""
	}
	note := fmt.Sprintf(" · skipped %d: %s", untrusted+unchecked, strings.Join(parts, ", "))
	if changed {
		note = " · catalog changed since you trusted it; T to trust it again" + note
	}
	return note
}

// hiddenLeftOut is the note's word about hidden servers a press did not
// reach, "" for none.
func hiddenLeftOut(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf(" · %d hidden left out (H unfolds them)", n)
}

// measureNote is skipNote over the rows the last press left out, as
// tr.skipped stands now — for the status line once the measurement is
// done, so that "measured" keeps saying what it did not measure.
func (p *picker) measureNote() string {
	untrusted, unchecked, changed := 0, 0, false
	for _, v := range p.tr.skipped {
		if v.NeedsTrust {
			untrusted++
		} else {
			unchecked++
			changed = changed || v.CatalogChanged
		}
	}
	hidden := 0
	if !p.showHidden {
		for _, s := range p.cat.Servers {
			if p.hidden[s.Name] {
				hidden++
			}
		}
	}
	return skipNote(untrusted, unchecked, changed) + hiddenLeftOut(hidden)
}

// measureKey is m: a full measurement, or — pressed again on a row the last
// one skipped for lack of trust — the trust screen for that one command.
func (p *picker) measureKey() (tea.Model, tea.Cmd) {
	cur, ok := p.current()
	if ok && p.tr.armed == cur.Name && p.tr.skipped[cur.Name].NeedsTrust {
		p.tr.armed = ""
		p.openTrustScreen([]string{cur.Name}, true)
		return p, nil
	}
	p.tr.armed = ""
	cmd := p.measureServers(nil)
	if ok && p.tr.skipped[cur.Name].NeedsTrust {
		p.tr.armed = cur.Name
	}
	return p, cmd
}

// trustOne grants and measures one server. A store that cannot be saved is
// reported, and the measurement still runs: the consent was just given, and
// refusing it would only make the user press again.
func (p *picker) trustOne(s catalog.Server) tea.Cmd {
	p.tr.store.Grant(p.tr.root, s.Name, s.Spec)
	delete(p.tr.skipped, s.Name)
	cmd := p.measureServers(map[string]bool{s.Name: true})
	if err := p.tr.store.Save(); err != nil {
		p.status = styErr.Render("could not save trust (" + trust.Safe(err.Error()) + "); measuring " + trust.Safe(s.Name) + " anyway")
	} else {
		p.status = "trusted " + trust.Safe(s.Name) + " for " + fsutil.ShortenHome(p.tr.root) + " · " + p.status
	}
	return cmd
}

// untrusted lists every server a measurement would skip for lack of trust,
// checked or not: the M screen is about commands, not the selection. Hidden
// servers are left out while their section is folded, as m leaves them out.
func (p *picker) untrusted() []string {
	var names []string
	for _, s := range p.cat.Servers {
		if p.measurable(s) && trust.Gate(p.scope(), s, p.sel[s.Name]).NeedsTrust {
			names = append(names, s.Name)
		}
	}
	return names
}

// measureAllKey is M, review commands: show every command that is not yet
// trusted and ask once for all of them. With nothing to ask, it is a plain
// measurement.
func (p *picker) measureAllKey() (tea.Model, tea.Cmd) {
	p.tr.armed = ""
	p.refreshCatalog()
	names := p.untrusted()
	if len(names) == 0 {
		cmd := p.measureServers(nil)
		p.status = "no commands to review · " + p.status
		return p, cmd
	}
	p.openTrustScreen(names, false)
	return p, nil
}

// openTrustScreen switches to the trust screen for names, scrolled to the
// top; one says a single m m row is being asked about.
func (p *picker) openTrustScreen(names []string, one bool) {
	p.mode = modeConfirmTrust
	p.tr.list, p.tr.one, p.tr.offset = names, one, 0
}

// updateConfirmTrust is the trust screen: y grants everything listed and
// measures; the arrows scroll; any other key keeps skipping. q cancels the
// screen rather than the picker, because the user is inside a question.
func (p *picker) updateConfirmTrust(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "y", "Y":
		p.mode = modeList
		if p.tr.one {
			if s, ok := p.cat.Find(p.tr.list[0]); ok {
				return p, p.trustOne(s)
			}
			return p, nil
		}
		for _, n := range p.tr.list {
			if s, ok := p.cat.Find(n); ok {
				p.tr.store.Grant(p.tr.root, n, s.Spec)
				delete(p.tr.skipped, n)
			}
		}
		err := p.tr.store.Save()
		cmd := p.measureServers(nil)
		if err != nil {
			p.status = styErr.Render(fmt.Sprintf("could not save trust (%s); measuring anyway", trust.Safe(err.Error())))
		} else {
			p.status = fmt.Sprintf("trusted %d command(s) for %s · %s",
				len(p.tr.list), fsutil.ShortenHome(p.tr.root), p.status)
		}
		return p, cmd
	case "up", "k":
		p.tr.offset--
	case "down", "j":
		p.tr.offset++
	case "pgup", "ctrl+u":
		p.tr.offset -= p.trustRows()
	case "pgdown", "ctrl+d":
		p.tr.offset += p.trustRows()
	default:
		p.mode = modeList
		p.status = "nothing trusted"
		return p, nil
	}
	p.scrollTrust()
	return p, nil
}

// trustRows is how many lines the trust screen can show at once: the window
// minus the title, the two notes, the hint and the scroll line.
func (p *picker) trustRows() int {
	return max(1, p.height-6)
}

// scrollTrust keeps the M and T screens' offset inside their lines. It
// runs after every message while one of them is up, the window's resize
// included: a wider window wraps a command over fewer lines, and an offset
// scrolled to the end of the old ones pointed past the new — a slice
// bounds panic on the next draw.
func (p *picker) scrollTrust() {
	lines := p.trustLines()
	if p.mode == modeConfirmCatalog {
		lines = p.catalogLines()
	}
	p.tr.offset = max(0, min(p.tr.offset, len(lines)-p.trustRows()))
}

// trustWindow is the slice of n lines a trust screen draws, from the offset
// clamped into range, so that a draw never reaches past the lines it has.
func (p *picker) trustWindow(n int) (start, end int) {
	start = max(0, min(p.tr.offset, n))
	return start, min(start+p.trustRows(), n)
}

// trustLines is the body of the trust screen: for each server a line with
// its name and origin, then its command wrapped to the window, whole. Nothing
// here is truncated — the point of the screen is that the user sees all of
// what y would run — and nothing here is masked: this is the user's own
// terminal, and consent needs the real arguments and env values (as the
// catalog wrote them, `${VAR}` unexpanded). Nothing here is raw catalog text
// either: names and commands go through trust.Safe, so an escape in an
// argument is shown, not obeyed.
func (p *picker) trustLines() []string {
	width := max(20, p.width)
	var lines []string
	for _, n := range p.tr.list {
		s, _ := p.cat.Find(n)
		lines = append(lines, "  "+truncate(trust.Safe(n), width-14)+"  "+styDim.Render(s.Origin))
		for _, l := range wrap(trust.Describe(s.Spec, trust.MaskNone), width-6) {
			lines = append(lines, "      "+l)
		}
	}
	return lines
}

// wrap breaks s into lines of at most width cells — display width, not
// runes: a wide character takes two, and Bubble Tea clips a line at the
// window's edge, where the tail of a command would vanish — at a space when
// there is one and inside a word otherwise, so the whole of s is returned.
func wrap(s string, width int) []string {
	width = max(1, width)
	r := []rune(s)
	var out []string
	for len(r) > 0 {
		// fit is how many runes fill the width; a rune wider than what is
		// left still goes on a line of its own, so nothing is dropped.
		fit, cells := 0, 0
		for fit < len(r) {
			w := lipgloss.Width(string(r[fit]))
			if cells+w > width && fit > 0 {
				break
			}
			cells += w
			fit++
		}
		if fit == len(r) {
			out = append(out, string(r))
			break
		}
		cut := fit
		for i := fit; i > 0; i-- {
			if r[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
		if len(r) > 0 && r[0] == ' ' {
			r = r[1:]
		}
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// trustScreen is the trust screen, drawn instead of the list. Commands and
// environment are shown as the catalog wrote them, values included and
// unmasked: `${VAR}` is exactly what the author wrote, and a literal in the
// catalog is what would run.
func (p *picker) trustScreen() string {
	var b strings.Builder
	width := max(20, p.width)
	title := fmt.Sprintf("Run and trust %d command(s)?", len(p.tr.list))
	if p.tr.one {
		title = "Run and trust this command?"
	}
	fmt.Fprintf(&b, "%s  %s\n", styTitle.Render(title),
		styDim.Render(truncate("It runs now and on every later measurement in "+fsutil.ShortenHome(p.tr.root)+".", width-len(title)-2)))

	lines := p.trustLines()
	rows := p.trustRows()
	start, end := p.trustWindow(len(lines))
	for _, l := range lines[start:end] {
		fmt.Fprintf(&b, "%s\n", l)
	}
	if len(lines) > rows {
		fmt.Fprintf(&b, "%s\n", styDim.Render(fmt.Sprintf("  … lines %d-%d of %d", start+1, end, len(lines))))
	}

	b.WriteString(styDim.Render(truncate("Launching is unchanged: it runs whatever is ticked.", width)) + "\n")
	if n := p.uncheckedRemotes(); n > 0 && !p.tr.one {
		b.WriteString(styDim.Render(truncate(fmt.Sprintf(
			"%d remote server(s) from this repository's catalog stay unmeasured until ticked, or until T trusts the repo.", n), width)) + "\n")
	}
	b.WriteString(styDim.Render(truncate("y run and trust · esc keep skipping · ↑↓ scroll", width)) + "\n")
	return b.String()
}

// uncheckedRemotes counts the servers the M screen does not cover: remote
// ones from the repository's catalog, which need a check (or T) rather
// than trust in a command.
func (p *picker) uncheckedRemotes() int {
	n := 0
	for _, s := range p.cat.Servers {
		if v := trust.Gate(p.scope(), s, p.sel[s.Name]); p.measurable(s) && !v.Run && !v.NeedsTrust {
			n++
		}
	}
	return n
}

// skipMark is the word in the cost column of a row the last measurement left
// out, one per reason: "run?" for a command not trusted yet, "ask" for a
// server of this repository's own catalog that is measured only once ticked
// (or the catalog trusted), since measuring it would send the user's
// environment. They used to share "skipped", which read as if every
// repository server needed trust; then "trust?" and "check", where T did
// not trust the "trust?" rows and "check" had three meanings.
func skipMark(v trust.Verdict) string {
	if v.NeedsTrust {
		return styWarn.Render("   run?")
	}
	return styDim.Render("    ask")
}

// skipDetail explains a row the last measurement left out, and what lifts
// it. The how comes before the command, so a long command loses its tail to
// the width and never the hint; the whole command is on the trust screen.
func (p *picker) skipDetail(s catalog.Server) string {
	v, ok := p.tr.skipped[s.Name]
	if !ok {
		return ""
	}
	width := max(20, p.width)
	name := trust.Safe(s.Name)
	if !v.NeedsTrust {
		// What lifts it first — a lapsed catalog trust is the one thing the
		// user most likely wants to know — then the condition that keeps
		// this repository server out, which a narrow terminal may cut.
		how := "tick it (space), or T to trust the repo"
		if v.CatalogChanged {
			how = "catalog changed since you trusted it; T to trust it again"
		}
		return styDim.Render(truncate(name+": "+how+" · repository server: "+v.Exposure, width))
	}
	how := "m m to review and run it, M for all"
	if p.tr.armed == s.Name {
		how = "press m again to review and run it"
	}
	return styWarn.Render(truncate(name+": skipped — "+how+" · "+v.Reason, width))
}

// armedDetail is the detail line while a second m is pending on the row
// under the cursor: what the second m is about has to be on screen between
// the two presses, whatever the status line says.
func (p *picker) armedDetail() string {
	s, ok := p.current()
	if !ok || p.tr.armed != s.Name {
		return ""
	}
	return p.skipDetail(s)
}

// afterMove runs after every message with the cursor and mode it had before.
// Leaving the row or the list disarms a pending m. Moving in the list also
// clears the status line, which is otherwise sticky after a measurement and
// would hide the row's own detail line — the skipped command, the failure —
// for good.
func (p *picker) afterMove(cursor int, mode mode) {
	if p.cursor != cursor && mode == modeList && p.mode == modeList {
		p.status = ""
	}
	if p.cursor != cursor || p.mode != mode {
		p.tr.armed = ""
	}
}
