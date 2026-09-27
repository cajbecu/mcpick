package tui

import (
	"fmt"
	"strings"
)

// The list is a window of at most max_rows server rows (config.yaml, default
// 10), or fewer when the terminal is short, that follows the cursor. What is
// out of view is counted at the edges: "↑ N more" under the first heading in
// view, "↓ N more" after the last row.

// windowRows is how many server rows fit: the terminal's share capped by
// max_rows, less the two edge lines once the list has to scroll at all.
// Both are reserved as soon as one may show, so the frame never grows past
// the window as the cursor moves.
func (p *picker) windowRows(share int, total int) int {
	rows := share
	if p.maxRows > 0 && rows > p.maxRows {
		rows = p.maxRows
	}
	if total > rows {
		rows = min(share-2, p.maxRows)
		if p.maxRows <= 0 {
			rows = share - 2
		}
	}
	return max(rows, 1)
}

// moreAbove and moreBelow are the edge lines, or "" when nothing is out of
// view on that side.
func (p *picker) moreAbove() string {
	if p.offset <= 0 {
		return ""
	}
	return styDim.Render(fmt.Sprintf("  ↑ %d more", p.offset))
}

func (p *picker) moreBelow(end, total int) string {
	if end >= total {
		return ""
	}
	return styDim.Render(fmt.Sprintf("  ↓ %d more", total-end))
}

// layout says which optional parts of the frame are drawn. A terminal too
// short for the whole frame around one server row loses them in order: the
// blank lines first, then the legend, then the key hints, last the headings
// (and the folded Hidden reminder with them). The header — it carries the
// "N hidden checked" warning — the cursor row and the status line are never
// dropped; a terminal shorter than those still overflows.
type layout struct {
	blanks, legend, hints, headings bool
}

// blank is the separator before a heading or the footer: a line, or nothing
// when the frame is squeezed.
func (l layout) blank() string {
	if l.blanks {
		return "\n"
	}
	return ""
}

func (p *picker) layout() layout {
	l := layout{blanks: true, legend: true, hints: true, headings: true}
	edges := 0
	if len(p.visible()) > 1 {
		edges = 2 // windowRows reserves both edge lines once the list may scroll
	}
	for _, drop := range []func(*layout){
		func(l *layout) { l.blanks = false },
		func(l *layout) { l.legend = false },
		func(l *layout) { l.hints = false },
		func(l *layout) { l.headings = false },
	} {
		if p.fixedLines(l)+1+edges <= p.height {
			break
		}
		drop(&l)
	}
	return l
}

// fixedLines is how many lines the frame takes around the list: everything
// View draws except the server rows and the edge lines.
func (p *picker) fixedLines(l layout) int {
	n := 1 + 1 // header; status or the cursor's detail
	if p.showCommand() {
		n++
	}
	if p.filterLine() != "" {
		n++
	}
	n += len(p.cat.Warnings)
	blank := 0
	if l.blanks {
		blank = 1
	}
	if len(p.visible()) == 0 {
		n += blank + 1 // the "(empty ...)" line in place of the rows
	}
	if l.headings {
		n += p.groupHeadings() * (blank + 1)
	}
	n += blank // before the footer
	switch p.mode {
	case modeList:
		if l.legend {
			n++
		}
		if l.hints {
			n += strings.Count(p.hint(), "\n") + 1 // one line, or two
		}
	case modeConfirmJSON:
		n += 3
	default:
		n++
	}
	return n
}
