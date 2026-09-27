package tui

import (
	"fmt"
	"strings"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/trust"
)

// Hidden servers (h) are the ones never used here: they leave the origin
// groups for a "Hidden (N)" section at the bottom, collapsed unless H expands
// it, and `a` leaves them alone. Hiding is not unchecking — a checked server
// stays checked, and the header says so — because a launch must never lose a
// server without the user seeing it go.

// sectionHidden is the heading hidden rows are drawn under, in place of
// their origin.
const sectionHidden = "hidden"

// section is the heading a row belongs to: its origin while active, the
// Hidden section once hidden.
func (p *picker) section(s catalog.Server) string {
	if p.hidden[s.Name] {
		return sectionHidden
	}
	return s.Origin
}

// matches reports whether a row passes the filter, hidden or not.
func (p *picker) matches(s catalog.Server) bool {
	needle := strings.ToLower(p.filter)
	return needle == "" ||
		strings.Contains(strings.ToLower(s.Name), needle) ||
		strings.Contains(strings.ToLower(s.Endpoint()), needle)
}

// measurable says whether a measurement (m, M) may touch a server: any
// active one, and a hidden one only while the Hidden section is unfolded.
// Hidden means not wanted here, and a hidden stdio server's command should
// not run from a key pressed with its row folded out of sight.
func (p *picker) measurable(s catalog.Server) bool {
	return !p.hidden[s.Name] || p.showHidden
}

// hiddenCount is how many hidden servers pass the filter: the N in the
// section heading.
func (p *picker) hiddenCount() int {
	n := 0
	for _, s := range p.cat.Servers {
		if p.hidden[s.Name] && p.matches(s) {
			n++
		}
	}
	return n
}

// hiddenNote is the header's word about hidden servers that will still
// load, or "" when none is checked.
func (p *picker) hiddenNote() string {
	n := 0
	for _, s := range p.cat.Servers {
		if p.hidden[s.Name] && p.sel[s.Name] {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d hidden checked", n)
}

// hiddenHeading is the section's heading line; it doubles as the reminder
// that rows are folded away when the section is collapsed.
func (p *picker) hiddenHeading() string {
	hint := "H expand"
	if p.showHidden {
		hint = "H collapse"
	}
	return fmt.Sprintf("%s %s", styGroup.Render(fmt.Sprintf("Hidden (%d)", p.hiddenCount())),
		styDim.Render("• h unhide · "+hint))
}

// hiddenTag says where a hidden row came from, since it no longer sits
// under its origin's heading.
func (p *picker) hiddenTag(s catalog.Server) string {
	if !p.hidden[s.Name] {
		return ""
	}
	return styDim.Render(strings.ToLower(p.groupLabel(s.Origin)) + "  ")
}

// loadHidden reads the workspace's hidden set when the picker opens. An
// unreadable set is not worth failing a launch: it shows as nothing hidden,
// and the next h writes a fresh one. The cursor then moves off a row that
// just folded away, or space and enter would act on a row nobody can see.
func (p *picker) loadHidden() {
	hidden, err := state.LoadHidden(p.root)
	if err != nil {
		hidden = map[string]bool{}
	}
	p.hidden = hidden
	p.keepCursorVisible()
}

// hideKey hides the row under the cursor, or unhides it when it is already
// hidden, and saves that one change right away: the picker may be quit with
// q. The set comes back as it stands on disk, so a hide made meanwhile by
// another picker on the same project shows up here too.
func (p *picker) hideKey() {
	s, ok := p.current()
	if !ok {
		return
	}
	if p.hidden == nil {
		p.hidden = map[string]bool{}
	}
	hide := !p.hidden[s.Name]
	// The name is the catalog's: spelled out before the status line draws it.
	if hide {
		p.status = "hid " + trust.Safe(s.Name)
		if p.sel[s.Name] {
			p.status += " (still checked: it loads until you uncheck it)"
		}
	} else {
		p.status = "unhid " + trust.Safe(s.Name)
	}
	if set, err := state.SetHidden(p.root, s.Name, hide); err != nil {
		// The screen still does what the key said; only the saving failed.
		if hide {
			p.hidden[s.Name] = true
		} else {
			delete(p.hidden, s.Name)
		}
		p.status = styErr.Render("hidden set not saved: " + trust.Safe(err.Error()))
	} else {
		p.hidden = set
	}
	p.cursorToNearest()
}

// toggleHiddenSection folds or unfolds the Hidden section.
func (p *picker) toggleHiddenSection() {
	if p.hiddenCount() == 0 && !p.showHidden {
		p.status = "nothing hidden (h hides the server under the cursor)"
		return
	}
	p.showHidden = !p.showHidden
	p.cursorToNearest()
}

// cursorToNearest keeps the cursor on a row in view after the row it was on
// moved out: the next one down, else the last one. Unlike keepCursorVisible
// it does not jump to the top, so hiding rows one after another reads on.
func (p *picker) cursorToNearest() {
	vis := p.visible()
	if len(vis) == 0 {
		return
	}
	next := -1
	for _, i := range vis {
		if i == p.cursor {
			return
		}
		// Hidden rows follow the active ones, so vis is not in catalog
		// order: the nearest is the smallest index past the cursor.
		if i > p.cursor && (next < 0 || i < next) {
			next = i
		}
	}
	if next < 0 {
		next = vis[len(vis)-1]
	}
	p.cursor = next
}
