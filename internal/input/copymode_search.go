package input

import (
	"bytes"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Search-related functions for copy mode (/, ?, n, N, etc.)

// maxSearchMatches bounds how many matches one search keeps. See
// terminal.MaxSearchMatches.
const maxSearchMatches = terminal.MaxSearchMatches

// searchBlock is how many history lines the search reads at a time. The
// history is read oldest first and searched newest first, so a block is read
// and then walked backwards.
const searchBlock = 256

// searchLine is one buffer line as the search sees it: the text of its cells,
// with the column each byte came from.
//
// The text is each cell's content in column order, without the spacer a wide
// character leaves in the column after it, with an empty or blank cell as a
// space and an image cell as a space. Lower-cased when the search ignores
// case, a rune at a time, so the bytes of the text stay matched to the cells
// they came from. A match's columns are then read from the cells, which is
// right for a wide character and for a cell of several runes, where counting
// runes as columns was not.
type searchLine struct {
	text []byte
	// cell is, for each byte of text, the index in cols of the cell it is
	// from.
	cell []int32
	// cols is each cell's column, then one more entry: the column after the
	// last cell, where a match that ends on the last cell ends.
	cols []int32
	fold bool
	// scratch holds a screen cell's content while it is added.
	scratch []byte
}

func (l *searchLine) reset(fold bool) {
	l.text, l.cell, l.cols = l.text[:0], l.cell[:0], l.cols[:0]
	l.fold = fold
}

// add appends the cell at column col.
func (l *searchLine) add(content []byte, width, col int) {
	if width == 0 {
		// A wide character's spacer: no text of its own.
		return
	}
	k := int32(len(l.cols))
	l.cols = append(l.cols, int32(col))
	if len(content) == 0 || bytes.HasPrefix(content, sixelLead) {
		l.text = append(l.text, ' ')
		l.cell = append(l.cell, k)
		return
	}
	for i := 0; i < len(content); {
		c := content[i]
		if c < utf8.RuneSelf {
			if l.fold && 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			l.text = append(l.text, c)
			i++
		} else {
			r, size := utf8.DecodeRune(content[i:])
			if l.fold {
				r = unicode.ToLower(r)
			}
			l.text = utf8.AppendRune(l.text, r)
			i += size
		}
		for len(l.cell) < len(l.text) {
			l.cell = append(l.cell, k)
		}
	}
}

// sixelLead starts the content of an image cell, which the search reads as
// a space. See vt.IsSixelMarker.
var sixelLead = []byte(vt.SixelMarkerLead)

// end closes the line: next is the column after the last cell added.
func (l *searchLine) end(next int) {
	l.cols = append(l.cols, int32(next))
}

// space is the content of a blank cell.
var space = []byte{' '}

// fromScreen reads row y of the active screen.
func (l *searchLine) fromScreen(term vt.Terminal, y int, fold bool) {
	l.reset(fold)
	width := term.Width()
	for x := range width {
		if c := term.CellAt(x, y); c != nil {
			l.scratch = append(l.scratch[:0], c.Content...)
			l.add(l.scratch, c.Width, x)
		} else {
			l.add(space, 1, x)
		}
	}
	l.end(width)
}

// fromHistory reads a history line given as text cells. The columns past
// the cells are blank, and only pad of them are added: a match runs into the
// blank tail no further than the query is long, unless the query is nothing
// but spaces.
func (l *searchLine) fromHistory(cells []vt.TextCell, width, pad int, fold bool) {
	l.reset(fold)
	for x := range cells {
		l.add(cells[x].Content, cells[x].Width, x)
	}
	next := len(cells)
	for ; next < width && pad > 0; next, pad = next+1, pad-1 {
		l.add(space, 1, next)
	}
	// next is the first blank column not added, or the width: either way
	// the column after the last cell added.
	l.end(next)
}

// find appends every occurrence of query in the line, left to right and not
// overlapping, to out.
func (l *searchLine) find(query []byte, absLine int, out []terminal.SearchMatch) []terminal.SearchMatch {
	at := 0
	for at <= len(l.text) {
		idx := bytes.Index(l.text[at:], query)
		if idx < 0 {
			break
		}
		start := at + idx
		stop := start + len(query)
		out = append(out, terminal.SearchMatch{
			Line:   absLine,
			StartX: int(l.cols[l.cell[start]]),
			EndX:   int(l.cols[l.cell[stop-1]+1]),
		})
		at = stop
	}
	return out
}

// searchPad is how many blank columns past a history line's stored cells the
// search adds: as many as the query has runes, or all of them for a query of
// spaces only, which can match anywhere in the blank tail.
func searchPad(query string, width int) int {
	if strings.Trim(query, " ") == "" {
		return width
	}
	return utf8.RuneCountInString(query)
}

// narrowedLines returns the history lines a search for cm.SearchQuery has to
// read, newest first, when the last search can tell, and ok false when every
// line has to be read. A line that holds a query holds every prefix of it.
// So when the last search was for a prefix of this one, over the same
// history, and found every match rather than stopping at the limit, only the
// lines it matched can match now. That is the search a person typing one more
// letter makes.
func narrowedLines(cm *terminal.CopyMode, gen uint64, sbLen int) ([]int, bool) {
	c := &cm.SearchCache
	if !c.Valid || c.Capped ||
		c.HistoryGen != gen || c.HistoryLen != sbLen || c.CaseSensitive != cm.CaseSensitive ||
		c.Query == "" || !strings.HasPrefix(cm.SearchQuery, c.Query) {
		return nil, false
	}
	var lines []int
	for i := len(c.Matches) - 1; i >= 0; i-- {
		m := c.Matches[i]
		if m.Line < sbLen && (len(lines) == 0 || lines[len(lines)-1] != m.Line) {
			lines = append(lines, m.Line)
		}
	}
	return lines, true
}

// executeSearch performs a search operation and updates matches
func executeSearch(cm *terminal.CopyMode, window *terminal.Window) {
	// Check cache
	if cm.SearchQuery != "" && cm.SearchQuery == cm.SearchCache.Query && cm.SearchCache.Valid {
		cm.SearchMatches = cm.SearchCache.Matches
		jumpFromOrigin(cm, window)
		return
	}

	cm.SearchMatches = nil
	if cm.SearchQuery == "" {
		restoreSearchOrigin(cm, window)
		return
	}

	fold := !cm.CaseSensitive
	query := cm.SearchQuery
	if fold {
		query = strings.ToLower(query)
	}
	q := []byte(query)

	term := window.Terminal
	scrollbackLen := window.ScrollbackLen()
	screenHeight := term.Height()
	gen := term.ScrollbackGeneration()

	// The buffer is scanned newest line first, the screen and then the
	// scrollback, so when the match limit is reached the matches kept are the
	// ones nearest the live screen, where copy mode starts. Scanning oldest
	// first kept the oldest matches, and a ? search from the prompt then
	// skipped every recent match. The lines are put back in buffer order below.
	var lines [][]terminal.SearchMatch
	total := 0
	var line searchLine
	for y := screenHeight - 1; y >= 0 && total < maxSearchMatches; y-- {
		line.fromScreen(term, y, fold)
		if found := line.find(q, scrollbackLen+y, nil); len(found) > 0 {
			lines = append(lines, found)
			total += len(found)
		}
	}

	// The history is read as text, a byte or so a cell, and no line is
	// decoded into cells: that was 270 MB and 150 ms a key on a full
	// 207-column pane.
	search := func(i, width int, cells []vt.TextCell) []terminal.SearchMatch {
		line.fromHistory(cells, width, searchPad(query, width), fold)
		return line.find(q, i, nil)
	}
	if only, ok := narrowedLines(cm, gen, scrollbackLen); ok {
		for _, i := range only {
			if total >= maxSearchMatches {
				break
			}
			term.ScrollbackText(i, i+1, func(i, width int, cells []vt.TextCell) bool {
				if found := search(i, width, cells); len(found) > 0 {
					lines = append(lines, found)
					total += len(found)
				}
				return true
			})
		}
	} else {
		var block [][]terminal.SearchMatch
		for end := scrollbackLen; end > 0 && total < maxSearchMatches; end -= searchBlock {
			from := max(end-searchBlock, 0)
			block = block[:0]
			term.ScrollbackText(from, end, func(i, width int, cells []vt.TextCell) bool {
				if found := search(i, width, cells); len(found) > 0 {
					block = append(block, found)
				}
				return true
			})
			for k := len(block) - 1; k >= 0 && total < maxSearchMatches; k-- {
				lines = append(lines, block[k])
				total += len(block[k])
			}
		}
	}
	cm.SearchMatches = make([]terminal.SearchMatch, 0, total)
	for i := len(lines) - 1; i >= 0; i-- {
		cm.SearchMatches = append(cm.SearchMatches, lines[i]...)
	}

	// Update cache
	cm.SearchCache = terminal.SearchCache{
		Query:         cm.SearchQuery,
		Matches:       cm.SearchMatches,
		CacheTime:     time.Now(),
		Valid:         true,
		CaseSensitive: cm.CaseSensitive,
		Capped:        total >= maxSearchMatches,
		HistoryGen:    gen,
		HistoryLen:    scrollbackLen,
	}

	jumpFromOrigin(cm, window)
}

// jumpFromOrigin moves the cursor to the match the typed query picks. The
// search runs from the origin saved when the prompt opened, so typing one more
// character refines the match instead of skipping past it. / takes the first
// match after the origin and ? the last one before it, each wrapping round the
// buffer. With no match the cursor goes back to the origin, as vim does.
func jumpFromOrigin(cm *terminal.CopyMode, window *terminal.Window) {
	o := cm.SearchOrigin
	idx, _ := searchFrom(cm.SearchMatches, o.Line, o.CursorX, cm.SearchBackward)
	if idx < 0 {
		restoreSearchOrigin(cm, window)
		return
	}
	cm.CurrentMatch = idx
	jumpToMatch(cm, window, idx)
}

// restoreSearchOrigin puts the cursor and the view back where they were when
// the search prompt opened. The origin is an absolute line, so output that
// arrived while the prompt was open does not move it. The cursor goes back to
// the same viewport row when the scrollback allows it.
func restoreSearchOrigin(cm *terminal.CopyMode, window *terminal.Window) {
	o := cm.SearchOrigin
	sb := window.ScrollbackLen()
	last := window.LastContentRow()
	row := min(max(o.Row, 0), last)
	offset := min(max(sb-o.Line+row, 0), sb)
	cm.CursorX = o.CursorX
	cm.CursorY = min(max(o.Line-sb+offset, 0), last)
	cm.ScrollOffset = offset
	window.ScrollbackOffset = offset
}

// searchFrom returns the index of the match a search from (absY, x) lands on.
// Forward it is the first match that starts after the position, backward the
// last match that starts before it. When there is none in that direction the
// search wraps to the other end of the buffer and wrapped is true. It returns
// -1 when there are no matches at all. matches is in buffer order, oldest line
// first, which is the order executeSearch builds it in.
func searchFrom(matches []terminal.SearchMatch, absY, x int, backward bool) (idx int, wrapped bool) {
	if len(matches) == 0 {
		return -1, false
	}
	if backward {
		for i := len(matches) - 1; i >= 0; i-- {
			m := matches[i]
			if m.Line < absY || (m.Line == absY && m.StartX < x) {
				return i, false
			}
		}
		return len(matches) - 1, true
	}
	for i, m := range matches {
		if m.Line > absY || (m.Line == absY && m.StartX > x) {
			return i, false
		}
	}
	return 0, true
}

// stepMatch is n and N: it moves to the next match from the cursor in the
// given direction, wrapping round the buffer. n passes the direction of the
// last search and N the opposite one, so after ? the n key goes up. It runs
// from the cursor rather than from the last match, so a match is found from
// wherever the cursor was moved to in between.
func stepMatch(cm *terminal.CopyMode, window *terminal.Window, backward bool) (wrapped bool) {
	idx, wrapped := searchFrom(cm.SearchMatches, getAbsoluteY(cm, window), cm.CursorX, backward)
	if idx < 0 {
		return false
	}
	cm.CurrentMatch = idx
	jumpToMatch(cm, window, idx)
	return wrapped
}

// jumpToMatch jumps cursor to a specific match
func jumpToMatch(cm *terminal.CopyMode, window *terminal.Window, matchIdx int) {
	if matchIdx < 0 || matchIdx >= len(cm.SearchMatches) {
		return
	}

	match := cm.SearchMatches[matchIdx]
	scrollbackLen := window.ScrollbackLen()

	if match.Line < scrollbackLen {
		// Match is in scrollback
		cm.ScrollOffset = scrollbackLen - match.Line
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = 0
	} else {
		// Match is in current screen
		screenLine := match.Line - scrollbackLen
		cm.ScrollOffset = 0
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = min(screenLine, window.LastContentRow())
	}

	cm.CursorX = match.StartX
}

// searchPrompt is the character the search prompt starts with: ? for a
// backward search and / for a forward one.
func searchPrompt(backward bool) string {
	if backward {
		return "?"
	}
	return "/"
}

// searchWrapMessage says that n or N went past the end of the buffer and
// started again at the other end.
func searchWrapMessage(backward bool) string {
	if backward {
		return "Search reached the top. It continues at the bottom."
	}
	return "Search reached the bottom. It continues at the top."
}
