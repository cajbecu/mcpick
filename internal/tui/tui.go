// Package tui is the picker: a checkbox list of the catalog with a context-cost
// estimate beside every server.
package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/oauth"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/target"
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
	modeSaveProfile
	modeLoadProfile
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
}

type picker struct {
	cat     *catalog.Catalog
	sel     map[string]bool
	cursor  int
	offset  int
	mode    mode
	uid     string
	version string
	tgt     target.Target
	argv    []string
	input   string
	filter  string
	newSrv  catalog.Server
	status  string
	launch  bool

	width, height int
	cache         *mcp.Cache
	measuring     map[string]bool
}

// Options describe the launch the picker is choosing servers for.
type Options struct {
	UID string
	// Version is shown in the header, so a screenshot or a bug report says
	// which build it came from.
	Version string
	// Target and Argv are the agent and its command line; the header shows
	// the agent and the exact command that will run.
	Target target.Target
	Argv   []string
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
	// A server disabled in Claude Code is checked only by an explicit press:
	// checking it re-enables it in Claude Code for good, so a selection
	// saved by an earlier run must not do that on a bare enter.
	for _, s := range cat.Servers {
		if s.Disabled {
			delete(sel, s.Name)
		}
	}
	p := &picker{
		cat: cat, sel: sel, uid: opts.UID, version: opts.Version, tgt: opts.Target, argv: opts.Argv,
		width: 80, height: 24,
		// Measurements start empty every time: a number or an error on screen
		// is always from this session's `m`, never a leftover from an
		// earlier run that may no longer be true.
		cache:     &mcp.Cache{Entries: map[string]mcp.Measurement{}},
		measuring: map[string]bool{},
	}
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
		if s.Disabled && res.sel[s.Name] {
			reenable[s.Name] = true
		}
	}
	return Result{Selected: res.sel, Reenable: reenable}, nil
}

func (p *picker) Init() tea.Cmd { return nil }

// visible is the list of catalog indices passing the current filter.
func (p *picker) visible() []int {
	out := make([]int, 0, len(p.cat.Servers))
	needle := strings.ToLower(p.filter)
	for i, s := range p.cat.Servers {
		if needle == "" ||
			strings.Contains(strings.ToLower(s.Name), needle) ||
			strings.Contains(strings.ToLower(s.Endpoint()), needle) {
			out = append(out, i)
		}
	}
	return out
}

func (p *picker) current() (catalog.Server, bool) {
	if p.cursor < 0 || p.cursor >= len(p.cat.Servers) {
		return catalog.Server{}, false
	}
	return p.cat.Servers[p.cursor], true
}

// Update is the only place picker state changes; View just draws it, as Bubble
// Tea expects. Scrolling follows the cursor after every message.
func (p *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := p.update(msg)
	p.scroll()
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
			p.status = styErr.Render(m.name + ": " + m.res.Err)
		} else if len(p.measuring) == 0 {
			p.status = "measured"
		}
		return p, nil
	case tea.KeyPressMsg:
		return p.key(m.String())
	}
	return p, nil
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
	case modeSaveProfile, modeLoadProfile:
		return p.updateProfile(k)
	default:
		return p.updateAdd(k)
	}
}

func (p *picker) moveCursor(delta int) {
	vis := p.visible()
	if len(vis) == 0 {
		return
	}
	pos := 0
	for i, idx := range vis {
		if idx == p.cursor {
			pos = i
			break
		}
		if idx > p.cursor {
			pos = i
			break
		}
		pos = i
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
	case "ctrl+c", "q", "esc":
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
			if s.Disabled {
				skipped++ // checking one changes Claude Code's settings: never in bulk
				continue
			}
			p.sel[s.Name] = true
		}
		p.status = "all enabled"
		if skipped > 0 {
			p.status += fmt.Sprintf(" (%d disabled in Claude Code left unchecked: check them one by one)", skipped)
		}
	case "n":
		for _, i := range p.visible() {
			delete(p.sel, p.cat.Servers[i].Name)
		}
		p.status = "all disabled"
	case "/":
		p.mode = modeFilter
		p.input = p.filter
		p.status = ""
	case "m":
		return p, p.measureCmd()
	case "s":
		p.mode = modeSaveProfile
		p.input = ""
		p.status = ""
	case "l":
		if len(p.cat.Profiles) == 0 {
			p.status = "no profiles in the catalog"
			return p, nil
		}
		p.mode = modeLoadProfile
		p.input = ""
		p.status = "profiles: " + strings.Join(spec.SortedKeys(p.cat.Profiles), ", ")
	case "+", "A":
		p.mode = modeAddName
		p.input = ""
		p.newSrv = catalog.Server{Origin: catalog.OriginWorkspace, Spec: map[string]any{}}
		p.status = ""
	case "d":
		if s, ok := p.current(); ok {
			p.input = ""
			p.status = ""
			if s.Origin == catalog.OriginPlugin {
				p.status = s.Name + " comes from a plugin; disable the plugin in Claude Code to remove it"
				return p, nil
			}
			if s.Origin == catalog.OriginWorkspace {
				p.mode = modeConfirmYAML
			} else {
				p.mode = modeConfirmJSON
			}
		}
	}
	return p, nil
}

// measureCmd probes every server, now: pressing m again measures again.
// Each server is its own command so the list fills in as answers arrive.
func (p *picker) measureCmd() tea.Cmd {
	var cmds []tea.Cmd
	// Claude Code's tokens are read once per press, by whichever server gets
	// there first: on a Mac each read goes through the Keychain.
	var once sync.Once
	var claude oauth.ClaudeTokens
	claudeNames := p.cat.ClaudeNames()
	skipped := 0
	for _, s := range p.cat.Servers {
		if p.measuring[s.Name] {
			continue
		}
		delete(p.cache.Entries, s.Name)
		// A server this repository defines runs a command or contacts a
		// host of its author's choosing, with your environment expanded
		// into it. It is measured once you check it — the same consent a
		// launch asks for — and not on a bare m.
		if s.Origin == catalog.OriginWorkspace && !p.sel[s.Name] {
			skipped++
			continue
		}
		p.measuring[s.Name] = true
		name, sp := s.Name, s.Spec
		cmds = append(cmds, func() tea.Msg {
			expanded, err := spec.Expand(sp, p.uid)
			if err != nil {
				return measuredMsg{name: name, spec: sp,
					res: mcp.Result{Name: name, Err: err.Error()}}
			}
			m, _ := expanded.(map[string]any)
			// Tokens from `mcpick login` go along, as they do at launch;
			// without them an authorised server would still show 401.
			one := spec.Selection{Names: []string{name}, Specs: map[string]map[string]any{name: m}}
			oauth.Attach(one)
			// Then Claude Code's own token, if it has one for this server
			// and nothing more specific was set.
			once.Do(func() { claude = oauth.LoadClaudeTokens() })
			auth := claude.Attach(one, claudeNames)[name]
			res := mcp.Probe(context.Background(), name, m, 15*time.Second)
			res.Auth = auth
			if auth == oauth.AuthClaudeExpired && !res.OK {
				res.Err = oauth.ExplainExpired(res.Err)
			}
			return measuredMsg{name: name, spec: sp, res: res}
		})
	}
	note := ""
	if skipped > 0 {
		note = fmt.Sprintf(" (%d from this repository's catalog skipped: check them to measure)", skipped)
	}
	if len(cmds) == 0 {
		p.status = "nothing to measure" + note
		return nil
	}
	p.status = fmt.Sprintf("measuring %d server(s)…", len(cmds)) + note
	return tea.Batch(cmds...)
}

func (p *picker) updateFilter(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "ctrl+c":
		p.filter = ""
		p.mode = modeList
	case "enter":
		p.filter = p.input
		p.mode = modeList
		if vis := p.visible(); len(vis) > 0 {
			p.cursor = vis[0]
		}
	case "backspace":
		if p.input != "" {
			p.input = p.input[:len(p.input)-1]
		}
	default:
		if len(k) == 1 {
			p.input += k
		}
	}
	return p, nil
}

func (p *picker) updateProfile(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "ctrl+c":
		p.mode = modeList
		p.input = ""
	case "enter":
		name := strings.TrimSpace(p.input)
		p.input = ""
		if name == "" {
			p.mode = modeList
			return p, nil
		}
		if p.mode == modeSaveProfile {
			var names []string
			for _, s := range p.cat.Servers {
				if p.sel[s.Name] {
					names = append(names, s.Name)
				}
			}
			if err := catalog.SaveProfile(p.cat.Path, name, names); err != nil {
				p.status = styErr.Render("save failed: " + err.Error())
			} else {
				p.cat.Profiles[name] = names
				p.status = fmt.Sprintf("profile %q saved to %s", name, fsutil.ShortenHome(p.cat.Path))
			}
		} else {
			names, ok := p.cat.Profiles[name]
			if !ok {
				p.status = styErr.Render("no profile " + name)
			} else {
				p.sel = map[string]bool{}
				for _, n := range names {
					p.sel[n] = true
				}
				p.status = fmt.Sprintf("profile %q loaded", name)
			}
		}
		p.mode = modeList
	case "backspace":
		if p.input != "" {
			p.input = p.input[:len(p.input)-1]
		}
	default:
		if len(k) == 1 {
			p.input += k
		}
	}
	return p, nil
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
}

func (p *picker) updateConfirmYAML(k string) (tea.Model, tea.Cmd) {
	s, ok := p.current()
	if !ok {
		p.mode = modeList
		return p, nil
	}
	switch k {
	case "y", "Y":
		if err := catalog.DeleteServer(p.cat.Path, s.Name); err != nil {
			p.status = styErr.Render("delete failed: " + err.Error())
		} else {
			p.removeCurrent()
			p.status = "removed " + s.Name + " from " + fsutil.ShortenHome(p.cat.Path)
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
			p.status = styErr.Render("delete failed: " + err.Error())
		} else {
			p.removeCurrent()
			p.status = "removed " + s.Name + " from ~/.claude.json (backup written)"
		}
		p.mode = modeList
		p.input = ""
	case "backspace":
		if p.input != "" {
			p.input = p.input[:len(p.input)-1]
		}
	default:
		if len(k) == 1 {
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
		if p.input != "" {
			p.input = p.input[:len(p.input)-1]
		}
		return p, nil
	case "enter":
		return p.advanceAdd()
	default:
		if len(k) == 1 || k == "space" {
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
		if err := catalog.AddServer(p.cat.Path, p.newSrv.Name, p.newSrv.Spec); err != nil {
			p.status = styErr.Render("add failed: " + err.Error())
		} else {
			at := p.groupTotal(catalog.OriginWorkspace)
			p.cat.Servers = append(p.cat.Servers, catalog.Server{})
			copy(p.cat.Servers[at+1:], p.cat.Servers[at:])
			p.cat.Servers[at] = p.newSrv
			p.sel[p.newSrv.Name] = true
			p.cursor = at
			p.status = "added " + p.newSrv.Name + " to " + fsutil.ShortenHome(p.cat.Path)
		}
		p.input = ""
		p.mode = modeList
	}
	return p, nil
}

func (p *picker) groupLabel(origin string) string {
	switch origin {
	case catalog.OriginWorkspace:
		return "Workspace"
	case catalog.OriginProject:
		return "Project"
	case catalog.OriginPlugin:
		return "Plugins"
	default:
		return "Global"
	}
}

func (p *picker) groupPath(origin string) string {
	switch origin {
	case catalog.OriginWorkspace:
		return fsutil.ShortenHome(p.cat.Path)
	case catalog.OriginProject:
		return fsutil.ShortenHome(p.cat.ClaudePath) + " -> projects[" + p.cat.ProjectKey + "]"
	case catalog.OriginPlugin:
		return "enabled Claude Code plugins (read-only)"
	default:
		return fsutil.ShortenHome(p.cat.ClaudePath)
	}
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

// listHeight is how many rows the server list may occupy: the window minus the
// header, the footer and the group headings that will be drawn.
func (p *picker) listHeight() int {
	h := p.height - 6 - p.groupHeadings() // header, legend, keys, status, margins
	if p.showCommand() {
		h-- // the command line under the header
	}
	if h < 3 {
		return 3
	}
	return h
}

func (p *picker) groupHeadings() int {
	seen := map[string]bool{}
	for _, i := range p.visible() {
		seen[p.cat.Servers[i].Origin] = true
	}
	return len(seen) * 2
}

func (p *picker) View() tea.View {
	var b strings.Builder
	vis := p.visible()

	count := 0
	for _, s := range p.cat.Servers {
		if p.sel[s.Name] {
			count++
		}
	}
	head := fmt.Sprintf("uid=%s · %d/%d selected", p.uid, count, len(p.cat.Servers))
	if tokens, complete := p.selectedTokens(); tokens > 0 {
		suffix := ""
		if !complete {
			suffix = "+"
		}
		head += fmt.Sprintf(" · ~%s%s context", mcp.HumanTokens(tokens), suffix)
	}
	if badge := p.badge(); badge != "" {
		head = styDim.Render(head) + " " + badge
	} else {
		head = styDim.Render(head)
	}
	title := "mcpick"
	if p.version != "" {
		title += " " + p.version
	}
	fmt.Fprintf(&b, "%s %s\n", styTitle.Render(title), head)
	if p.showCommand() {
		fmt.Fprintf(&b, "%s\n", p.commandLine())
	}

	for _, w := range p.cat.Warnings {
		fmt.Fprintf(&b, "%s\n", styWarn.Render("! "+w))
	}

	switch {
	case len(p.cat.Servers) == 0:
		b.WriteString("\n" + styDim.Render("  (empty — press + to add one)") + "\n")
	case len(vis) == 0:
		b.WriteString("\n" + styDim.Render("  (no server matches "+p.filter+")") + "\n")
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
	for _, i := range vis[p.offset:end] {
		s := p.cat.Servers[i]
		if s.Origin != last {
			fmt.Fprintf(&b, "\n%s %s\n", styGroup.Render(p.groupLabel(s.Origin)),
				styDim.Render(fmt.Sprintf("• %d/%d selected  %s",
					p.groupCount(s.Origin), p.groupTotal(s.Origin), p.groupPath(s.Origin))))
			last = s.Origin
		}
		pointer := "  "
		if i == p.cursor {
			pointer = styCursor.Render("> ")
		}
		// The box is the selection and nothing else; what launching will do
		// with a server disabled in Claude Code is said in words beside it.
		box, tag := p.boxFor(s)
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
		}
		endpoint := styDim.Render(truncate(s.Endpoint(), max(16, p.width-width-24-lipgloss.Width(tag))))
		fmt.Fprintf(&b, "%s%s %-*s %s  %s%s\n", pointer, box, width, s.Name, cost, tag, endpoint)
	}

	if len(vis) > height {
		fmt.Fprintf(&b, "%s\n", styDim.Render(
			fmt.Sprintf("  … %d-%d of %d", p.offset+1, end, len(vis))))
	}

	b.WriteString("\n")
	switch p.mode {
	case modeConfirmYAML:
		s, _ := p.current()
		fmt.Fprintf(&b, "%s\n", styWarn.Render(
			fmt.Sprintf("delete %q from %s? [y/N]", s.Name, fsutil.ShortenHome(p.cat.Path))))
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
	case modeFilter:
		fmt.Fprintf(&b, "filter: %s\n", p.input)
	case modeSaveProfile:
		fmt.Fprintf(&b, "save selection as profile: %s\n", p.input)
	case modeLoadProfile:
		fmt.Fprintf(&b, "load profile: %s\n", p.input)
	default:
		hint := "↑↓ move · space toggle · / filter · m measure · a all · n none · s save · l load · + add · d delete · enter launch · q abort"
		if p.filter != "" {
			hint = "filtered by " + p.filter + " · esc in filter clears · " + hint
		}
		b.WriteString(p.legend() + "\n")
		b.WriteString(styDim.Render(truncate(hint, p.width)) + "\n")
	}
	if p.status != "" {
		fmt.Fprintf(&b, "%s\n", p.status)
	} else if why := p.cursorDetail(); why != "" {
		fmt.Fprintf(&b, "%s\n", why)
	}

	return tea.NewView(b.String())
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
	m, ok := p.cache.Get(s.Name, s.Spec)
	if !ok {
		return ""
	}
	if m.OK {
		if m.Auth == "" {
			return ""
		}
		return styDim.Render(truncate(fmt.Sprintf("%s: %d tools · auth: %s", s.Name, m.Tools, m.Auth), max(20, p.width)))
	}
	if m.Err == "" {
		return ""
	}
	msg := s.Name + ": " + m.Err
	if mcp.NeedsLogin(m.Err) && m.Auth != oauth.AuthClaudeExpired {
		msg += " — try: mcpick login " + s.Name
	}
	return styErr.Render(truncate(msg, max(20, p.width)))
}

// legend explains the marks in the list on one line, drawn in the same
// styles as the marks themselves so the eye can match them. It fits the
// terminal: entries go in order of how puzzling their mark is, first with
// their full wording, then with a short one, and whatever does not fit is
// left out rather than wrapped.
func (p *picker) legend() string {
	type entry struct{ mark, long, short string }
	entries := []entry{
		{styErr.Render("401"), " check failed", " failed"},
		{styCost.Render("4.2k"), " context cost", " cost"},
		{styOn.Render("[x]"), " loads", " on"},
		{styDim.Render("···"), " measuring", " measuring"},
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
	for _, short := range []bool{false, true} {
		for n := len(entries); n > 0; n-- {
			if line := build(n, short); lipgloss.Width(line) <= p.width {
				if n >= 3 || short {
					return line
				}
				break // too few with long words: try the short ones
			}
		}
	}
	return build(1, true)
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
func (p *picker) commandLine() string {
	if p.tgt == nil || len(p.argv) == 0 {
		return ""
	}
	sel := spec.Selection{Specs: map[string]map[string]any{}}
	for _, s := range p.cat.Servers {
		if p.sel[s.Name] {
			sel.Names = append(sel.Names, s.Name)
		}
	}
	pv := p.tgt.Preview(sel, p.argv)
	parts := append(append([]string{}, pv.Env...), proc.ShellQuote(pv.Argv)...)
	line := "→ " + strings.Join(parts, " ")
	if pv.Note != "" {
		line += "  (" + pv.Note + ")"
	}
	return styDim.Render(truncate(line, max(20, p.width)))
}

// toggle checks or unchecks a server.
func (p *picker) toggle(s catalog.Server) {
	p.sel[s.Name] = !p.sel[s.Name]
}

// boxFor draws a row's box and, for a server disabled in Claude Code, what
// launching will do with it: nothing while unchecked, re-enable it in Claude
// Code once checked.
func (p *picker) boxFor(s catalog.Server) (box, tag string) {
	switch {
	case s.Disabled && p.sel[s.Name]:
		return styOn.Render("[x]"), styOn.Render("will be enabled in Claude Code  ")
	case s.Disabled:
		return "[ ]", styWarn.Render("disabled in Claude Code  ")
	case p.sel[s.Name]:
		return styOn.Render("[x]"), ""
	}
	return "[ ]", ""
}
