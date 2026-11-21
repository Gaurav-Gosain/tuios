package vt

import (
	"sync"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

// TextCell is one history cell without its style or link: what it shows and
// how many columns it takes. Content is the cell's text, empty for the spacer
// a wide character leaves in the column after it. It aliases the history's
// own storage, so it is valid only until the callback it was passed to
// returns.
type TextCell struct {
	Content []byte // text of the cell
	Width   int    // columns the cell takes
}

func (sb *Scrollback) eachRow(from, end int, fn func(index int, line uv.Line) bool) {
	from, end = max(from, 0), min(end, sb.Len())
	var buf uv.Line
	for i := from; i < end; i++ {
		data := sb.lines[sb.slot(i)]
		w, n := lineWidth(data)
		if n <= 0 {
			continue
		}
		if cap(buf) < w {
			buf = make(uv.Line, w)
		}
		line := sb.decodeInto(buf[:w:w], data[n:])
		if !fn(i, line) {
			return
		}
	}
}

func (sb *Scrollback) eachText(from, end int, fn func(index, width int, cells []TextCell) bool) {
	from, end = max(from, 0), min(end, sb.Len())
	var cells []TextCell
	for i := from; i < end; i++ {
		data := sb.lines[sb.slot(i)]
		w, n := lineWidth(data)
		if n <= 0 {
			continue
		}
		cells = appendTextCells(cells[:0], data[n:], w)
		if !fn(i, w, cells) {
			return
		}
	}
}

func appendTextCells(cells []TextCell, data []byte, width int) []TextCell {
	i := 0
	for i < len(data) && len(cells) < width {
		switch c := data[i]; c {
		case sbStyle:
			_, j, ok := readStyle(data, i+1)
			if !ok {
				return cells
			}
			i = j
		case sbLink:
			j, ok := skipLink(data, i+1)
			if !ok {
				return cells
			}
			i = j
		case sbCell:
			if i+1 >= len(data) {
				return cells
			}
			w := int(data[i+1])
			content, j, ok := readContentBytes(data, i+2)
			if !ok {
				return cells
			}
			i = j
			cells = append(cells, TextCell{Content: content, Width: w})
		default:
			if c < utf8.RuneSelf {
				cells = append(cells, TextCell{Content: data[i : i+1 : i+1], Width: 1})
				i++
				continue
			}
			content, j, ok := readContentBytes(data, i)
			if !ok {
				return cells
			}
			i = j
			cells = append(cells, TextCell{Content: content, Width: 1})
		}
	}
	return cells
}

func readContentBytes(data []byte, i int) ([]byte, int, bool) {
	if i >= len(data) {
		return nil, i, false
	}
	switch data[i] {
	case sbEmpty:
		return nil, i + 1, true
	case sbGrapheme:
		s, next, ok := readString(data, i+1)
		if !ok {
			return nil, i, false
		}
		return s[:len(s):len(s)], next, true
	case sbStyle, sbLink, sbCell:
		return nil, i, false
	}
	r, size := utf8.DecodeRune(data[i:])
	if r == utf8.RuneError && size <= 1 {
		return nil, i, false
	}
	return data[i : i+size : i+size], i + size, true
}

// ScrollbackCopy is a run of history lines copied out of a terminal in the
// form the pure emulator's ring keeps them, a byte or so a cell for text. A
// reader copies the lines while it holds the terminal's lock and decodes them
// after releasing it, so the terminal is held for a copy of the encoded bytes
// and not for the decode.
//
// A backend that cannot copy its lines cheaply copies something else under the
// lock and makes the lines from it on first use: see build.
type ScrollbackCopy struct {
	sb    *Scrollback
	build func() *Scrollback
	once  sync.Once
}

//nolint:unused // Only the ghostty backend calls it (ghostty_scrollback_iter.go).
func lazyScrollbackCopy(build func() *Scrollback) *ScrollbackCopy {
	return &ScrollbackCopy{build: build}
}

func (c *ScrollbackCopy) lines() *Scrollback {
	if c.build != nil {
		c.once.Do(func() { c.sb = c.build() })
	}
	return c.sb
}

func (sb *Scrollback) copyLines(from, end int) *ScrollbackCopy {
	from, end = max(from, 0), min(end, sb.Len())
	n := max(end-from, 0)
	c := &Scrollback{
		maxLines: max(n, 1),
		colors:   sb.colors[:len(sb.colors):len(sb.colors)],
	}
	if n == 0 {
		return &ScrollbackCopy{sb: c}
	}
	size := 0
	for i := from; i < end; i++ {
		size += len(sb.lines[sb.slot(i)])
	}
	arena := make([]byte, 0, size)
	c.lines = make([][]byte, n)
	c.wraps = make([]rowFlag, n)
	for k := range n {
		slot := sb.slot(from + k)
		at := len(arena)
		arena = append(arena, sb.lines[slot]...)
		c.lines[k] = arena[at:len(arena):len(arena)]
		c.wraps[k] = sb.wraps[slot]
	}
	c.maxLines = n
	c.full = true
	return &ScrollbackCopy{sb: c}
}

// Len is the number of lines in the copy.
func (c *ScrollbackCopy) Len() int {
	if c == nil || c.lines() == nil {
		return 0
	}
	return c.sb.Len()
}

// Rows calls fn with the copy's lines from index from to end-1, counted from
// the copy's first line, each decoded to its full width into a buffer reused
// from line to line. fn must copy what it keeps. It stops when fn returns
// false.
func (c *ScrollbackCopy) Rows(from, end int, fn func(index int, line uv.Line) bool) {
	if c.Len() == 0 {
		return
	}
	c.sb.eachRow(from, end, fn)
}

// Text is Rows for a reader of text: see Terminal.ScrollbackText.
func (c *ScrollbackCopy) Text(from, end int, fn func(index, width int, cells []TextCell) bool) {
	if c.Len() == 0 {
		return
	}
	c.sb.eachText(from, end, fn)
}

// Wrapped reports whether line index of the copy carried on to the next line
// by autowrap.
func (c *ScrollbackCopy) Wrapped(index int) bool {
	return c.Len() > 0 && c.sb.LineWrapped(index)
}

// Padded reports whether line index of the copy wrapped a column early before
// a wide character. See Terminal.ScrollbackPadded.
func (c *ScrollbackCopy) Padded(index int) bool {
	return c.Len() > 0 && c.sb.lineFlags(index)&rowPadded != 0
}

//nolint:unused // Only the ghostty backend calls it (ghostty_scrollback_iter.go).
func (c *ScrollbackCopy) push(line uv.Line, flags rowFlag, n int) {
	if c.sb == nil {
		c.sb = NewScrollback(max(n, 1))
	}
	if len(line) == 0 {
		c.sb.PushBlankLine(1)
	} else {
		c.sb.PushLine(line)
	}
	c.sb.markNewest(flags)
}

// ScrollbackRows calls fn with each main screen history line from index from
// to end-1, oldest first, decoded into a buffer reused from line to line. See
// Terminal.ScrollbackRows.
func (e *Emulator) ScrollbackRows(from, end int, fn func(index int, line uv.Line) bool) {
	if sb := e.scrs[0].scrollback; sb != nil {
		sb.eachRow(from, end, fn)
	}
}

// ScrollbackText calls fn with each main screen history line from index from
// to end-1 as text cells. See Terminal.ScrollbackText.
func (e *Emulator) ScrollbackText(from, end int, fn func(index, width int, cells []TextCell) bool) {
	if sb := e.scrs[0].scrollback; sb != nil {
		sb.eachText(from, end, fn)
	}
}

// CopyScrollback copies the main screen history lines from index from to
// end-1. See Terminal.CopyScrollback.
func (e *Emulator) CopyScrollback(from, end int) *ScrollbackCopy {
	sb := e.scrs[0].scrollback
	if sb == nil {
		return &ScrollbackCopy{}
	}
	return sb.copyLines(from, end)
}

// ScrollbackGeneration is a number that changes whenever the main screen's
// history does: a line pushed, the ring trimmed, cleared or resized. A reader
// that derived something from the history can keep it for as long as the
// number stays the same.
func (e *Emulator) ScrollbackGeneration() uint64 {
	if sb := e.scrs[0].scrollback; sb != nil {
		return sb.gen
	}
	return 0
}
