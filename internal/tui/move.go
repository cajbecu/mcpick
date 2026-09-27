package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/trust"
)

// v moves the server under the cursor to another group: a one-line chooser
// names the writable groups other than its own, and the first letter picks
// one. The files are edited by catalog.Move — destination first, then the
// source — and the row is re-placed under its new heading with the cursor
// on it and its check kept. A plugin server has no file mcpick may edit.
//
// Moving a server from ~/.claude.json into the project catalog is the one
// case that asks: that file usually sits in git, so credentials written in
// the spec are confirmed with a typed yes, or written as ${VAR} references
// with r, as `mcpick import` does.

type moveState struct {
	name  string   // the server being moved
	dests []string // where it may go, in catalog.Writable order
	to    string   // the group chosen, while the secrets question is open
	// redacted and vars are the spec with its credentials replaced and
	// the variables that then have to be exported, from spec.Redact.
	redacted map[string]any
	vars     []string
}

// moveKey is v: open the chooser for the server under the cursor.
func (p *picker) moveKey() {
	s, ok := p.current()
	if !ok {
		return
	}
	p.input = ""
	p.status = ""
	if s.Origin == catalog.OriginPlugin {
		p.status = trust.Safe(s.Name) + " comes from a plugin; plugin servers cannot be moved"
		return
	}
	p.mv = moveState{name: s.Name, dests: p.cat.Destinations(s.Name)}
	p.mode = modeMoveTo
}

// updateMoveTo is the chooser: the first letter of a group picks it, esc
// leaves.
func (p *picker) updateMoveTo(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "ctrl+c", "q":
		p.mode = modeList
		return p, nil
	}
	for _, d := range p.mv.dests {
		if k == d[:1] {
			p.chooseDestination(d)
			return p, nil
		}
	}
	return p, nil
}

// chooseDestination checks the move can be made, then asks about
// credentials when the destination is the project catalog, or moves.
func (p *picker) chooseDestination(to string) {
	s, ok := p.cat.Find(p.mv.name)
	if !ok {
		p.mode = modeList
		return
	}
	if err := p.cat.CanMove(s.Name, to); err != nil {
		p.status = styErr.Render("move refused: " + trust.Safe(err.Error()))
		p.mode = modeList
		return
	}
	if to == catalog.OriginProject && s.Origin != catalog.OriginProject {
		if redacted, vars := spec.Redact(s.Name, s.Spec); len(vars) > 0 {
			p.mv.to, p.mv.redacted, p.mv.vars = to, redacted, vars
			p.input = ""
			p.mode = modeMoveSecrets
			return
		}
	}
	p.doMove(to, s.Spec, nil)
}

// updateMoveSecrets is the credentials question: a typed yes writes them as
// they are, r writes ${VAR} references, esc cancels.
func (p *picker) updateMoveSecrets(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "ctrl+c":
		p.mode = modeList
		p.input = ""
		p.status = "cancelled"
	case "enter":
		switch p.input {
		case "yes":
			p.doMove(p.mv.to, p.cat.SpecOf(p.mv.name), nil)
		case "r":
			p.doMove(p.mv.to, p.mv.redacted, p.mv.vars)
		default:
			p.status = "cancelled"
			p.mode = modeList
		}
		p.input = ""
	case "backspace":
		p.input = dropLastRune(p.input)
	case "r":
		if p.input == "" {
			p.doMove(p.mv.to, p.mv.redacted, p.mv.vars)
			return p, nil
		}
		p.input += k
	default:
		if printable(k) && k != "space" {
			p.input += k
		}
	}
	return p, nil
}

// doMove edits the files and re-places the row. vars are the variables a
// redaction left to export. The status leads with the move and then the
// one thing the user has to do — export the variables before launching —
// so a narrow terminal cuts the notes after it, never the export.
func (p *picker) doMove(to string, sp map[string]any, vars []string) {
	p.mode = modeList
	var m catalog.Moved
	note, err := p.editCatalog(func() ([]string, error) {
		var err error
		if m, err = p.cat.Move(p.mv.name, to, sp); err != nil {
			return nil, err
		}
		p.placeMoved(m.Name, to, sp, m.ToFile)
		return []string{m.Name}, nil
	})
	if err != nil {
		p.status = styErr.Render("move failed: " + trust.Safe(err.Error()))
		return
	}
	status := m.Name + " → " + to
	if len(vars) > 0 {
		names := make([]string, 0, len(vars))
		for _, v := range vars {
			name, _, _ := strings.Cut(v, "=")
			names = append(names, name)
		}
		status += " · export " + strings.Join(names, ", ") + " before launching (the values are now only in the " + fsutil.ShortenHome(m.FromFile) + " backup)"
	}
	for _, n := range m.Notes() {
		status += " · " + n
	}
	p.status = trust.Safe(status) + note
}

// groupRank orders the groups as the list draws them.
func groupRank(origin string) int {
	switch origin {
	case catalog.OriginProject:
		return 0
	case catalog.OriginLocal:
		return 1
	case catalog.OriginUser:
		return 2
	}
	return 3
}

// placeMoved updates the row in place and puts it where a reload would: at
// the end of the Project group, since the catalog file appends, or in
// name order in Local and User, since ~/.claude.json's maps are read
// sorted. The cursor follows it; the selection is by name and needs nothing.
func (p *picker) placeMoved(name, to string, sp map[string]any, file string) {
	i := slices.IndexFunc(p.cat.Servers, func(s catalog.Server) bool { return s.Name == name })
	if i < 0 {
		return
	}
	s := p.cat.Servers[i]
	s.Origin, s.Source, s.Spec = to, file, sp
	rest := slices.Delete(slices.Clone(p.cat.Servers), i, i+1)
	at := len(rest)
	for j, o := range rest {
		r := groupRank(o.Origin)
		if r > groupRank(to) || (r == groupRank(to) && to != catalog.OriginProject && o.Name > name) {
			at = j
			break
		}
	}
	p.cat.Servers = slices.Insert(rest, at, s)
	p.cursor = at
}

// moveToLine is the chooser's prompt.
func (p *picker) moveToLine() string {
	parts := make([]string, 0, len(p.mv.dests)+1)
	for _, d := range p.mv.dests {
		parts = append(parts, styCursor.Render(d[:1])+" "+d)
	}
	parts = append(parts, styDim.Render("esc cancel"))
	line := fmt.Sprintf("move %q to: %s", trust.Safe(p.mv.name), strings.Join(parts, " · "))
	// Out of user, the server leaves ~/.claude.json's mcpServers, which
	// every project reads: said here, before the letter is pressed.
	if s, ok := p.cat.Find(p.mv.name); ok && s.Origin == catalog.OriginUser {
		line += " " + styWarn.Render("(removes it from ~/.claude.json: every project loses it)")
	}
	return line
}

// moveSecretsLines is the credentials question.
func (p *picker) moveSecretsLines() []string {
	return []string{
		styWarn.Render(fmt.Sprintf("%q holds credentials as written; they will be written into %s",
			trust.Safe(p.mv.name), fsutil.ShortenHome(p.cat.Path))),
		fmt.Sprintf("type 'yes' to write them as they are, r to write ${VAR} references instead, esc to cancel: %s", p.input),
	}
}
