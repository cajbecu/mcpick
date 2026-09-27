package tui

// Text that was never meant for the picker: a prompt pasted into the terminal
// while the picker is up, or typed before its first frame. Read as keys, the
// letters are commands — n unchecks everything, s opens the save prompt, the
// rest becomes a profile name. The picker takes keys, not text, so text is
// told apart from keys three ways: the terminal's bracketed paste, a grace
// period after start, and a burst no hand could type.

import (
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

const (
	// startupGrace is how long after start keys are still typeahead — typed
	// at the shell, or at the agent the user thought was already running —
	// and not aimed at the picker, which had not drawn yet.
	startupGrace = 250 * time.Millisecond
	// burstKeys printable keys within burstWindow is a paste, not typing:
	// a hand manages perhaps fifteen a second, key repeat thirty.
	burstKeys   = 8
	burstWindow = 40 * time.Millisecond

	pastedStatus = "ignored pasted text — the picker takes keys, not text"
	earlyStatus  = "ignored keys typed before the picker was ready"
)

// clock is the time; a test sets its own so the windows are deterministic.
func (p *picker) clock() time.Time {
	if p.now == nil {
		return time.Now()
	}
	return p.now()
}

// commandMode says whether a printable key is a command here, as opposed to
// a character of what is being typed.
func (p *picker) commandMode() bool {
	switch p.mode {
	case modeFilter, modeAddName, modeAddType, modeAddTarget, modeConfirmJSON, modeMoveSecrets:
		return false
	case modeProfiles:
		return p.pm.sub == pmNone || p.pm.sub == pmConfirmDelete
	}
	return true
}

// dropLastRune is backspace: one character off the end, not one byte, so
// that ș or 日 goes whole instead of leaving half an encoding behind.
func dropLastRune(s string) string {
	if r := []rune(s); len(r) > 0 {
		return string(r[:len(r)-1])
	}
	return s
}

// printable says k is a character, not a control or a named key.
func printable(k string) bool {
	return k == "space" || len([]rune(k)) == 1
}

// stray says whether a key press is not for the picker and was dropped: it
// came inside the grace period after start, or during a burst of printable
// keys too fast to be typed — every key of it, the enter of a pasted line
// included. The burst is counted in every mode, so that one which opened a
// prompt with its first keys (s) is still seen; when it is, a prompt opened
// inside the burst is closed again, since the paste opened it, while a paste
// into a prompt the user opened is text and kept.
func (p *picker) stray(k string) bool {
	now := p.clock()
	if !p.started.IsZero() && now.Sub(p.started) < startupGrace {
		p.status = earlyStatus
		return true
	}
	keep := p.recent[:0]
	for _, t := range p.recent {
		if now.Sub(t) <= burstWindow {
			keep = append(keep, t)
		}
	}
	p.recent = keep
	if printable(k) {
		p.recent = append(p.recent, now)
		if len(p.recent) > burstKeys+1 {
			p.recent = p.recent[len(p.recent)-burstKeys-1:] // only "more than burstKeys" matters
		}
	}
	if len(p.recent) <= burstKeys {
		return false
	}
	if !p.commandMode() {
		if now.Sub(p.promptAt) > burstWindow {
			return false // a paste into a prompt the user opened: text, not commands
		}
		p.closePrompt()
	}
	p.status = pastedStatus
	return true
}

// releaseMsg ends the wait of the key held as number gen, and of every
// key held before it.
type releaseMsg struct{ gen int }

// heldKey is a command key waiting out burstWindow, numbered in arrival
// order.
type heldKey struct {
	key string
	gen int
}

// holdKey keeps a command key back for burstWindow. The burst detector needs
// burstKeys keys to be sure, and by then the first of them would have acted:
// n unchecked everything, h written the hidden set, d and y deleted a server.
// Held, they act only once no burst followed them; a burst drops them all.
// Each key has a deadline of its own, burstWindow after it arrived: a key
// held down repeats every 30 ms or so, and a wait that restarted with every
// repeat froze the cursor until the key was let go. The keys still act in
// order, since a later key's deadline is later. A person does not notice
// 40 ms.
func (p *picker) holdKey(k string) tea.Cmd {
	p.heldGen++
	gen := p.heldGen
	p.held = append(p.held, heldKey{key: k, gen: gen})
	return tea.Tick(burstWindow, func(time.Time) tea.Msg { return releaseMsg{gen} })
}

// release runs the held keys up to and including the one whose deadline
// this is, if a burst has not dropped them since; a tick for a key already
// released does nothing.
func (p *picker) release(m releaseMsg) (tea.Model, tea.Cmd) {
	n := 0
	for n < len(p.held) && p.held[n].gen <= m.gen {
		n++
	}
	if n == 0 {
		return p, nil
	}
	keys := p.held[:n]
	p.held = append([]heldKey(nil), p.held[n:]...)
	var cmds []tea.Cmd
	for _, h := range keys {
		_, cmd := p.dispatch(h.key)
		cmds = append(cmds, cmd)
	}
	return p, tea.Batch(cmds...)
}

// closePrompt leaves whatever text input is open, as esc would, dropping what
// the burst typed into it.
func (p *picker) closePrompt() {
	p.input = ""
	switch p.mode {
	case modeProfiles:
		p.pm.sub, p.pm.input = pmNone, ""
	case modeFilter:
		p.filter = ""
		p.mode = modeList
	default:
		p.mode = modeList
	}
}

// paste is a bracketed paste: text into a text input, ignored anywhere keys
// are commands.
func (p *picker) paste(content string) {
	if p.commandMode() {
		p.status = pastedStatus
		return
	}
	text := pasteText(content)
	switch p.mode {
	case modeFilter:
		p.filter += text
		p.keepCursorVisible()
	case modeProfiles:
		p.pm.input += text
	default:
		p.input += text
	}
}

// pasteText is what of a paste goes into a one-line input: its first line,
// without control characters.
func pasteText(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
