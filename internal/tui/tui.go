// Package tui is the picker: a checkbox list of the catalog with a context-cost
// estimate beside every server.
package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cajbecu/mcpick/internal/backend"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/config"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/oauth"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/trust"
)

type mode int

const (
	modeList mode = iota
	modeConfirmYAML
	modeConfirmJSON
	modeAddName
	modeAddType
	modeAddTarget
	modeFilter
	modeProfiles
	modeConfirmTrust
	modeConfirmCatalog
	modeMoveTo
	modeMoveSecrets
)

var (
	styTitle  = lipgloss.NewStyle().Bold(true)
	styDim    = lipgloss.NewStyle().Faint(true)
	styOn     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styCursor = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styGroup  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styCost   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
)

// ErrAborted is returned when the user leaves the picker without launching.
var ErrAborted = errors.New("aborted")

type measuredMsg struct {
	name string
	spec map[string]any
	res  mcp.Result
	// note is something the measurement has to say besides its result: a
	// refreshed OAuth token that could not be saved.
	note string
}

type picker struct {
	cat    *catalog.Catalog
	sel    map[string]bool
	cursor int
	offset int
	mode   mode
	uid    string
	// uidGiven says --uid was on the command line: the header shows it
	// then, and not the hostname it defaults to.
	uidGiven bool
	version  string
	tgt      backend.Backend
	argv     []string
	input    string
	filter   string
	newSrv   catalog.Server
	status   string
	launch   bool

	width, height int
	cache         *mcp.Cache
	measuring     map[string]bool

	// root is the workspace, which keys the hidden set; hidden is that set
	// and showHidden whether its section is unfolded (see hide.go).
	root       string
	hidden     map[string]bool
	showHidden bool
	// maxRows is config.yaml's max_rows: the most server rows in view at
	// once (see scroll.go).
	maxRows int
	// profiles is the user's own set; pm is the profile manager's state.
	profiles *profile.Set
	pm       profilesScreen
	// tr is what the trust gate remembers for m/M (see trust.go).
	tr trustState
	// unconfirmed are the servers of the saved selection whose check no
	// longer covers them (Options.Unconfirmed), by name, with why: their
	// rows start unchecked and say so until they are checked again.
	unconfirmed map[string]string
	// mv is the move in progress after v (see move.go).
	mv moveState

	// now is the clock (nil means time.Now), started when Run began — zero
	// means no grace period — recent when the last printable keys arrived
	// and promptAt when the open text input was opened (see paste.go).
	now      func() time.Time
	started  time.Time
	recent   []time.Time
	promptAt time.Time
	// hold makes a command key wait out burstWindow before it acts, held the
	// keys waiting and heldGen which release may run them (see paste.go).
	hold    bool
	held    []heldKey
	heldGen int
}

// Options describe the launch the picker is choosing servers for.
type Options struct {
	UID string
	// UIDGiven says --uid was on the command line, so the header shows it;
	// the default, the hostname, is not worth a column.
	UIDGiven bool
	// FirstRun says no selection has been saved for this project and uid:
	// the picker then starts from what the agent would load without mcpick
	// (see preselect), not from nothing.
	FirstRun bool
	// Version is shown in the header, so a screenshot or a bug report says
	// which build it came from.
	Version string
	// Target and Argv are the agent and its command line; the header shows
	// the agent and the exact command that will run.
	Target backend.Backend
	Argv   []string
	// Root is the workspace the picker was opened in; it keys what is kept
	// per project: the hidden servers and which commands have been trusted
	// for measuring.
	Root string
	// Config is the user's config.yaml; the zero value means the defaults.
	Config config.Config
	// Profiles is the user's own set, for the profile manager (p).
	Profiles *profile.Set
	// Unconfirmed are the servers the saved selection had checked whose
	// check no longer covers them — a repository server that has since
	// taken a user server's name, or changed its spec (trust.Confirm). They
	// are not in sel; the picker says why on the row, and checking one
	// again is the new consent.
	Unconfirmed []trust.Unconfirmed
}

// Result is what the user launched with.
type Result struct {
	Selected map[string]bool
	// Reenable are servers disabled in Claude Code that the user checked,
	// and so asked to have switched back on in Claude Code.
	Reenable map[string]bool
}

// Run shows the picker and returns the selection the user launched with.
func Run(cat *catalog.Catalog, sel map[string]bool, opts Options) (Result, error) {
	p := build(cat, sel, opts)
	final, err := tea.NewProgram(p).Run()
	if err != nil {
		return Result{}, err
	}
	res := final.(*picker)
	if !res.launch {
		return Result{}, ErrAborted
	}
	reenable := map[string]bool{}
	for _, s := range res.cat.Servers {
		if res.disabled(s) && res.sel[s.Name] {
			reenable[s.Name] = true
		}
	}
	return Result{Selected: res.sel, Reenable: reenable}, nil
}

// build is the picker as Run starts it, before the program runs.
func build(cat *catalog.Catalog, sel map[string]bool, opts Options) *picker {
	// A server disabled in Claude Code is checked only by an explicit press:
	// checking it re-enables it in Claude Code for good, so a selection
	// saved by an earlier run must not do that on a bare enter.
	claude := opts.Target != nil && opts.Target.Info().ClaudeSettings
	for _, s := range cat.Servers {
		if claude && s.Disabled {
			delete(sel, s.Name)
		}
	}
	p := &picker{
		cat: cat, sel: sel, uid: opts.UID, uidGiven: opts.UIDGiven, version: opts.Version, tgt: opts.Target, argv: opts.Argv,
		width: 80, height: 24,
		// Measurements start empty every time: a number or an error on screen
		// is always from this session's `m`, never a leftover from an
		// earlier run that may no longer be true.
		cache:     &mcp.Cache{Entries: map[string]mcp.Measurement{}},
		measuring: map[string]bool{},
		root:      opts.Root,
		hold:      true,
		maxRows:   opts.Config.MaxRows,
		profiles:  opts.Profiles,
		tr:        newTrustState(trust.Load(), opts.Root),
		now:       time.Now,
		started:   time.Now(),
	}
	if p.profiles == nil {
		p.profiles = profile.New("") // saves fail with a reason, not a panic
	}
	if p.maxRows <= 0 {
		p.maxRows = config.DefaultMaxRows // a caller that loaded no config
	}
	p.loadHidden()
	p.refreshCatalog()
	if opts.FirstRun {
		p.preselect()
	}
	p.setUnconfirmed(opts.Unconfirmed)
	return p
}

// preselect is the first run's selection: the servers the agent would load
// without mcpick — the local, user and plugin ones, minus those disabled in
// Claude Code (for claude) and those hidden here. Project servers stay
// unchecked: Claude asks before loading them, and a tick is the consent
// that measuring and launching them rests on. A first run that opened
// with nothing checked launched the agent with no servers on a bare enter,
// which is rarely what a first-time user meant.
func (p *picker) preselect() {
	for _, s := range p.cat.Servers {
		if s.Origin == catalog.OriginProject || p.hidden[s.Name] || p.disabled(s) {
			continue
		}
		p.sel[s.Name] = true
	}
	// Only claude reads ~/.claude.json itself; for another agent these
	// are the servers Claude Code loads, which is what the status says.
	what := "what " + p.agentName() + " loads without mcpick"
	if p.tgt == nil || !p.tgt.Info().ClaudeSettings {
		what = "the servers Claude Code loads (local, user, plugins)"
	}
	p.status = "first run: checked " + what + " · project servers wait for a tick"
}

// agentName is the agent as the header and the notices call it: its
// backend's name, or the command for one mcpick has no adapter for.
func (p *picker) agentName() string {
	if p.tgt == nil {
		return "the agent"
	}
	if in := p.tgt.Info(); in.Color != "" {
		return in.Name
	}
	return firstArg(p.argv)
}

// nothingChecked is the header's word when nothing is checked: enter would
// launch with no MCP servers at all, and that has to be said before it
// happens.
func (p *picker) nothingChecked() string {
	for _, s := range p.cat.Servers {
		if p.sel[s.Name] {
			return ""
		}
	}
	return "nothing checked — enter launches " + p.agentName() + " with no MCP servers"
}

// setUnconfirmed takes the servers whose saved check lapsed: their boxes
// are empty (sel came without them), the status line names them once, and
// each row explains itself until it is checked again (see cursorDetail).
func (p *picker) setUnconfirmed(list []trust.Unconfirmed) {
	if len(list) == 0 {
		return
	}
	p.unconfirmed = map[string]string{}
	var names []string
	for _, u := range list {
		delete(p.sel, u.Name)
		p.unconfirmed[u.Name] = u.Why
		names = append(names, trust.Safe(u.Name))
	}
	p.status = styWarn.Render(fmt.Sprintf("%d unchecked, no longer what was checked: %s; check again to select",
		len(list), strings.Join(names, ", ")))
}

// unconfirmedDetail is the row's explanation while an unconfirmed server
// stays unchecked; a check ends it.
func (p *picker) unconfirmedDetail(s catalog.Server) string {
	why, ok := p.unconfirmed[s.Name]
	if !ok || p.sel[s.Name] {
		return ""
	}
	return styWarn.Render(truncate(trust.Safe(s.Name)+" "+why+"; check it again", max(20, p.width)))
}

func (p *picker) Init() tea.Cmd { return nil }

// visible is the list of catalog indices passing the current filter: the
// active rows, then — only while the section is unfolded — the hidden ones.
func (p *picker) visible() []int {
	out := make([]int, 0, len(p.cat.Servers))
	for i, s := range p.cat.Servers {
		if !p.hidden[s.Name] && p.matches(s) {
			out = append(out, i)
		}
	}
	if p.showHidden {
		for i, s := range p.cat.Servers {
			if p.hidden[s.Name] && p.matches(s) {
				out = append(out, i)
			}
		}
	}
	return out
}

// current is the row under the cursor, if that row is in view: a cursor left
// on a hidden or filtered-away server, or on nothing when every server is
// hidden, must not let space, h or d act on a row the user cannot see.
func (p *picker) current() (catalog.Server, bool) {
	if p.cursor < 0 || p.cursor >= len(p.cat.Servers) || !slices.Contains(p.visible(), p.cursor) {
		return catalog.Server{}, false
	}
	return p.cat.Servers[p.cursor], true
}

// Update is the only place picker state changes; View just draws it, as Bubble
// Tea expects. Scrolling follows the cursor after every message.
func (p *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cursor, mode := p.cursor, p.mode
	model, cmd := p.update(msg)
	p.afterMove(cursor, mode)
	p.scroll()
	if p.mode == modeProfiles {
		p.scrollProfiles()
	}
	if p.mode == modeConfirmTrust || p.mode == modeConfirmCatalog {
		p.scrollTrust()
	}
	return model, cmd
}

// scroll keeps the cursor inside the visible window.
func (p *picker) scroll() {
	vis := p.visible()
	pos := 0
	for i, idx := range vis {
		if idx == p.cursor {
			pos = i
		}
	}
	height := p.listHeight()
	if pos < p.offset {
		p.offset = pos
	}
	if pos >= p.offset+height {
		p.offset = pos - height + 1
	}
	if p.offset > len(vis)-height {
		p.offset = len(vis) - height
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p *picker) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		return p, nil
	case measuredMsg:
		delete(p.measuring, m.name)
		p.cache.Put(m.name, m.spec, m.res)
		if m.res.Err != "" {
			// The name is the catalog's and the error the server's: spelled
			// out before the status line draws them.
			p.status = styErr.Render(trust.Safe(m.name + ": " + m.res.Err))
		} else if len(p.measuring) == 0 {
			// The rows the press left out are still left out: the note
			// that said so stays, or "measured" would read as everything.
			p.status = "measured" + p.measureNote()
		}
		if m.note != "" {
			p.status = styErr.Render(trust.Safe(m.note))
		}
		return p, nil
	case tea.KeyPressMsg:
		k := m.String()
		if p.stray(k) {
			p.held = nil // the keys of the burst held so far go with it
			return p, nil
		}
		if p.hold && (len(p.held) > 0 || (p.commandMode() && printable(k))) {
			return p, p.holdKey(k)
		}
		return p.dispatch(k)
	case releaseMsg:
		return p.release(m)
	case tea.PasteMsg:
		p.paste(m.Content)
		return p, nil
	}
	return p, nil
}

// dispatch acts on a key that is for the picker.
func (p *picker) dispatch(k string) (tea.Model, tea.Cmd) {
	wasCommand := p.commandMode()
	model, cmd := p.key(k)
	if wasCommand && !p.commandMode() {
		p.promptAt = p.clock() // this key opened a text input
	}
	return model, cmd
}

func (p *picker) key(k string) (tea.Model, tea.Cmd) {
	switch p.mode {
	case modeList:
		return p.updateList(k)
	case modeConfirmYAML:
		return p.updateConfirmYAML(k)
	case modeConfirmJSON:
		return p.updateConfirmJSON(k)
	case modeFilter:
		return p.updateFilter(k)
	case modeProfiles:
		return p.updateProfiles(k)
	case modeConfirmTrust:
		return p.updateConfirmTrust(k)
	case modeConfirmCatalog:
		return p.updateConfirmCatalog(k)
	case modeMoveTo:
		return p.updateMoveTo(k)
	case modeMoveSecrets:
		return p.updateMoveSecrets(k)
	default:
		return p.updateAdd(k)
	}
}

func (p *picker) moveCursor(delta int) {
	vis := p.visible()
	if len(vis) == 0 {
		return
	}
	// The Hidden section follows the active rows, so vis is not in catalog
	// order: the cursor's place is found, never inferred from its index.
	pos := slices.Index(vis, p.cursor)
	if pos < 0 {
		p.cursorToNearest() // onto a row first, then move from there
		pos = slices.Index(vis, p.cursor)
	}
	pos += delta
	if pos < 0 {
		pos = 0
	}
	if pos >= len(vis) {
		pos = len(vis) - 1
	}
	p.cursor = vis[pos]
}

func (p *picker) updateList(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		// With a filter kept, the first esc drops it; the next one quits.
		if p.filter != "" {
			p.filter = ""
			return p, nil
		}
		return p, tea.Quit
	case "ctrl+c", "q":
		return p, tea.Quit
	case "enter":
		p.launch = true
		return p, tea.Quit
	case "up", "k":
		p.moveCursor(-1)
	case "down", "j":
		p.moveCursor(1)
	case "pgup", "ctrl+u":
		p.moveCursor(-p.listHeight())
	case "pgdown", "ctrl+d":
		p.moveCursor(p.listHeight())
	case "g", "home":
		if vis := p.visible(); len(vis) > 0 {
			p.cursor = vis[0]
		}
	case "G", "end":
		if vis := p.visible(); len(vis) > 0 {
			p.cursor = vis[len(vis)-1]
		}
	case " ", "space", "x":
		if s, ok := p.current(); ok {
			p.toggle(s)
		}
	case "a":
		skipped := 0
		for _, i := range p.visible() {
			s := p.cat.Servers[i]
			if p.disabled(s) {
				skipped++ // checking one changes Claude Code's settings: never in bulk
				continue
			}
			if p.hidden[s.Name] {
				continue // hidden means "not for this project": only by hand
			}
			p.sel[s.Name] = true
		}
		p.status = "all enabled"
		if skipped > 0 {
			p.status += fmt.Sprintf(" (%d disabled in Claude Code left unchecked: check them one by one)", skipped)
		}
		if n := p.hiddenCount(); n > 0 {
			p.status += fmt.Sprintf(" (%d hidden left unchecked)", n)
		}
	case "n":
		// Hidden rows too, folded or not: "none" that left a hidden server
		// loading would be the silent kind of surprise hiding must not cause.
		for _, s := range p.cat.Servers {
			if p.matches(s) {
				delete(p.sel, s.Name)
			}
		}
		p.status = "all disabled"
	case "h":
		p.hideKey()
	case "H", "shift+h":
		p.toggleHiddenSection()
	case "/":
		p.mode = modeFilter
		p.status = ""
	case "m":
		return p.measureKey()
	case "M", "shift+m":
		return p.measureAllKey()
	case "T", "shift+t":
		return p.trustCatalogKey()
	case "p", "l":
		p.openProfiles(pmNone)
	case "s":
		p.openProfiles(pmAdd) // the new profile starts as this selection
	case "+", "A":
		p.mode = modeAddName
		p.input = ""
		p.newSrv = catalog.Server{Origin: catalog.OriginProject, Spec: map[string]any{}}
		p.status = ""
	case "d":
		if s, ok := p.current(); ok {
			p.input = ""
			p.status = ""
			if s.Origin == catalog.OriginPlugin {
				p.status = trust.Safe(s.Name) + " comes from a plugin; disable the plugin in Claude Code to remove it"
				return p, nil
			}
			if s.Origin == catalog.OriginProject {
				p.mode = modeConfirmYAML
			} else {
				p.mode = modeConfirmJSON
			}
		}
	case "v":
		p.moveKey()
	}
	return p, nil
}

// measureServers probes every server, now: pressing m again measures again.
// Each server is its own command so the list fills in as answers arrive.
// With only set, just those servers are probed and the rest keep their
// numbers; nil is a full press.
func (p *picker) measureServers(only map[string]bool) tea.Cmd {
	var cmds []tea.Cmd
	// Claude Code's tokens are read once per press, by whichever server gets
	// there first: on a Mac each read goes through the Keychain.
	var once sync.Once
	var claude oauth.ClaudeTokens
	claudeNames := p.cat.ClaudeNames()
	if only == nil || p.tr.skipped == nil {
		p.tr.skipped = map[string]trust.Verdict{}
	}
	p.refreshCatalog()
	untrusted, unchecked, hidden, changed := 0, 0, 0, false
	for _, s := range p.cat.Servers {
		if p.measuring[s.Name] || (only != nil && !only[s.Name]) {
			continue
		}
		// A hidden server is out of the way, measurement included: its
		// command does not run and its cost is not fetched while its row is
		// folded away. Unfold the section (H) to measure it with the rest.
		if only == nil && !p.measurable(s) {
			hidden++
			continue
		}
		delete(p.cache.Entries, s.Name)
		// A stdio server runs a command of its author's choosing, and a
		// remote one the repository defines gets your environment expanded
		// into its URL and headers. trust.Gate says which may run on this
		// press, and which only onto a public address; the rest are marked
		// skipped, with the reason on their row.
		v := trust.Gate(p.scope(), s, p.sel[s.Name])
		if !v.Run {
			p.tr.skipped[s.Name] = v
			if v.NeedsTrust {
				untrusted++
			} else {
				unchecked++
				changed = changed || v.CatalogChanged
			}
			continue
		}
		delete(p.tr.skipped, s.Name)
		p.measuring[s.Name] = true
		// Written is the URL as the catalog has it, so an error names
		// `?key=${K}`, never the expanded value; Raw puts back any
		// expanded value a server echoes in its error.
		name, sp, opt := s.Name, s.Spec, mcp.Options{PublicOnly: v.PublicOnly, Written: spec.ViewOf(s.Spec).URL, Raw: s.Spec}
		cmds = append(cmds, func() tea.Msg {
			expanded, err := spec.Expand(sp, p.uid)
			if err != nil {
				return measuredMsg{name: name, spec: sp,
					res: mcp.Result{Name: name, Err: err.Error()}}
			}
			m, _ := expanded.(map[string]any)
			// Gate looked at the spec as written; a URL that expanded to
			// nothing would run the entry's command instead, unseen.
			if err := trust.Effective(sp, m); err != nil {
				return measuredMsg{name: name, spec: sp,
					res: mcp.Result{Name: name, Err: err.Error()}}
			}
			// Tokens from `mcpick login` go along, as they do at launch;
			// without them an authorised server would still show 401.
			one := spec.Selection{Names: []string{name}, Specs: map[string]map[string]any{name: m}}
			_, attachErr := oauth.Attach(one)
			// Then Claude Code's own token, if it has one for this server
			// and nothing more specific was set.
			once.Do(func() { claude = oauth.LoadClaudeTokens() })
			auth := claude.Attach(one, claudeNames)[name]
			res := mcp.ProbeWith(context.Background(), name, m, 15*time.Second, opt)
			res.Auth = auth
			if auth == oauth.AuthClaudeExpired && !res.OK {
				res.Err = oauth.ExplainExpired(res.Err)
			}
			msg := measuredMsg{name: name, spec: sp, res: res}
			if attachErr != nil {
				msg.note = attachErr.Error()
			}
			return msg
		})
	}
	note := skipNote(untrusted, unchecked, changed) + hiddenLeftOut(hidden)
	if len(cmds) == 0 {
		p.status = "nothing to measure" + note
		return nil
	}
	p.status = fmt.Sprintf("measuring %d server(s)…", len(cmds)) + note
	return tea.Batch(cmds...)
}

// updateFilter edits the filter in place: the list narrows with every key,
// and the arrows keep moving through what is left. Enter keeps the filter and
// returns to the list; esc drops it.
func (p *picker) updateFilter(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "ctrl+c":
		p.filter = ""
		p.mode = modeList
	case "enter":
		p.mode = modeList
	case "up", "down", "pgup", "pgdown":
		return p.updateList(k)
	case "backspace":
		if p.filter != "" {
			r := []rune(p.filter)
			p.filter = string(r[:len(r)-1])
		}
	case "space":
		p.filter += " "
	default:
		if len([]rune(k)) == 1 {
			p.filter += k
		}
	}
	p.keepCursorVisible()
	return p, nil
}

// keepCursorVisible moves the cursor onto the first match when the filter
// has hidden the row it was on.
func (p *picker) keepCursorVisible() {
	vis := p.visible()
	for _, i := range vis {
		if i == p.cursor {
			return
		}
	}
	if len(vis) > 0 {
		p.cursor = vis[0]
	}
}

func (p *picker) removeCurrent() {
	s, ok := p.current()
	if !ok {
		return
	}
	delete(p.sel, s.Name)
	p.cat.Servers = append(p.cat.Servers[:p.cursor], p.cat.Servers[p.cursor+1:]...)
	if p.cursor >= len(p.cat.Servers) && p.cursor > 0 {
		p.cursor--
	}
	p.cursorToNearest() // the row that moved up under it may be hidden
}

// projectFile is the catalog file a project server was read from. Both
// .mcp.yaml and .mcp.json are read, and new servers go to the first; one
// defined in the other has to be deleted where it is.
func projectFile(cat *catalog.Catalog, s catalog.Server) string {
	if s.Source != "" {
		return s.Source
	}
	return cat.Path
}

func (p *picker) updateConfirmYAML(k string) (tea.Model, tea.Cmd) {
	s, ok := p.current()
	if !ok {
		p.mode = modeList
		return p, nil
	}
	switch k {
	case "y", "Y":
		file := projectFile(p.cat, s)
		note, err := p.editCatalog(func() ([]string, error) {
			if err := catalog.DeleteServer(file, s.Name); err != nil {
				return nil, err
			}
			p.removeCurrent()
			return nil, nil
		})
		if err != nil {
			p.status = styErr.Render("delete failed: " + trust.Safe(err.Error()))
		} else {
			p.status = "removed " + trust.Safe(s.Name) + " from " + fsutil.ShortenHome(file) + note
		}
		p.mode = modeList
	default:
		p.mode = modeList
	}
	return p, nil
}

func (p *picker) updateConfirmJSON(k string) (tea.Model, tea.Cmd) {
	s, ok := p.current()
	if !ok {
		p.mode = modeList
		return p, nil
	}
	switch k {
	case "esc", "ctrl+c":
		p.mode = modeList
		p.input = ""
	case "enter":
		if p.input != "yes" {
			p.status = "cancelled"
			p.mode = modeList
			p.input = ""
			return p, nil
		}
		err := catalog.DeleteFromClaudeJSON(p.cat.ClaudePath, p.cat.ProjectKey, s.Name, s.Origin)
		if err != nil {
			p.status = styErr.Render("delete failed: " + trust.Safe(err.Error()))
		} else {
			p.removeCurrent()
			p.status = "removed " + trust.Safe(s.Name) + " from ~/.claude.json (backup written)"
		}
		p.mode = modeList
		p.input = ""
	case "backspace":
		p.input = dropLastRune(p.input)
	default:
		if printable(k) && k != "space" {
			p.input += k
		}
	}
	return p, nil
}

func (p *picker) updateAdd(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "ctrl+c":
		p.mode = modeList
		p.input = ""
		return p, nil
	case "backspace":
		p.input = dropLastRune(p.input)
		return p, nil
	case "enter":
		return p.advanceAdd()
	default:
		if printable(k) {
			if k == "space" {
				k = " "
			}
			p.input += k
		}
		return p, nil
	}
}

func (p *picker) advanceAdd() (tea.Model, tea.Cmd) {
	val := strings.TrimSpace(p.input)
	switch p.mode {
	case modeAddName:
		if val == "" {
			p.status = "name cannot be empty"
			return p, nil
		}
		// A name the catalog has anywhere — its own file, ~/.claude.json,
		// a plugin — is refused: written, it would duplicate the row and
		// write over the entry the first file had.
		if s, ok := p.cat.Find(val); ok {
			p.status = styErr.Render(fmt.Sprintf("%s already exists (%s); pick another name", trust.Safe(val), s.Origin))
			return p, nil
		}
		p.newSrv.Name = val
		p.input = "http"
		p.mode = modeAddType
	case modeAddType:
		if val != "http" && val != "sse" && val != "stdio" {
			p.status = "type must be http, sse or stdio"
			return p, nil
		}
		p.newSrv.Spec["type"] = val
		p.input = ""
		p.mode = modeAddTarget
	case modeAddTarget:
		if val == "" {
			p.status = "target cannot be empty"
			return p, nil
		}
		if p.newSrv.Spec["type"] == "stdio" {
			fields := strings.Fields(val)
			p.newSrv.Spec["command"] = fields[0]
			if len(fields) > 1 {
				args := make([]any, 0, len(fields)-1)
				for _, f := range fields[1:] {
					args = append(args, f)
				}
				p.newSrv.Spec["args"] = args
			}
		} else {
			p.newSrv.Spec["url"] = val
		}
		note, err := p.editCatalog(func() ([]string, error) {
			if err := catalog.AddServer(p.cat.Path, p.newSrv.Name, p.newSrv.Spec); err != nil {
				return nil, err
			}
			at := p.groupTotal(catalog.OriginProject)
			p.cat.Servers = append(p.cat.Servers, catalog.Server{})
			copy(p.cat.Servers[at+1:], p.cat.Servers[at:])
			p.cat.Servers[at] = p.newSrv
			p.sel[p.newSrv.Name] = true
			p.cursor = at
			return []string{p.newSrv.Name}, nil
		})
		if err != nil {
			p.status = styErr.Render("add failed: " + trust.Safe(err.Error()))
		} else {
			p.status = "added " + trust.Safe(p.newSrv.Name) + " to " + fsutil.ShortenHome(p.cat.Path) + note
		}
		p.input = ""
		p.mode = modeList
	}
	return p, nil
}

// groupLabel is a group's heading: Claude Code's name for the scope.
func (p *picker) groupLabel(origin string) string {
	switch origin {
	case catalog.OriginProject:
		return "Project"
	case catalog.OriginLocal:
		return "Local"
	case catalog.OriginPlugin:
		return "Plugins"
	default:
		return "User"
	}
}

func (p *picker) groupPath(origin string) string {
	switch origin {
	case catalog.OriginProject:
		return fsutil.ShortenHome(p.cat.Path)
	case catalog.OriginLocal:
		return fsutil.ShortenHome(p.cat.ClaudePath) + " -> projects[" + p.cat.ProjectKey + "]"
	case catalog.OriginPlugin:
		return "enabled Claude Code plugins (read-only)"
	default:
		return fsutil.ShortenHome(p.cat.ClaudePath)
	}
}

// groupTag follows the heading: the Project group says `trusted` while
// the project's catalog trust is in force (T), since it changes what m does
// to every remote server under it.
func (p *picker) groupTag(origin string) string {
	if origin == catalog.OriginProject && p.tr.catalog == trust.CatalogTrusted {
		return "  " + styDim.Render("trusted")
	}
	return ""
}

func (p *picker) groupTotal(origin string) int {
	n := 0
	for _, s := range p.cat.Servers {
		if s.Origin == origin {
			n++
		}
	}
	return n
}

func (p *picker) groupCount(origin string) int {
	n := 0
	for _, s := range p.cat.Servers {
		if s.Origin == origin && p.sel[s.Name] {
			n++
		}
	}
	return n
}

func (p *picker) selectedTokens() (int, bool) {
	total, complete := 0, true
	for _, s := range p.cat.Servers {
		if !p.sel[s.Name] {
			continue
		}
		m, ok := p.cache.Get(s.Name, s.Spec)
		if !ok || !m.OK {
			complete = false
			continue
		}
		total += m.Tokens
	}
	return total, complete
}

// listHeight is how many rows the server list may occupy: the window minus
// everything else the frame draws (see fixedLines), capped by max_rows.
func (p *picker) listHeight() int {
	return p.windowRows(p.height-p.fixedLines(p.layout()), len(p.visible()))
}

// groupHeadings is how many section headings the list draws.
func (p *picker) groupHeadings() int {
	seen := map[string]bool{}
	for _, i := range p.visible() {
		seen[p.section(p.cat.Servers[i])] = true
	}
	if !p.showHidden && p.hiddenCount() > 0 {
		seen[sectionHidden] = true // the folded section still has its heading
	}
	return len(seen)
}

func (p *picker) View() tea.View {
	var content string
	switch p.mode {
	case modeProfiles:
		content = p.viewProfiles().Content
	case modeConfirmTrust:
		content = p.trustScreen()
	case modeConfirmCatalog:
		content = p.catalogScreen()
	default:
		content = p.listView()
	}
	v := tea.NewView(fitFrame(content, p.width, p.height))
	// The alternate screen, as a full-screen program uses: drawn inline, a
	// frame as tall as the window (the profile screen) scrolled the terminal,
	// and what scrolled off could not be erased when a shorter frame
	// followed — the header was left repeated above the list. On exit the
	// terminal returns to where it was, and the agent starts on a clean line.
	v.AltScreen = true
	return v
}

func (p *picker) listView() string {
	var b strings.Builder
	vis := p.visible()
	l := p.layout()

	fmt.Fprintf(&b, "%s\n", p.header())
	if p.showCommand() {
		fmt.Fprintf(&b, "%s\n", p.commandLine())
	}
	if line := p.filterLine(); line != "" {
		fmt.Fprintf(&b, "%s\n", line)
	}

	for _, w := range p.cat.Warnings {
		fmt.Fprintf(&b, "%s\n", styWarn.Render("! "+trust.Safe(w)))
	}

	switch {
	case len(p.cat.Servers) == 0:
		b.WriteString(l.blank() + styDim.Render("  (empty — press + to add one)") + "\n")
	case len(vis) == 0 && p.filter == "":
		b.WriteString(l.blank() + styDim.Render("  (every server is hidden — H shows them)") + "\n")
	case len(vis) == 0:
		b.WriteString(l.blank() + styDim.Render("  (no server matches "+p.filter+")") + "\n")
	}

	width := 0
	for _, i := range vis {
		if n := len(p.cat.Servers[i].Name); n > width {
			width = n
		}
	}

	height := p.listHeight()
	end := p.offset + height
	if end > len(vis) {
		end = len(vis)
	}

	last := ""
	for n, i := range vis[p.offset:end] {
		s := p.cat.Servers[i]
		if sec := p.section(s); sec != last && l.headings {
			if sec == sectionHidden {
				fmt.Fprintf(&b, "%s%s\n", l.blank(), p.hiddenHeading())
			} else {
				fmt.Fprintf(&b, "%s%s %s%s\n", l.blank(), styGroup.Render(p.groupLabel(s.Origin)),
					styDim.Render(fmt.Sprintf("• %d/%d selected  %s",
						p.groupCount(s.Origin), p.groupTotal(s.Origin), p.groupPath(s.Origin))), p.groupTag(s.Origin))
			}
			last = sec
		}
		if n == 0 {
			if above := p.moreAbove(); above != "" {
				fmt.Fprintf(&b, "%s\n", above) // under the first heading, where the rows resume
			}
		}
		pointer := "  "
		if i == p.cursor {
			pointer = styCursor.Render("> ")
		}
		// The box is the selection and nothing else; what launching will do
		// with a server disabled in Claude Code is said in words beside it.
		box, tag := p.boxFor(s)
		tag += p.hiddenTag(s)
		cost := "       "
		if m, ok := p.cache.Get(s.Name, s.Spec); ok {
			if m.OK {
				cost = fmt.Sprintf("%7s", mcp.HumanTokens(m.Tokens))
				cost = styCost.Render(cost)
			} else {
				cost = styErr.Render(fmt.Sprintf("%7s", mcp.ErrorKind(m.Err)))
			}
		} else if p.measuring[s.Name] {
			cost = styDim.Render("    ···")
		} else if v, skipped := p.tr.skipped[s.Name]; skipped {
			cost = skipMark(v)
		}
		// Name and address are the catalog's text: an escape in them is
		// shown, not obeyed, like on the trust screen.
		endpoint := styDim.Render(truncate(trust.Safe(s.Endpoint()), max(16, p.width-width-24-lipgloss.Width(tag))))
		fmt.Fprintf(&b, "%s%s %-*s %s  %s%s\n", pointer, box, width, trust.Safe(s.Name), cost, tag, endpoint)
	}

	if below := p.moreBelow(end, len(vis)); below != "" {
		fmt.Fprintf(&b, "%s\n", below)
	}
	if !p.showHidden && p.hiddenCount() > 0 && l.headings {
		fmt.Fprintf(&b, "%s%s\n", l.blank(), p.hiddenHeading())
	}

	b.WriteString(l.blank())
	switch p.mode {
	case modeConfirmYAML:
		s, _ := p.current()
		fmt.Fprintf(&b, "%s\n", styWarn.Render(
			fmt.Sprintf("delete %q from %s? [y/N]", s.Name, fsutil.ShortenHome(projectFile(p.cat, s)))))
	case modeConfirmJSON:
		s, _ := p.current()
		fmt.Fprintf(&b, "%s\n", styWarn.Render(fmt.Sprintf(
			"%q lives in ~/.claude.json (%s). Deleting it affects EVERY session",
			s.Name, s.Origin)))
		fmt.Fprintf(&b, "%s\n", styWarn.Render(
			"sharing that file, not just this one. A backup is written first."))
		fmt.Fprintf(&b, "type 'yes' to confirm, esc to cancel: %s\n", p.input)
	case modeAddName:
		fmt.Fprintf(&b, "new server name: %s\n", p.input)
	case modeAddType:
		fmt.Fprintf(&b, "type (http/sse/stdio): %s\n", p.input)
	case modeAddTarget:
		if p.newSrv.Spec["type"] == "stdio" {
			fmt.Fprintf(&b, "command line: %s\n", p.input)
		} else {
			fmt.Fprintf(&b, "url: %s\n", p.input)
		}
	case modeMoveTo:
		fmt.Fprintf(&b, "%s\n", p.moveToLine())
	case modeMoveSecrets:
		for _, line := range p.moveSecretsLines() {
			fmt.Fprintf(&b, "%s\n", line)
		}
	default:
		if l.legend {
			b.WriteString(p.legend() + "\n")
		}
		if l.hints {
			for _, line := range strings.Split(p.hint(), "\n") {
				b.WriteString(styDim.Render(line) + "\n")
			}
		}
	}
	if why := p.armedDetail(); why != "" {
		fmt.Fprintf(&b, "%s\n", why) // the command a second m would run, over any status
	} else if p.status != "" {
		fmt.Fprintf(&b, "%s\n", p.status)
	} else if why := p.cursorDetail(); why != "" {
		fmt.Fprintf(&b, "%s\n", why)
	}

	return b.String()
}

// header is the first line: the agent's badge, the count, the context
// total, then what the user must not miss — hidden servers still checked,
// nothing checked — then the uid when --uid was given, and the version
// last, since a narrow terminal cuts the line from the right.
func (p *picker) header() string {
	count := 0
	for _, s := range p.cat.Servers {
		if p.sel[s.Name] {
			count++
		}
	}
	parts := []string{styDim.Render(fmt.Sprintf("%d/%d selected", count, len(p.cat.Servers)))}
	if tokens, complete := p.selectedTokens(); tokens > 0 {
		suffix := ""
		if !complete {
			suffix = "+"
		}
		parts = append(parts, styDim.Render(fmt.Sprintf("~%s%s context", mcp.HumanTokens(tokens), suffix)))
	}
	if note := p.hiddenNote(); note != "" {
		parts = append(parts, styWarn.Render(note))
	}
	if note := p.nothingChecked(); note != "" {
		parts = append(parts, styWarn.Render(note))
	}
	if p.uidGiven {
		parts = append(parts, styDim.Render("uid="+p.uid))
	}
	title := "mcpick"
	if p.version != "" {
		title += " " + p.version
	}
	parts = append(parts, styDim.Render(title))
	head := strings.Join(parts, styDim.Render(" · "))
	if badge := p.badge(); badge != "" {
		return badge + "  " + head
	}
	return styTitle.Render("mcpick") + "  " + head
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// cursorDetail explains the last measurement of the server under the cursor.
// A failure gets the full message — the list only has room for a word — and
// the command that would fix a missing login. A success says where its
// credentials came from, when mcpick had to find some.
func (p *picker) cursorDetail() string {
	s, ok := p.current()
	if !ok {
		return ""
	}
	if why := p.unconfirmedDetail(s); why != "" {
		return why
	}
	if why := p.skipDetail(s); why != "" {
		return why
	}
	return p.detailFor(s)
}

func (p *picker) detailFor(s catalog.Server) string {
	m, ok := p.cache.Get(s.Name, s.Spec)
	if !ok {
		return ""
	}
	if m.OK {
		if m.Auth == "" {
			return ""
		}
		return styDim.Render(truncate(trust.Safe(fmt.Sprintf("%s: %s · auth: %s", s.Name, mcp.Tools(m.Tools), m.Auth)), max(20, p.width)))
	}
	if m.Err == "" {
		return ""
	}
	msg := s.Name + ": " + m.Err
	if mcp.NeedsLogin(m.Err) && m.Auth != oauth.AuthClaudeExpired {
		msg += " — try: mcpick login " + s.Name
	}
	// The name comes from the catalog and the error from the server: either
	// can carry terminal escapes.
	return styErr.Render(truncate(trust.Safe(msg), max(20, p.width)))
}

// legend explains the marks in the list on one line, drawn in the same
// styles as the marks themselves so the eye can match them. It fits the
// terminal: entries go in order of how puzzling their mark is, first with
// their full wording, then with a short one, and whatever does not fit is
// left out rather than wrapped.
//
// The cost and the box are always explained; the other marks only while a
// row shows one, so the legend never explains what is not on the screen.
func (p *picker) legend() string {
	type entry struct{ mark, long, short string }
	var failed, untrusted, unchecked, measuring bool
	for _, s := range p.cat.Servers {
		if m, ok := p.cache.Get(s.Name, s.Spec); ok {
			failed = failed || !m.OK
		} else if p.measuring[s.Name] {
			measuring = true
		} else if v, ok := p.tr.skipped[s.Name]; ok {
			untrusted = untrusted || v.NeedsTrust
			unchecked = unchecked || !v.NeedsTrust
		}
	}
	var entries []entry
	if failed {
		entries = append(entries, entry{styErr.Render("401"), " HTTP error", " error"})
	}
	entries = append(entries,
		entry{styCost.Render("4.2k"), " context cost", " cost"},
		entry{styOn.Render("[x]"), " loads", " on"})
	if untrusted {
		entries = append(entries, entry{styWarn.Render("run?"), " runs a command: m m", " m m"})
	}
	if unchecked {
		entries = append(entries, entry{styDim.Render("ask"), " sends your env: tick it or T", " tick or T"})
	}
	if measuring {
		entries = append(entries, entry{styDim.Render("···"), " measuring", " measuring"})
	}
	sep := styDim.Render(" · ")
	label := styTitle.Render("Legend:") + " "
	build := func(n int, short bool) string {
		parts := make([]string, 0, n)
		for _, e := range entries[:n] {
			text := e.long
			if short {
				text = e.short
			}
			parts = append(parts, e.mark+styDim.Render(text))
		}
		return label + strings.Join(parts, sep)
	}
	// Every mark in long words, else every mark in short ones; only then
	// are marks dropped, from the end.
	if line := build(len(entries), false); lipgloss.Width(line) <= p.width {
		return line
	}
	for n := len(entries); n > 1; n-- {
		if line := build(n, true); lipgloss.Width(line) <= p.width {
			return line
		}
	}
	return build(1, true)
}

// hint is the key line under the legend. The keys keep their order; on a
// narrow terminal the least needed go first — T, v, H, M, then d and +,
// then movement, then n and a — so that at any width the
// line still says how to toggle, launch and abort, rather than being cut
// where the width happens to fall. T's label follows the catalog trust
// (trustKey).
func (p *picker) hint() string {
	type entry struct {
		text string
		rank int // lower is kept longer
	}
	entries := []entry{
		{"↑↓ move", 9}, {"space toggle", 2}, {"/ filter", 3}, {"m measure", 4},
		{"M review commands", 12}, {"a all", 7}, {"n none", 8}, {"h hide", 6},
		{"H hidden", 13}, {"p profiles", 5}, {"+ add", 10}, {"d delete", 11},
		{"v move", 14}, {p.trustKey(), 15}, {"enter launch", 0}, {"q abort", 1},
	}
	all := make([]string, len(entries))
	for i, e := range entries {
		all[i] = e.text
	}
	// Every key, wrapped over as many as three lines, so a terminal of
	// ordinary width still shows + add, d delete, v move and T; only when
	// three lines are not enough are keys dropped, least needed first.
	if lines, ok := wrapParts(all, p.width, 3); ok {
		return lines
	}
	for keep := len(entries); keep > 0; keep-- {
		parts := make([]string, 0, keep)
		for _, e := range entries {
			if e.rank < keep {
				parts = append(parts, e.text)
			}
		}
		if line := strings.Join(parts, " · "); lipgloss.Width(line) <= p.width {
			return line
		}
	}
	return truncate("enter launch", p.width)
}

// wrapParts packs parts, in order, into lines of at most width columns,
// breaking only at a separator; ok is false when it takes more than maxLines
// lines or a single part is wider than the line.
func wrapParts(parts []string, width, maxLines int) (string, bool) {
	var lines []string
	cur := ""
	for _, part := range parts {
		if lipgloss.Width(part) > width {
			return "", false
		}
		next := part
		if cur != "" {
			next = cur + " · " + part
		}
		if lipgloss.Width(next) <= width {
			cur = next
			continue
		}
		lines = append(lines, cur)
		cur = part
	}
	lines = append(lines, cur)
	return strings.Join(lines, "\n"), len(lines) <= maxLines
}

// badge names the agent in its own colours, with its glyph. An agent mcpick
// has no adapter for is drawn plainly and says so: only MCPICK_CONFIG is set,
// and the user should know the selection may not reach it.
func (p *picker) badge() string {
	if p.tgt == nil {
		return ""
	}
	in := p.tgt.Info()
	if in.Color == "" {
		return styWarn.Render(in.Glyph + " " + firstArg(p.argv) + " (unknown agent)")
	}
	fg := lipgloss.Color("#FFFFFF")
	if lightBrand[in.Name] {
		fg = lipgloss.Color("#000000")
	}
	return lipgloss.NewStyle().Bold(true).
		Background(lipgloss.Color(in.Color)).Foreground(fg).Padding(0, 1).
		Render(in.Glyph + " " + in.Name)
}

// lightBrand are colours dark text reads better on.
var lightBrand = map[string]bool{"grok": true}

func firstArg(argv []string) string {
	if len(argv) == 0 {
		return "?"
	}
	return filepath.Base(argv[0])
}

// showCommand says whether there is room for the command line. On a very
// short terminal the list matters more.
func (p *picker) showCommand() bool {
	return p.tgt != nil && len(p.argv) > 0 && p.height >= 16
}

// commandLine shows what will actually run once the user presses enter:
// the agent's command line with whatever mcpick adds, the variables it sets,
// and what it changes on disk. It follows the selection, because for some
// agents the command itself names the servers. Wrappers that start mcpick
// hide the real command; this is where it becomes visible.
func (p *picker) commandLine() string { return p.commandLineFor(p.selectedNames()) }

// commandLineFor previews the launch for a given selection, in catalog order.
func (p *picker) commandLineFor(names []string) string {
	if p.tgt == nil || len(p.argv) == 0 {
		return ""
	}
	sel := spec.Selection{Names: names, Specs: map[string]map[string]any{}}
	pv := p.tgt.Preview(sel, p.argv)
	parts := append(append([]string{}, pv.Env...), proc.ShellQuote(pv.Argv)...)
	line := "→ " + strings.Join(parts, " ")
	if pv.Note != "" {
		line += "  (" + pv.Note + ")"
	}
	return styDim.Render(truncate(trust.Safe(line), max(20, p.width)))
}

// toggle checks or unchecks a server.
func (p *picker) toggle(s catalog.Server) {
	p.sel[s.Name] = !p.sel[s.Name]
}

// disabled reports a server Claude Code will not load. Only an agent that
// reads Claude's settings cares; for any other it is an ordinary server.
func (p *picker) disabled(s catalog.Server) bool {
	return s.Disabled && p.tgt != nil && p.tgt.Info().ClaudeSettings
}

// boxFor draws a row's box and, for a server disabled in Claude Code, what
// launching will do with it: nothing while unchecked, re-enable it in Claude
// Code once checked.
func (p *picker) boxFor(s catalog.Server) (box, tag string) {
	switch {
	case p.disabled(s) && p.sel[s.Name]:
		return styOn.Render("[x]"), styOn.Render("will be enabled in Claude Code  ")
	case p.disabled(s):
		return "[ ]", styWarn.Render("disabled in Claude Code  ")
	case p.sel[s.Name]:
		return styOn.Render("[x]"), ""
	}
	return "[ ]", ""
}

// filterLine shows the filter where it acts, above the list: while typing,
// with a cursor; once kept, as a reminder that rows are hidden.
func (p *picker) filterLine() string {
	if p.mode != modeFilter && p.filter == "" {
		return ""
	}
	count := fmt.Sprintf("%d of %d", len(p.visible()), len(p.cat.Servers))
	if p.mode == modeFilter {
		return styCursor.Render("/ ") + p.filter + styCursor.Render("▏") + "  " +
			styDim.Render(count+" · enter keep · esc clear")
	}
	return styCursor.Render("/ ") + p.filter + "  " + styDim.Render(count+" · / edit · esc clear")
}
