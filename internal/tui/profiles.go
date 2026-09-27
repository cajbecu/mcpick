package tui

// The profile manager: a screen of its own inside the picker, profiles on the
// left, the servers of the highlighted one on the right. Every edit is saved
// at once to the user's own profiles file, never to the catalog.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/trust"
)

// pmSub is a prompt open on top of the screen.
type pmSub int

const (
	pmNone pmSub = iota
	pmAdd
	pmCopy
	pmRename
	pmConfirmDelete
)

// rowKind says where a profile comes from, which decides what may be done to
// it: only personal ones are edited here.
type rowKind int

const (
	rowDefault  rowKind = iota // built-in: everything available
	rowPersonal                // ~/.mcpick/profiles.yaml
	rowCatalog                 // profiles: in the project's catalog, read-only
)

type profileRow struct {
	name    string
	kind    rowKind
	servers []string
}

// serverRow is one line of the right pane: a catalog index, or a name the
// profile lists that this project does not have (idx < 0).
type serverRow struct {
	idx  int
	name string
}

type profilesScreen struct {
	pane       int // 0 profiles, 1 servers
	cursor     int // index into profileRows
	srv        int // index into serverRows of the highlighted profile
	offL, offR int
	sub        pmSub
	input      string
}

// openProfiles switches to the screen. The cursor starts on the first
// personal profile, the one most likely wanted, else on default.
func (p *picker) openProfiles(sub pmSub) {
	p.mode = modeProfiles
	p.pm = profilesScreen{sub: sub}
	if len(p.profiles.Profiles) > 0 {
		p.pm.cursor = 1
	}
	p.status = ""
}

// profileRows is the left pane: default first, then the user's own in stored
// order, then the catalog's, minus any a personal one shadows.
func (p *picker) profileRows() []profileRow {
	rows := []profileRow{{name: profile.Default, kind: rowDefault, servers: p.defaultServers()}}
	for _, pr := range p.profiles.Profiles {
		rows = append(rows, profileRow{name: pr.Name, kind: rowPersonal, servers: pr.Servers})
	}
	for _, n := range p.cat.ProfileNames() {
		if _, _, ok := p.profiles.Find(n); ok {
			continue
		}
		rows = append(rows, profileRow{name: n, kind: rowCatalog, servers: p.cat.Profiles[n]})
	}
	return rows
}

func (p *picker) currentProfile() profileRow {
	rows := p.profileRows()
	return rows[max(0, min(p.pm.cursor, len(rows)-1))]
}

// defaultServers is what `a` would check: every server but the ones disabled
// in Claude Code, which only an explicit press may switch back on, and the
// ones hidden with h, which are not wanted here (--all skips them too).
func (p *picker) defaultServers() []string {
	var out []string
	for _, s := range p.cat.Servers {
		if !p.disabled(s) && !p.hidden[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out
}

func (p *picker) has(name string) bool {
	_, ok := p.cat.Find(name)
	return ok
}

// serverRows is the right pane for one profile: the whole catalog, in its
// order, then the names this project lacks.
func (p *picker) serverRows(row profileRow) []serverRow {
	out := make([]serverRow, 0, len(p.cat.Servers))
	for i, s := range p.cat.Servers {
		out = append(out, serverRow{idx: i, name: s.Name})
	}
	for _, n := range profile.Missing(row.servers, p.has) {
		out = append(out, serverRow{idx: -1, name: n})
	}
	return out
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// resolved is the one rule for applying a profile, shared by enter, l and c:
// the servers it names that this project has, minus those disabled in Claude
// Code. Skips are counted so the status can say what was left out.
func (p *picker) resolved(row profileRow) (sel map[string]bool, missing, disabled int) {
	sel = map[string]bool{}
	for _, s := range p.cat.Servers {
		if !contains(row.servers, s.Name) {
			continue
		}
		if p.disabled(s) {
			disabled++
			continue
		}
		sel[s.Name] = true
	}
	return sel, len(profile.Missing(row.servers, p.has)), disabled
}

// applyProfile makes the profile the picker's selection and says what was
// skipped, in the words `a` uses.
func (p *picker) applyProfile(row profileRow) {
	sel, missing, disabled := p.resolved(row)
	p.sel = sel
	p.status = fmt.Sprintf("profile %q loaded", row.name)
	if missing > 0 {
		p.status += fmt.Sprintf(" (%d missing here skipped)", missing)
	}
	if disabled > 0 {
		p.status += fmt.Sprintf(" (%d disabled in Claude Code left unchecked: check them one by one)", disabled)
	}
}

// selectedNames is the picker's selection in catalog order.
func (p *picker) selectedNames() []string {
	var out []string
	for _, s := range p.cat.Servers {
		if p.sel[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out
}

// profileNames is what a copy of row stores: the servers present here in
// catalog order, then the ones this project lacks, so nothing is lost. It
// reads row.servers for every kind — for default, what the screen shows
// checked — so a copy never holds a box the original showed empty.
func (p *picker) profileNames(row profileRow) []string {
	var out []string
	for _, s := range p.cat.Servers {
		if contains(row.servers, s.Name) {
			out = append(out, s.Name)
		}
	}
	return append(out, profile.Missing(row.servers, p.has)...)
}

// edit applies one change to the profiles file as it is on disk now (another
// picker may have written it since this one started) and reports. On an
// error nothing changes, on screen or in the file, and the status says why.
// The reload can shift the rows (a profile deleted elsewhere), so the cursor
// is put back on the profile it was on, by name, not by index; callers that
// add, rename or move set it again afterwards.
func (p *picker) edit(fn func(*profile.Set) error, ok string) bool {
	was := p.currentProfile()
	if err := p.profiles.Update(fn); err != nil {
		p.status = styErr.Render(trust.Safe(err.Error()))
		return false
	}
	p.pm.cursor = p.rowIndex(was)
	p.status = ok
	return true
}

// rowIndex is where row is now, by name and kind; gone, the old index
// clamped, so the cursor stays in range.
func (p *picker) rowIndex(row profileRow) int {
	rows := p.profileRows()
	for i, r := range rows {
		if r.name == row.name && r.kind == row.kind {
			return i
		}
	}
	return max(0, min(p.pm.cursor, len(rows)-1))
}

func (p *picker) readOnly(row profileRow, verb string) string {
	if row.kind == rowDefault {
		return fmt.Sprintf("default is everything available and cannot be %s; copy it with c to edit", verb)
	}
	return fmt.Sprintf("%q comes from the project's catalog and cannot be %s here; edit the file or copy it with c", row.name, verb)
}

func (p *picker) updateProfiles(k string) (tea.Model, tea.Cmd) {
	switch p.pm.sub {
	case pmAdd, pmCopy, pmRename:
		return p.updateProfilesInput(k)
	case pmConfirmDelete:
		return p.updateProfilesConfirm(k)
	}
	row := p.currentProfile()
	switch k {
	case "ctrl+c":
		return p, tea.Quit
	case "esc", "q":
		// Back to the list; a sub-screen does not quit the program.
		p.mode = modeList
		p.status = ""
	case "tab", "shift+tab":
		p.pm.pane ^= 1
		p.status = ""
	case "enter":
		p.applyProfile(row)
		p.launch = true
		return p, tea.Quit
	case "l":
		p.applyProfile(row)
		p.mode = modeList
	case "up", "k":
		p.moveProfiles(-1)
	case "down", "j":
		p.moveProfiles(1)
	case "pgup", "ctrl+u":
		p.moveProfiles(-p.profilesListHeight())
	case "pgdown", "ctrl+d":
		p.moveProfiles(p.profilesListHeight())
	case "g", "home":
		p.moveProfiles(-1 << 30)
	case "G", "end":
		p.moveProfiles(1 << 30)
	default:
		if p.pm.pane == 0 {
			p.profilesLeftKey(k, row)
		} else {
			p.profilesRightKey(k, row)
		}
	}
	return p, nil
}

func (p *picker) moveProfiles(delta int) {
	if p.pm.pane == 0 {
		p.pm.cursor = max(0, min(len(p.profileRows())-1, p.pm.cursor+delta))
		p.pm.srv = 0
		return
	}
	n := len(p.serverRows(p.currentProfile()))
	if n > 0 {
		p.pm.srv = max(0, min(n-1, p.pm.srv+delta))
	}
}

func (p *picker) profilesLeftKey(k string, row profileRow) {
	switch k {
	case "+":
		p.pm.sub, p.pm.input, p.status = pmAdd, "", ""
	case "c":
		name := row.name
		if name == profile.Default {
			name = "" // reserved: the copy needs a name of its own
		}
		p.pm.sub, p.pm.input, p.status = pmCopy, name, ""
	case "r":
		if row.kind != rowPersonal {
			p.status = p.readOnly(row, "renamed")
			return
		}
		p.pm.sub, p.pm.input, p.status = pmRename, row.name, ""
	case "d":
		// A catalog profile can be deleted from its file (asked first, the
		// file named): it may be one nobody meant to keep, like a pasted
		// prompt an older picker saved as a name. Renaming and moving stay
		// personal-only.
		if row.kind != rowPersonal && row.kind != rowCatalog {
			p.status = p.readOnly(row, "deleted")
			return
		}
		p.pm.sub, p.status = pmConfirmDelete, ""
	case "J", "K":
		if row.kind != rowPersonal {
			p.status = p.readOnly(row, "moved")
			return
		}
		delta := 1
		if k == "K" {
			delta = -1
		}
		if !p.edit(func(s *profile.Set) error { return s.Move(row.name, delta) }, "") {
			return
		}
		_, i, _ := p.profiles.Find(row.name)
		p.pm.cursor = 1 + i
	}
}

func (p *picker) profilesRightKey(k string, row profileRow) {
	if k != " " && k != "space" && k != "x" && k != "a" && k != "n" {
		return
	}
	if row.kind != rowPersonal {
		p.status = p.readOnly(row, "edited")
		return
	}
	if err := profile.CheckName(row.name); err != nil {
		// Stored before the name rule (or pasted in before it existed): shown
		// as it is, never rewritten under that name; rename it (r) to edit it.
		p.status = styErr.Render(trust.Safe("rename it first (r): " + err.Error()))
		return
	}
	srvs := p.serverRows(row)
	if len(srvs) == 0 {
		return
	}
	sr := srvs[max(0, min(p.pm.srv, len(srvs)-1))]
	p.edit(func(s *profile.Set) error {
		pr, _, ok := s.Find(row.name)
		if !ok {
			return fmt.Errorf("no profile %q", row.name)
		}
		var names []string
		switch k {
		case "a":
			// Every box here, and whatever the profile keeps for other projects.
			names = append(p.defaultServers(), profile.Missing(pr.Servers, p.has)...)
		case "n":
			names = nil
		default:
			if _, err := s.Toggle(row.name, sr.name); err != nil {
				return err
			}
			pr, _, _ = s.Find(row.name)
			// Stored as the catalog orders them, missing ones last, so the
			// file reads like the screen.
			names = p.profileNames(profileRow{name: row.name, kind: rowPersonal, servers: pr.Servers})
		}
		return s.Put(row.name, names)
	}, "")
}

func (p *picker) updateProfilesInput(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "ctrl+c":
		return p, tea.Quit
	case "esc":
		p.pm.sub, p.pm.input = pmNone, ""
	case "enter":
		p.commitProfilesInput()
	case "backspace":
		if r := []rune(p.pm.input); len(r) > 0 {
			p.pm.input = string(r[:len(r)-1])
		}
	case "space":
		p.pm.input += " "
	default:
		if len([]rune(k)) == 1 {
			p.pm.input += k
		}
	}
	return p, nil
}

// commitProfilesInput applies the prompt. A validation error stays on the
// status line with the prompt still open, so the name can be corrected.
func (p *picker) commitProfilesInput() {
	name := strings.TrimSpace(p.pm.input)
	row := p.currentProfile()
	where := fsutil.ShortenHome(p.profiles.Path())
	sub := p.pm.sub
	ok := p.edit(func(s *profile.Set) error {
		switch sub {
		case pmAdd:
			return s.Add(name, p.selectedNames())
		case pmCopy:
			return s.Add(name, p.profileNames(row))
		}
		return s.Rename(row.name, name)
	}, fmt.Sprintf("profile %q saved to %s", name, where))
	if !ok {
		return
	}
	_, i, _ := p.profiles.Find(name)
	p.pm.cursor = 1 + i
	p.pm.sub, p.pm.input = pmNone, ""
}

func (p *picker) updateProfilesConfirm(k string) (tea.Model, tea.Cmd) {
	row := p.currentProfile()
	p.pm.sub = pmNone
	switch k {
	case "ctrl+c":
		return p, tea.Quit
	case "y", "Y":
		if row.kind == rowCatalog {
			p.deleteCatalogProfile(row.name)
			return p, nil
		}
		if p.edit(func(s *profile.Set) error { return s.Delete(row.name) },
			fmt.Sprintf("deleted profile %q (its servers stay in the catalog)", row.name)) {
			p.pm.cursor = max(0, min(p.pm.cursor, len(p.profileRows())-1))
		}
	default:
		p.status = "cancelled"
	}
	return p, nil
}

// deleteCatalogProfile removes a profile from the catalog file it came from.
func (p *picker) deleteCatalogProfile(name string) {
	file := p.cat.ProfileFiles[name]
	if file == "" {
		file = p.cat.Path
	}
	note, err := p.editCatalog(func() ([]string, error) {
		if err := catalog.DeleteProfile(file, name); err != nil {
			return nil, err
		}
		delete(p.cat.Profiles, name)
		delete(p.cat.ProfileFiles, name)
		return nil, nil
	})
	if err != nil {
		p.status = styErr.Render(trust.Safe(err.Error()))
		return
	}
	p.pm.cursor = max(0, min(p.pm.cursor, len(p.profileRows())-1))
	p.status = fmt.Sprintf("deleted profile %s from %s (its servers stay)", clip(trust.Safe(fmt.Sprintf("%q", name)), 60), fsutil.ShortenHome(file)) + note
}

// --- drawing ----------------------------------------------------------------

// paneLine is one drawn line of a pane and the row it belongs to, -1 for a
// heading, so scrolling can keep the cursor's row in view.
type paneLine struct {
	text string
	row  int
}

func (p *picker) profilesWarnings() []string {
	var out []string
	if err := p.profiles.Err(); err != nil {
		out = append(out, fmt.Sprintf("%s could not be read (%v); personal profiles are unavailable",
			fsutil.ShortenHome(p.profiles.Path()), err))
	}
	out = append(out, p.profiles.Warnings...)
	if _, ok := p.cat.Profiles[profile.Default]; ok {
		// Two rows called default: the built-in one and the catalog's, told
		// apart by their tag. --profile default refuses to choose; say so here.
		out = append(out, fmt.Sprintf("%s also defines %q (the built-in profile): rename it there or copy it with c",
			fsutil.ShortenHome(p.cat.Path), profile.Default))
	}
	return out
}

// warningLines is what the window can show of profilesWarnings: the title,
// the key line, the pane headers and two pane rows come first; when the rest
// does not hold every warning, the last line says how many were left out.
func (p *picker) warningLines() []string {
	all := p.profilesWarnings()
	fixed := 3 + 2 // title, key line, one pane header, two pane rows
	if p.stacked() {
		fixed++
	}
	room := p.height - fixed
	switch {
	case len(all) <= room:
		return all
	case room <= 0:
		return nil
	case room == 1:
		return []string{fmt.Sprintf("%d warnings; the window is too short to show them", len(all))}
	}
	shown := append([]string{}, all[:room-1]...)
	return append(shown, fmt.Sprintf("… and %d more warnings", len(all)-len(shown)))
}

// Pane widths side by side: the left pane never squeezes the right one
// below what a row needs (pointer, box and a readable name); narrower than
// both minimums plus the separator, the panes stack.
const (
	minLeftW  = 20
	minRightW = 24
	paneSep   = 3
)

// pmLayout is what fits in the window: the pane heights and which optional
// lines are drawn. Short windows drop the spacer above the key line, then
// the command line, then the status line (which then takes the key line's
// place while it has something to say), before the panes go below two rows.
type pmLayout struct {
	hL, hR  int
	command bool
	spacer  bool
	status  bool
}

func (p *picker) profilesLayout() pmLayout {
	l := pmLayout{command: p.showCommand(), spacer: true, status: true}
	fixed := 2 + len(p.warningLines()) // title and key line
	if p.stacked() {
		fixed += 2 // one header per pane
	} else {
		fixed++
	}
	free := func() int {
		n := p.height - fixed
		for _, on := range []bool{l.command, l.spacer, l.status} {
			if on {
				n--
			}
		}
		return n
	}
	for _, drop := range []*bool{&l.spacer, &l.command, &l.status} {
		if free() < 2 {
			*drop = false
		}
	}
	n := max(2, free())
	if p.stacked() {
		l.hL, l.hR = n/2, n-n/2
	} else {
		l.hL, l.hR = n, n
	}
	// config.yaml's max_rows caps the panes as it caps the list: that many
	// profiles on the left, that many servers on the right — the right
	// pane's headings come on top, as the list's do.
	if p.maxRows > 0 {
		l.hL = max(1, min(l.hL, p.maxRows))
		l.hR = max(1, min(l.hR, p.maxRows+p.rightHeadings()))
	}
	return l
}

// rightHeadings counts the heading lines of the right pane for the
// highlighted profile: one per origin present, one more for Missing here.
func (p *picker) rightHeadings() int {
	n := 0
	for _, line := range p.rightLines(p.currentProfile(), max(20, p.width)) {
		if line.row < 0 {
			n++
		}
	}
	return n
}

// profilesListHeight is how many rows a pane can page through.
func (p *picker) profilesListHeight() int { return max(1, p.profilesLayout().hL) }

// stacked says the terminal is too narrow for two panes side by side.
func (p *picker) stacked() bool { return p.width < minLeftW+paneSep+minRightW }

// paneHeights is the rows each pane gets.
func (p *picker) paneHeights() (left, right int) {
	l := p.profilesLayout()
	return l.hL, l.hR
}

// leftWidth fits the longest name when it can, and otherwise leaves the
// right pane its minimum: a long profile name is truncated, not given the
// whole terminal.
func (p *picker) leftWidth() int {
	if p.stacked() {
		return p.width
	}
	longest := 0
	for _, r := range p.profileRows() {
		longest = max(longest, len([]rune(trust.Safe(r.name))))
	}
	room := p.width - paneSep - minRightW
	return max(minLeftW, min(34, room, longest+leftPad))
}

// leftPad is what a left-pane row adds around the name: pointer, count,
// the longest tag and their gaps.
const leftPad = 2 + 1 + 3 + 1 + 8

// clampOffset moves a scroll offset the least needed to show pos in height
// rows out of n, and never past the end.
func clampOffset(off, pos, n, height int) int {
	if pos < off {
		off = pos
	}
	if pos >= off+height {
		off = pos - height + 1
	}
	return max(0, min(off, n-height))
}

// visibleRows is how many rows fit when n rows share height lines with the
// "… a-b of n" line that appears once they do not all fit. A single line
// shows a row rather than only the count.
func visibleRows(n, height int) int {
	if n > height && height > 1 {
		return height - 1
	}
	return max(1, height)
}

// scrollProfiles keeps both cursors in range and in view; called after every
// message so View can stay pure.
func (p *picker) scrollProfiles() {
	rows := p.profileRows()
	p.pm.cursor = max(0, min(p.pm.cursor, len(rows)-1))
	hL, hR := p.paneHeights()
	p.pm.offL = clampOffset(p.pm.offL, p.pm.cursor, len(rows), visibleRows(len(rows), hL))

	lines := p.rightLines(rows[p.pm.cursor], p.width)
	srvs := p.serverRows(rows[p.pm.cursor])
	p.pm.srv = max(0, min(p.pm.srv, len(srvs)-1))
	pos := 0
	for i, l := range lines {
		if l.row == p.pm.srv {
			pos = i
		}
	}
	p.pm.offR = clampOffset(p.pm.offR, pos, len(lines), visibleRows(len(lines), hR))
}

func padTo(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// cut trims a pane's lines to its window and adds the "… a-b of n" line when
// rows are out of view, padding with blank lines so the panes line up.
func cut(lines []paneLine, off, height int) []string {
	if height <= 0 {
		return nil
	}
	n := len(lines)
	avail := visibleRows(n, height)
	end := min(off+avail, n)
	out := make([]string, 0, height)
	for _, l := range lines[off:end] {
		out = append(out, l.text)
	}
	if n > height && height > 1 {
		out = append(out, styDim.Render(fmt.Sprintf("  … %d-%d of %d", off+1, end, n)))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

func (p *picker) pointer(active bool, on bool) string {
	switch {
	case !on:
		return "  "
	case active:
		return styCursor.Render("> ")
	}
	return styDim.Render("> ")
}

// Names, addresses and paths drawn on this screen are the catalog's and the
// profiles file's text: they go through trust.Safe before they are measured,
// truncated or styled, so an escape in them is shown, not obeyed, as on the
// list and the trust screen.
func (p *picker) leftLines(rows []profileRow, width int) []paneLine {
	nameW := max(1, width-leftPad)
	out := make([]paneLine, 0, len(rows))
	for i, r := range rows {
		count := len(r.servers)
		// The tag is the source, which is what tells two rows of the same
		// name apart (a catalog that defines its own "default").
		tag := ""
		switch r.kind {
		case rowCatalog:
			tag = styDim.Render("catalog")
		case rowDefault:
			tag = styDim.Render("built-in")
		}
		text := fmt.Sprintf("%s%-*s %3d %s", p.pointer(p.pm.pane == 0, i == p.pm.cursor),
			nameW, truncate(trust.Safe(r.name), nameW), count, tag)
		out = append(out, paneLine{text: strings.TrimRight(text, " "), row: i})
	}
	return out
}

// costCell is the measured cost of a server as the picker's list shows it,
// seven columns wide.
func (p *picker) costCell(s catalog.Server) string {
	if m, ok := p.cache.Get(s.Name, s.Spec); ok {
		if m.OK {
			return styCost.Render(fmt.Sprintf("%7s", mcp.HumanTokens(m.Tokens)))
		}
		return styErr.Render(fmt.Sprintf("%7s", mcp.ErrorKind(m.Err)))
	}
	if p.measuring[s.Name] {
		return styDim.Render("    ···")
	}
	return "       "
}

// profileBox is a right-pane row's box and tag: the box says whether the
// profile names the server, the tag what launching would do with it.
func (p *picker) profileBox(row profileRow, s catalog.Server) (box, tag string) {
	in := contains(row.servers, s.Name)
	box = "[ ]"
	switch {
	case in && row.kind == rowPersonal:
		box = styOn.Render("[x]")
	case in:
		box = styDim.Render("[x]") // read-only: shown, not editable here
	case row.kind != rowPersonal:
		box = styDim.Render(box)
	}
	// A hidden server is still listed — a profile names what it names — but
	// tagged, so a box checked here is not a surprise at launch.
	if p.hidden[s.Name] {
		tag = styDim.Render("hidden")
	}
	if p.disabled(s) {
		tag = styWarn.Render("disabled in Claude Code")
	}
	return box, tag
}

func (p *picker) rightLines(row profileRow, width int) []paneLine {
	srvs := p.serverRows(row)
	nameW := 0
	for _, sr := range srvs {
		nameW = max(nameW, len([]rune(trust.Safe(sr.name))))
	}
	nameW = min(nameW, max(6, width-6))
	active := p.pm.pane == 1
	// fit appends a column only when it fits, so a narrow pane loses the
	// endpoint, then the tag, then the cost, and never wraps.
	fit := func(text, gap, col string) string {
		if col != "" && width-lipgloss.Width(text)-len(gap) >= lipgloss.Width(col) {
			return text + gap + col
		}
		return text
	}
	heading := func(label, note string) paneLine {
		text := styGroup.Render(label)
		if room := width - lipgloss.Width(text) - 1; room >= 6 {
			text += " " + styDim.Render(truncate(trust.Safe(note), room))
		}
		return paneLine{text: text, row: -1}
	}
	var out []paneLine
	last := ""
	for i, sr := range srvs {
		if sr.idx < 0 {
			if last != "missing" {
				out = append(out, heading("Missing here", "• listed by the profile, not in this project"))
				last = "missing"
			}
			text := p.pointer(active, i == p.pm.srv) + styWarn.Render("[!]") + " " +
				fmt.Sprintf("%-*s", nameW, truncate(trust.Safe(sr.name), nameW))
			if room := width - lipgloss.Width(text) - 2; room >= 6 {
				text += "  " + styDim.Render(truncate("not in this project · space removes", room))
			}
			out = append(out, paneLine{text: text, row: i})
			continue
		}
		s := p.cat.Servers[sr.idx]
		if s.Origin != last {
			out = append(out, heading(p.groupLabel(s.Origin), p.groupPath(s.Origin)))
			last = s.Origin
		}
		box, tag := p.profileBox(row, s)
		text := fmt.Sprintf("%s%s %-*s", p.pointer(active, i == p.pm.srv), box, nameW, truncate(trust.Safe(s.Name), nameW))
		text = fit(text, " ", p.costCell(s))
		text = fit(text, "  ", tag)
		if room := width - lipgloss.Width(text) - 2; room >= 10 {
			text += "  " + styDim.Render(truncate(trust.Safe(s.Endpoint()), room))
		}
		out = append(out, paneLine{text: text, row: i})
	}
	return out
}

// tokensFor is the measured cost of the named servers, with a "+" when some
// are unmeasured, as the picker's header does for the selection.
func (p *picker) tokensFor(sel map[string]bool) string {
	total, complete := 0, true
	for _, s := range p.cat.Servers {
		if !sel[s.Name] {
			continue
		}
		m, ok := p.cache.Get(s.Name, s.Spec)
		if !ok || !m.OK {
			complete = false
			continue
		}
		total += m.Tokens
	}
	if total == 0 {
		return ""
	}
	suffix := ""
	if !complete {
		suffix = "+"
	}
	return fmt.Sprintf(" · ~%s%s context", mcp.HumanTokens(total), suffix)
}

func (p *picker) profilesKeys() string {
	name := p.currentProfile().name
	switch p.pm.sub {
	case pmAdd:
		return promptLine("new profile name: ", "", "", p.pm.input, p.width)
	case pmCopy:
		return promptLine("copy ", name, " as: ", p.pm.input, p.width)
	case pmRename:
		return promptLine("rename ", name, " to: ", p.pm.input, p.width)
	case pmConfirmDelete:
		// The keys are the "input" here: whatever else goes, [y/N] stays.
		if row := p.currentProfile(); row.kind == rowCatalog {
			return styWarn.Render(promptLine("delete profile ", name,
				" from "+fsutil.ShortenHome(p.cat.ProfileFiles[row.name])+"? ", "[y/N]", p.width))
		}
		line := promptLine("delete profile ", name, "? (its servers stay in the catalog) ", "[y/N]", p.width)
		if lipgloss.Width(line) > p.width || strings.Contains(line, "…") {
			line = promptLine("delete profile ", name, "? ", "[y/N]", p.width)
		}
		return styWarn.Render(line)
	}
	hint := "+ add · c copy · r rename · d delete · J/K move · tab servers · l load · enter launch · esc back"
	if p.pm.pane == 1 {
		hint = "space toggle · a all · n none · tab profiles · l load · enter launch · esc back"
	}
	return styDim.Render(clip(hint, p.width))
}

// promptLine fits `prefix "name" suffix input` in w cells with the end of
// the input always visible: the name gives way first, then the label, and
// last the head of the input itself.
func promptLine(prefix, name, suffix, input string, w int) string {
	label := func(name string) string {
		if name == "" {
			return prefix + suffix
		}
		return fmt.Sprintf("%s%q%s", prefix, name, suffix)
	}
	inW := lipgloss.Width(input)
	if lipgloss.Width(label(name))+inW <= w {
		return label(name) + input
	}
	// The suffix (the input, or [y/N]) is reserved first; the name gets what
	// is left, in cells, so a wide-character name cannot push it off.
	if room := w - inW - lipgloss.Width(prefix+suffix) - 2; name != "" && room >= 2 {
		if l := label(truncateCells(name, room)); lipgloss.Width(l)+inW <= w {
			return l + input
		}
	}
	if inW >= w-4 {
		// The input alone nearly fills the line: show its tail.
		r := []rune(input)
		for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
			r = r[1:]
		}
		return "…" + string(r)
	}
	return clip(label(name), w-inW) + input
}

// truncateCells is truncate measured in display cells rather than runes, for
// plain text that is quoted or padded afterwards (clip would add a reset).
func truncateCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	cells := 0
	for _, r := range s {
		cw := lipgloss.Width(string(r))
		if cells+cw > w-1 {
			break
		}
		b.WriteRune(r)
		cells += cw
	}
	b.WriteString("…")
	return b.String()
}

// clip cuts s to w display cells, ending with "…" when something was cut.
// Escape sequences are kept whole and not counted, and a cut ends with a
// reset, so a styled status line never wraps and never bleeds its colour
// into the line below. Names are measured in cells, not runes, so a wide
// character counts for two.
func clip(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	rs := []rune(s)
	var b strings.Builder
	cells := 0
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\x1b' && i+1 < len(rs) && rs[i+1] == '[' {
			j := i + 2
			for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
				j++
			}
			b.WriteString(string(rs[i:min(j+1, len(rs))]))
			i = j
			continue
		}
		cw := lipgloss.Width(string(rs[i]))
		if cells+cw > w-1 {
			break
		}
		b.WriteRune(rs[i])
		cells += cw
	}
	b.WriteString("…\x1b[0m")
	return b.String()
}

// profilesDetail explains the server under the right cursor, as the picker's
// detail line does; a missing one says why it has no box.
func (p *picker) profilesDetail() string {
	if p.pm.pane != 1 {
		return ""
	}
	srvs := p.serverRows(p.currentProfile())
	if len(srvs) == 0 {
		return ""
	}
	sr := srvs[max(0, min(p.pm.srv, len(srvs)-1))]
	if sr.idx < 0 {
		return styDim.Render(trust.Safe(sr.name) + ": not in this project's catalog")
	}
	return p.detailFor(p.cat.Servers[sr.idx])
}

func (p *picker) viewProfiles() tea.View {
	var b strings.Builder
	rows := p.profileRows()
	row := rows[max(0, min(p.pm.cursor, len(rows)-1))]
	sel, missing, _ := p.resolved(row)

	title := "mcpick"
	if p.version != "" {
		title += " " + p.version
	}
	head := styDim.Render(fmt.Sprintf("uid=%s · profiles", p.uid))
	if badge := p.badge(); badge != "" {
		head += " " + badge
	}
	line := styTitle.Render(title) + " " + head
	if lipgloss.Width(line) > p.width {
		line = styTitle.Render(truncate(title+" · profiles", p.width)) // the badge is a luxury
	}
	// Every line goes through clip on its way out: whatever a name, a path
	// or an error message brings, a line that wraps would push the cursor,
	// the prompt and the keys off their rows.
	put := func(s string) { b.WriteString(clip(s, p.width) + "\n") }
	put(line)
	layout := p.profilesLayout()
	if layout.command {
		names := make([]string, 0, len(sel))
		for _, s := range p.cat.Servers {
			if sel[s.Name] {
				names = append(names, s.Name)
			}
		}
		put(p.commandLineFor(names))
	}
	for _, w := range p.warningLines() {
		put(styWarn.Render(clip("! "+trust.Safe(w), p.width)))
	}

	leftW := p.leftWidth()
	rightW := p.width
	if !p.stacked() {
		rightW = p.width - leftW - paneSep
	}
	hL, hR := layout.hL, layout.hR
	present := 0
	for _, n := range row.servers {
		if p.has(n) {
			present++
		}
	}
	if row.kind == rowDefault {
		present = len(sel)
	}
	rightHead := fmt.Sprintf("%s · %d/%d servers%s", trust.Safe(row.name), present, len(p.cat.Servers), p.tokensFor(sel))
	if missing > 0 {
		rightHead += fmt.Sprintf(" · %d missing here", missing)
	}
	leftHead := styTitle.Render("Profiles")
	rightHead = styTitle.Render(clip(rightHead, max(0, rightW)))

	left := cut(p.leftLines(rows, leftW), p.pm.offL, hL)
	right := cut(p.rightLines(row, rightW), p.pm.offR, hR)
	sep := " " + styDim.Render("│") + " "
	if p.stacked() {
		put(leftHead)
		for _, l := range left {
			put(l)
		}
		put(rightHead)
		for _, l := range right {
			put(l)
		}
	} else {
		put(padTo(clip(leftHead, leftW), leftW) + sep + rightHead)
		// max_rows can leave the panes unequal (the right one has its
		// headings on top of its rows): draw the taller one whole, padding
		// the shorter, or the right pane's last rows — its cursor, its
		// "… a-b of n" line — would go undrawn.
		at := func(lines []string, i int) string {
			if i < len(lines) {
				return lines[i]
			}
			return ""
		}
		for i := 0; i < max(hL, hR); i++ {
			put(padTo(clip(at(left, i), leftW), leftW) + sep + clip(at(right, i), rightW))
		}
	}

	if layout.spacer {
		b.WriteString("\n")
	}
	keys := p.profilesKeys()
	if !layout.status && p.status != "" && p.pm.sub == pmNone {
		keys = p.status // no room of its own: an answer matters more than the hints
	}
	put(keys)
	if layout.status {
		if p.status != "" {
			put(p.status)
		} else if why := p.profilesDetail(); why != "" {
			put(why)
		}
	}
	return tea.NewView(b.String())
}
