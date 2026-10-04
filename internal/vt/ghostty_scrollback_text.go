//go:build ghostty

package vt

import (
	"math"
	"strings"
	"unicode/utf8"

	gh "go.mitchellh.com/libghostty"
)

// AppendScrollbackText writes the main screen's history to buf as plain text,
// oldest line first, each line followed by a newline: what ScrollbackRows and
// uv.Line.String give for every line.
//
// The text comes from the library's formatter. Reading the history line by
// line goes through one GridRef per cell, which for a full 10000-line history
// at 80 columns took about 200 ms, under the terminal's lock. A pending
// wait-for window-output captures that often, so on a flooding pane the lock
// was held most of the time and the flood ran 200 times slower. The formatter
// and the checks below take a few milliseconds.
//
// The formatter trims every trailing space, and uv.Line.String keeps a
// trailing space that has a style or a link. So a row that may end in one is
// read cell by cell, as before: a row whose VT form prints a space while an
// SGR attribute is set after its last other character, and a row the library
// says holds a hyperlink. The formatter also leaves out blank rows at the end
// of the history, which are read back one by one. When its text does not
// line up with the rows, the whole history is read line by line instead, so a
// change in the library's output cannot shift lines.
func (t *GhosttyTerminal) AppendScrollbackText(buf *strings.Builder) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	src, from, end := t.historyRangeLocked(0, math.MaxInt)
	if src == nil {
		return
	}
	if text, ok := t.historyTextLocked(src, end); ok {
		buf.Grow(len(text) + 1 + t.height*(t.width+1))
		buf.WriteString(text)
		buf.WriteByte('\n')
		return
	}
	for i := from; i < end; i++ {
		buf.WriteString(t.readHistoryLineLocked(src, i).String())
		buf.WriteByte('\n')
	}
}

// historyTextLocked is the first n history lines of src as plain text, joined
// by newlines with no newline at the end. ok is false when the library failed
// or its text does not line up with the rows.
func (t *GhosttyTerminal) historyTextLocked(src *gh.Terminal, n int) (string, bool) {
	if n <= 0 || t.width <= 0 {
		return "", false
	}
	start, err := src.GridRef(gh.Point{Tag: gh.PointTagHistory, X: 0, Y: 0})
	if err != nil || start == nil {
		return "", false
	}
	end, err := src.GridRef(gh.Point{Tag: gh.PointTagHistory, X: uint16(t.width - 1), Y: uint32(n - 1)})
	if err != nil || end == nil {
		return "", false
	}
	sel := &gh.Selection{Start: *start, End: *end}
	text, err := src.SelectionFormatString(gh.WithSelection(sel), gh.WithSelectionTrim(true), gh.WithSelectionUnwrap(false))
	if err != nil {
		return "", false
	}
	styled, err := src.SelectionFormatString(gh.WithSelection(sel), gh.WithSelectionTrim(true), gh.WithSelectionUnwrap(false),
		gh.WithSelectionFormat(gh.FormatterFormatVT))
	if err != nil {
		return "", false
	}
	exact, ok := styledTailRows(styled, n)
	if !ok {
		return "", false
	}
	got := strings.Count(text, "\n") + 1
	if got > n {
		return "", false
	}
	for i := range n {
		if exact[i] {
			continue
		}
		ref, err := src.GridRef(gh.Point{Tag: gh.PointTagHistory, Y: uint32(i)})
		if err != nil || ref == nil {
			return "", false
		}
		row, err := ref.Row()
		if err != nil || row == nil {
			return "", false
		}
		if link, err := row.Hyperlink(); err != nil || link {
			exact[i] = true
		}
	}
	// The rows the formatter left out are read one by one. They must be
	// blank: anything else means its lines are not the rows.
	for i := got; i < n; i++ {
		if t.readHistoryLineLocked(src, i).String() != "" {
			return "", false
		}
		exact[i] = false
	}
	var lines []string
	for i := range got {
		if !exact[i] {
			continue
		}
		if lines == nil {
			lines = strings.Split(text, "\n")
		}
		lines[i] = t.readHistoryLineLocked(src, i).String()
	}
	if lines != nil {
		text = strings.Join(lines, "\n")
	}
	return text + strings.Repeat("\n", n-got), true
}

// styledTailRows reads the VT form of n history rows, rows ended by CR LF, and
// marks each row that prints a space while an SGR attribute is set, after the
// row's last other character. Such a row may end in a space that has a style,
// which the plain text has trimmed. The SGR state carries from row to row, as
// it does in the stream. It is conservative: any SGR but a plain reset counts
// as set. ok is false when the text holds more than n rows.
func styledTailRows(vt string, n int) (marks []bool, ok bool) {
	marks = make([]bool, n)
	row, set, tail := 0, false, false
	for i := 0; i < len(vt); {
		c := vt[i]
		switch {
		case c == '\r' && i+1 < len(vt) && vt[i+1] == '\n':
			if row >= n {
				return nil, false
			}
			marks[row] = tail
			row, tail = row+1, false
			i += 2
		case c == 0x1b:
			i = skipVTEscape(vt, i, &set)
		case c == ' ':
			if set {
				tail = true
			}
			i++
		default:
			_, size := utf8.DecodeRuneInString(vt[i:])
			tail = false
			i += size
		}
	}
	if row >= n {
		return nil, false
	}
	marks[row] = tail
	return marks, true
}

// skipVTEscape returns the index past the escape sequence at vt[i]. An SGR
// sequence sets *set: false for a plain reset, true for anything else.
func skipVTEscape(vt string, i int, set *bool) int {
	if i+1 >= len(vt) {
		return len(vt)
	}
	switch vt[i+1] {
	case '[':
		j := i + 2
		for j < len(vt) && (vt[j] < 0x40 || vt[j] > 0x7e) {
			j++
		}
		if j >= len(vt) {
			return len(vt)
		}
		if vt[j] == 'm' {
			params := vt[i+2 : j]
			*set = params != "" && params != "0"
		}
		return j + 1
	case ']', 'P', '_', '^', 'X':
		// A string runs to BEL or ST.
		for j := i + 2; j < len(vt); j++ {
			if vt[j] == 0x07 {
				return j + 1
			}
			if vt[j] == 0x1b && j+1 < len(vt) && vt[j+1] == '\\' {
				return j + 2
			}
		}
		return len(vt)
	default:
		return i + 2
	}
}
