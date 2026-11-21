package input

import (
	"bytes"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

const searchBlock = 256

type searchLine struct {
	text    []byte
	cell    []int32
	cols    []int32
	fold    bool
	scratch []byte
}

func (l *searchLine) reset(fold bool) {
	l.text, l.cell, l.cols = l.text[:0], l.cell[:0], l.cols[:0]
	l.fold = fold
}

func (l *searchLine) add(content []byte, width, col int) {
	if width == 0 {
		return
	}
	k := int32(len(l.cols))
	l.cols = append(l.cols, int32(col))
	if len(content) == 1 && content[0] < utf8.RuneSelf {
		c := content[0]
		if l.fold && 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		l.text = append(l.text, c)
		l.cell = append(l.cell, k)
		return
	}
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

var sixelLead = []byte(vt.SixelMarkerLead)

func (l *searchLine) end(next int) {
	l.cols = append(l.cols, int32(next))
}

var space = []byte{' '}

func (l *searchLine) fromScreen(term terminal.CopyText, y int, fold bool) {
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

func (l *searchLine) fromHistory(cells []vt.TextCell, width, pad int, fold bool) {
	l.reset(fold)
	for x := range cells {
		l.add(cells[x].Content, cells[x].Width, x)
	}
	next := len(cells)
	for ; next < width && pad > 0; next, pad = next+1, pad-1 {
		l.add(space, 1, next)
	}
	l.end(next)
}

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

func searchPad(query string, width int) int {
	if strings.Trim(query, " ") == "" {
		return width
	}
	return utf8.RuneCountInString(query)
}

func narrowedLines(cm *terminal.CopyMode, gen uint64, sbLen int) ([]int, bool) {
	c := &cm.SearchCache
	if !c.Valid || c.Capped ||
		c.HistoryGen != gen || c.HistoryLen != sbLen || c.CaseSensitive != cm.CaseSensitive ||
		c.Query == "" || !strings.HasPrefix(cm.SearchQuery(), c.Query) {
		return nil, false
	}
	var lines []int
	for _, m := range slices.Backward(c.Matches) {
		if m.Line < sbLen && (len(lines) == 0 || lines[len(lines)-1] != m.Line) {
			lines = append(lines, m.Line)
		}
	}
	return lines, true
}

func executeSearch(cm *terminal.CopyMode, grid terminal.CopyGrid, maxSearchMatches int) {
	if cm.SearchQuery() != "" && cm.SearchQuery() == cm.SearchCache.Query && cm.SearchCache.Valid {
		cm.SearchMatches = cm.SearchCache.Matches
		jumpFromOrigin(cm, grid)
		return
	}

	cm.SearchMatches = nil
	if cm.SearchQuery() == "" {
		restoreSearchOrigin(cm, grid)
		return
	}

	fold := !cm.CaseSensitive
	query := cm.SearchQuery()
	if fold {
		query = strings.ToLower(query)
	}
	q := []byte(query)

	term := grid.Text()
	scrollbackLen := grid.ScrollbackLen()
	screenHeight := term.Height()
	gen := term.ScrollbackGeneration()

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
	for _, line := range slices.Backward(lines) {
		cm.SearchMatches = append(cm.SearchMatches, line...)
	}

	cm.SearchCache = terminal.SearchCache{
		Query:         cm.SearchQuery(),
		Matches:       cm.SearchMatches,
		CacheTime:     time.Now(),
		Valid:         true,
		CaseSensitive: cm.CaseSensitive,
		Capped:        total >= maxSearchMatches,
		HistoryGen:    gen,
		HistoryLen:    scrollbackLen,
	}

	jumpFromOrigin(cm, grid)
}

func jumpFromOrigin(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	o := cm.SearchOrigin
	idx, _ := searchFrom(cm.SearchMatches, o.Line, o.CursorX, cm.SearchBackward)
	if idx < 0 {
		restoreSearchOrigin(cm, grid)
		return
	}
	cm.CurrentMatch = idx
	jumpToMatch(cm, grid, idx)
}

func restoreSearchOrigin(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	o := cm.SearchOrigin
	sb := grid.ScrollbackLen()
	last := grid.LastContentRow()
	row := min(max(o.Row, 0), last)
	offset := min(max(sb-o.Line+row, 0), sb)
	cm.CursorX = o.CursorX
	cm.CursorY = min(max(o.Line-sb+offset, 0), last)
	cm.ScrollOffset = offset
	grid.SetScrollOffset(offset)
}

func searchFrom(matches []terminal.SearchMatch, absY, x int, backward bool) (idx int, wrapped bool) {
	if len(matches) == 0 {
		return -1, false
	}
	if backward {
		for i, m := range slices.Backward(matches) {
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

func stepMatch(cm *terminal.CopyMode, grid terminal.CopyGrid, backward bool) (wrapped bool) {
	idx, wrapped := searchFrom(cm.SearchMatches, getAbsoluteY(cm, grid), cm.CursorX, backward)
	if idx < 0 {
		return false
	}
	cm.CurrentMatch = idx
	jumpToMatch(cm, grid, idx)
	return wrapped
}

func jumpToMatch(cm *terminal.CopyMode, grid terminal.CopyGrid, matchIdx int) {
	if matchIdx < 0 || matchIdx >= len(cm.SearchMatches) {
		return
	}

	match := cm.SearchMatches[matchIdx]
	scrollbackLen := grid.ScrollbackLen()

	if match.Line < scrollbackLen {
		cm.ScrollOffset = scrollbackLen - match.Line
		grid.SetScrollOffset(cm.ScrollOffset)
		cm.CursorY = 0
	} else {
		screenLine := match.Line - scrollbackLen
		cm.ScrollOffset = 0
		grid.SetScrollOffset(cm.ScrollOffset)
		cm.CursorY = min(screenLine, grid.LastContentRow())
	}

	cm.CursorX = match.StartX
}

func searchPrompt(backward bool) string {
	if backward {
		return "?"
	}
	return "/"
}

func searchWrapMessage(backward bool) string {
	if backward {
		return "Search reached the top. It continues at the bottom."
	}
	return "Search reached the bottom. It continues at the top."
}
