package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The picker draws inline, below the prompt, and redraws by moving the cursor
// up as many lines as it drew. A line the terminal wraps is one line more than
// that, and every redraw then leaves the top line behind: the header repeated
// up the screen. Each part of the frame is meant to fit already; this is the
// guarantee for whatever does not — a long error on the status line, a group
// heading with a long path.

// fitFrame cuts every line of a frame to width columns and the frame to
// height lines. A zero size (not known yet) leaves it alone.
func fitFrame(content string, width, height int) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	if width > 0 {
		for i, l := range lines {
			if ansi.StringWidth(l) > width {
				lines[i] = ansi.Truncate(l, width, "…") + ansi.ResetStyle
			}
		}
	}
	return strings.Join(lines, "\n")
}
