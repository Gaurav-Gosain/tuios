package session

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

// Unwrapped and trimmed captures.
//
// A capture has one line per terminal row. A line longer than the pane comes
// back cut into rows, so a path, a URL or a stack frame that wrapped is split,
// and a search over the capture misses it. unwrap joins the rows the terminal
// soft-wrapped, the ones autowrap carried on, back into the line the program
// wrote. A row that ends because the program wrote a newline stays a line of
// its own. Both backends record which rows wrapped (RowSoftWrapped and
// ScrollbackSoftWrapped), and a backend that cannot tell for a row reads as a
// row that ends, which leaves it as it was.
//
// trim drops the blank rows below the last line with content: a capture of a
// quiet pane otherwise ends in every empty row under the cursor.

// unwrapRows joins the soft-wrapped rows of lines. lines[i] is absolute row
// first+i, where absolute rows count the history oldest first and then the
// screen. A row that wrapped filled the pane to its edge, so the spaces the
// capture trimmed from its end are put back before the next row is joined:
// they were text. A row that wrapped a column early, because a wide character
// did not fit in the last column, gets one space less.
//
// main says the screen rows of lines were read from the main screen, as
// last-command-output reads them, rather than from the active screen. The
// history always belongs to the main screen, and its newest row carries on to
// the main screen's first row. So while the alternate screen is up, a capture
// of the active screen must not join that row to the alternate screen's first
// row, and a read of the main screen cannot use the screen's wrap flags, which
// describe the alternate screen. Both read as rows that end.
func unwrapRows(term vt.Terminal, lines []string, first int, main bool) []string {
	if term == nil || len(lines) < 2 {
		return lines
	}
	sbLen, width := term.ScrollbackLen(), term.Width()
	alt := term.ActiveScreenIsAlt()
	wrapped := func(abs int) (bool, bool) {
		if abs < sbLen {
			if alt && !main && abs == sbLen-1 {
				return false, false
			}
			w, known := term.ScrollbackSoftWrapped(abs)
			return known && w, term.ScrollbackPadded(abs)
		}
		if alt && main {
			return false, false
		}
		w, known := term.RowSoftWrapped(abs - sbLen)
		return known && w, term.RowPadded(abs - sbLen)
	}
	out := make([]string, 0, len(lines))
	var cur strings.Builder
	for i, line := range lines {
		cur.WriteString(line)
		if i < len(lines)-1 {
			if w, padded := wrapped(first + i); w {
				fill := width - ansi.StringWidth(line)
				if padded {
					fill--
				}
				if fill > 0 {
					cur.WriteString(strings.Repeat(" ", fill))
				}
				continue
			}
		}
		out = append(out, cur.String())
		cur.Reset()
	}
	return out
}

// unwrapCapture is unwrapRows over a capture's content: the history rows
// when history is set, then the screen rows. A content whose line count is
// not the pane's row count is returned as it is: its lines cannot be matched
// to the rows the wrap flags describe.
func unwrapCapture(term vt.Terminal, content string, history bool) string {
	if term == nil {
		return content
	}
	lines := strings.Split(content, "\n")
	first := term.ScrollbackLen()
	if history {
		first = 0
	}
	if len(lines) != term.ScrollbackLen()-first+term.Height() {
		return content
	}
	return strings.Join(unwrapRows(term, lines, first, false), "\n")
}

// trimCapture drops the blank lines at the end of a capture. A line is blank
// when it holds nothing but spaces and escape sequences. The result ends with
// a newline unless it is empty.
func trimCapture(content string) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
