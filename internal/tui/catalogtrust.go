package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/trust"
)

// T trusts the repo — the project's own catalog: once given, m measures
// every remote server it defines — placeholders and private hosts included,
// because the user has seen the files and said so — where a bare m
// otherwise measures only the harmless ones (see trust.Exposure) and leaves
// the rest to a tick. The screen lists the catalog files and each remote
// server the trust would cover; y records it, keyed on the files' contents,
// so an edit lapses it and T asks again. Stdio commands are not covered:
// each keeps its own grant (m m, M). T on a trusted catalog offers to
// untrust it, and the key line says which (trustKey).

// refreshCatalog records where the project's catalog trust stands: the
// store's record against the hash of the catalog as the picker loaded it —
// the bytes its rows were parsed from, not the files as they are on disk
// now. A trust covers what is on screen, and the screen is the loaded
// catalog; an edit on disk while the picker is open is caught by T
// (catalogOnDisk), which refuses to trust what was not shown. Called at the
// entry points that depend on it — m, M, T, and the picker's start — rather
// than on every frame. Once a write of the picker's own has found edits on
// disk it did not make (drift), a trust held counts as changed until a
// reload.
func (p *picker) refreshCatalog() {
	p.tr.hash, p.tr.files = trust.CatalogHash(p.cat.Files)
	p.tr.catalog = p.tr.store.Catalog(p.tr.root, p.tr.hash)
	if p.tr.drift && p.tr.catalog == trust.CatalogTrusted {
		p.tr.catalog = trust.CatalogChanged
	}
}

// catalogOnDisk says whether the catalog files on disk are still the bytes
// the picker parsed. T records a trust keyed on those bytes and shows the
// servers parsed from them; if the files have changed since, the screen
// would approve a catalog the user has not seen, so T refuses and asks for
// a reload. A file that cannot be read now counts as changed.
func (p *picker) catalogOnDisk() bool {
	now, err := catalog.Snapshot(p.cat.Path, p.tr.root)
	if err != nil {
		return false
	}
	hash, _ := trust.CatalogHash(now)
	return hash == p.tr.hash
}

const catalogChangedOnDisk = "the catalog changed on disk; reload to review it"

// editCatalog is one write of the picker's own on the catalog files — +,
// d, v, a catalog profile's delete — and what follows it (resnapshot).
// write returns the names of the project rows it wrote a spec for (+ and
// v's move in), whose rows then take the spec as the file has it. A
// catalog trust in force carries over the write, since the user made it
// here and saw what it did; but only when the files on disk were still the
// bytes the picker loaded, and are after the write the rows and nothing
// else, or the carry-over would cover an edit made elsewhere, unseen. The
// note is the status line's word about the trust, "" when there is nothing
// to say.
func (p *picker) editCatalog(write func() (wrote []string, err error)) (note string, err error) {
	carry := p.tr.catalog == trust.CatalogTrusted && p.catalogOnDisk()
	wrote, err := write()
	if err != nil {
		return "", err
	}
	return p.resnapshot(wrote, carry), nil
}

// resnapshot follows a catalog write of the picker's own — +, d, v, a
// catalog profile's delete. The rows were changed in place; the bytes the
// trust is keyed on (cat.Files) are read again, so that T covers the
// catalog the picker has instead of refusing it as changed on disk. A
// trust held lapses, as it does for any edit, unless carry says the write
// was this picker's on a catalog that was still as loaded: then the
// trust is recorded again for the new bytes, and the note says so.
//
// Only the rows in wrote — the ones this write put in the file — take
// their specs as the file now has them, so that what a check records
// (trust.SpecFingerprint) and what a move compares is the spec as written,
// the one the next load parses; and even they must be what was written.
// Every other project row must be in the files exactly as it was loaded.
// When the files say anything else — a server edited, added or removed by
// someone else since the load, perhaps in the moment of the write itself —
// nothing is adopted: the rows stay as the user saw them, no trust is
// carried or held (drift) and T refuses until a reload, as for any file
// the screen did not show. Adopting such a file would have handed a check
// or the catalog trust to a spec the user never saw.
func (p *picker) resnapshot(wrote []string, carry bool) string {
	files, err := catalog.Snapshot(p.cat.Path, p.tr.root)
	if err != nil {
		return p.drifted()
	}
	specs := map[string]map[string]any{}
	sources := map[string]string{}
	for _, f := range files {
		names, parsed, _, err := catalog.Parse(f.Path, f.Data)
		if err != nil {
			return p.drifted()
		}
		for _, n := range names {
			if _, dup := specs[n]; !dup { // the first file wins, as in Load
				specs[n], sources[n] = parsed[n], f.Path
			}
		}
	}
	mine := map[string]bool{}
	for _, n := range wrote {
		mine[n] = true
	}
	rows := 0
	for _, s := range p.cat.Servers {
		if s.Origin != catalog.OriginProject {
			continue
		}
		disk, ok := specs[s.Name]
		if !ok || trust.SpecFingerprint(disk) != trust.SpecFingerprint(s.Spec) {
			return p.drifted()
		}
		if !mine[s.Name] && s.Source != "" && s.Source != sources[s.Name] {
			return p.drifted()
		}
		rows++
	}
	if rows != len(specs) {
		return p.drifted()
	}
	for i := range p.cat.Servers {
		if s := &p.cat.Servers[i]; s.Origin == catalog.OriginProject && mine[s.Name] {
			s.Spec, s.Source = specs[s.Name], sources[s.Name]
		}
	}
	p.cat.Files = files
	p.refreshCatalog()
	if !carry || p.tr.catalog != trust.CatalogChanged {
		return ""
	}
	p.tr.store.TrustCatalog(p.tr.root, p.tr.hash, p.tr.files)
	err = p.tr.store.Save()
	p.refreshCatalog()
	if err != nil {
		return " · repo trust not carried over (" + trust.Safe(err.Error()) + "); T to trust it again"
	}
	return " · repo trust carried over to the edited catalog"
}

// drifted is resnapshot's answer to files that hold more than the
// picker's own write: the catalog counts as changed from here on, and the
// note says to reload.
func (p *picker) drifted() string {
	p.tr.drift = true
	p.refreshCatalog()
	return " · " + catalogChangedOnDisk
}

// scope is what trust.Gate is asked in: the store, the project and the
// catalog trust as last refreshed.
func (p *picker) scope() trust.Scope {
	return trust.Scope{Store: p.tr.store, Root: p.tr.root, Catalog: p.tr.catalog}
}

// catalogRemotes lists the remote servers of the project's catalog: what a
// catalog trust covers. Hidden ones are included — the trust covers them
// too, once their section is unfolded — and the screen says which they are.
func (p *picker) catalogRemotes() []catalog.Server {
	var out []catalog.Server
	for _, s := range p.cat.Servers {
		if s.Origin == catalog.OriginProject && spec.ViewOf(s.Spec).Remote() {
			out = append(out, s)
		}
	}
	return out
}

// trustKey is T's label on the key line: what T would do now.
func (p *picker) trustKey() string {
	if p.tr.catalog == trust.CatalogTrusted {
		return "T untrust repo"
	}
	return "T trust repo"
}

// trustCatalogKey is T: the confirmation screen, or a status line saying
// why there is nothing to ask about.
func (p *picker) trustCatalogKey() (tea.Model, tea.Cmd) {
	p.tr.armed = ""
	p.refreshCatalog()
	if len(p.tr.files) == 0 {
		p.status = "no catalog file in this project (" + fsutil.ShortenHome(p.cat.Path) + "); nothing to trust"
		return p, nil
	}
	if p.tr.catalog != trust.CatalogTrusted && len(p.catalogRemotes()) == 0 {
		p.status = "this repository's catalog defines no remote server; a command needs its own review (m m or M)"
		return p, nil
	}
	if !p.catalogOnDisk() {
		p.status = styErr.Render(catalogChangedOnDisk)
		return p, nil
	}
	p.mode = modeConfirmCatalog
	p.tr.offset = 0
	return p, nil
}

// updateConfirmCatalog is the T screen: y trusts (or untrusts), the arrows
// scroll, anything else leaves things as they were.
func (p *picker) updateConfirmCatalog(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "y", "Y":
		p.mode = modeList
		if p.tr.catalog == trust.CatalogTrusted {
			p.tr.store.WithdrawCatalog(p.tr.root)
			err := p.tr.store.Save()
			p.refreshCatalog()
			if err != nil {
				p.status = styErr.Render("could not save trust (" + trust.Safe(err.Error()) + ")")
			} else {
				p.status = "untrusted this repository's catalog for " + fsutil.ShortenHome(p.tr.root)
			}
			return p, nil
		}
		// Checked again at y: the screen may have been open a while, and
		// the record must cover the bytes on screen and nothing else.
		if !p.catalogOnDisk() {
			p.status = styErr.Render(catalogChangedOnDisk)
			return p, nil
		}
		p.tr.store.TrustCatalog(p.tr.root, p.tr.hash, p.tr.files)
		err := p.tr.store.Save()
		p.refreshCatalog()
		// The rows the last measurement left out for a check are what the
		// trust was for: measure them now, as y on the M screen runs what
		// it trusted. Nothing else is touched.
		only := map[string]bool{}
		for _, s := range p.catalogRemotes() {
			if v, ok := p.tr.skipped[s.Name]; ok && !v.NeedsTrust {
				only[s.Name] = true
			}
		}
		var cmd tea.Cmd
		if len(only) > 0 {
			cmd = p.measureServers(only)
		}
		if err != nil {
			p.status = styErr.Render("could not save trust (" + trust.Safe(err.Error()) + "); this session measures the catalog's servers anyway")
		} else {
			p.status = "trusted this repository's catalog for " + fsutil.ShortenHome(p.tr.root) + " · m measures its remote servers"
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
		p.status = "nothing changed"
		return p, nil
	}
	p.scrollTrust()
	return p, nil
}

// catalogLines is the body of the T screen: the catalog files, then each
// remote server with its URL and header names (values masked, unless they
// are `${VAR}` placeholders — those are the user's own that would leave),
// wrapped whole like a command on the M screen. Names and addresses go
// through trust.Safe.
func (p *picker) catalogLines() []string {
	width := max(20, p.width)
	var lines []string
	for _, f := range p.tr.files {
		lines = append(lines, "  "+truncate(fsutil.ShortenHome(f), width-4))
	}
	remotes := p.catalogRemotes()
	if len(remotes) == 0 {
		lines = append(lines, styDim.Render("  (no remote server in the catalog now)"))
	}
	for _, s := range remotes {
		tag := ""
		if p.hidden[s.Name] {
			tag = "  " + styDim.Render("hidden")
		}
		lines = append(lines, "  "+truncate(trust.Safe(s.Name), width-14)+tag)
		for _, l := range wrap(trust.DescribeRemote(s.Spec), width-6) {
			lines = append(lines, "      "+l)
		}
	}
	return lines
}

// catalogScreen is the T screen, drawn instead of the list.
func (p *picker) catalogScreen() string {
	var b strings.Builder
	width := max(20, p.width)
	title := "Trust this repository's catalog?"
	note := "m then measures every remote server it defines, unticked, with your environment expanded into URL and headers."
	switch p.tr.catalog {
	case trust.CatalogTrusted:
		title = "Untrust this repository's catalog?"
		note = "m then measures only the harmless remote servers of the catalog, and ticked ones."
	case trust.CatalogChanged:
		note = "The catalog changed since you trusted it. " + note
	}
	fmt.Fprintf(&b, "%s  %s\n", styTitle.Render(title), styDim.Render(truncate(note, width-len(title)-2)))

	lines := p.catalogLines()
	rows := p.trustRows()
	start, end := p.trustWindow(len(lines))
	for _, l := range lines[start:end] {
		fmt.Fprintf(&b, "%s\n", l)
	}
	if len(lines) > rows {
		fmt.Fprintf(&b, "%s\n", styDim.Render(fmt.Sprintf("  … lines %d-%d of %d", start+1, end, len(lines))))
	}

	b.WriteString(styDim.Render(truncate("Stdio commands are not covered: each still needs its own review (m m or M). "+
		"An edit to a catalog file lapses this trust.", width)) + "\n")
	b.WriteString(styDim.Render(truncate("Recorded in "+fsutil.ShortenHome(p.tr.store.Path())+" for "+fsutil.ShortenHome(p.tr.root)+
		", keyed by the files' contents.", width)) + "\n")
	hint := "y trust · esc keep asking · ↑↓ scroll"
	if p.tr.catalog == trust.CatalogTrusted {
		hint = "y untrust · esc keep trusting · ↑↓ scroll"
	}
	b.WriteString(styDim.Render(truncate(hint, width)) + "\n")
	return b.String()
}
