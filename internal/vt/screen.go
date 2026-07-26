package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// Screen represents a virtual terminal screen.
type Screen struct {
	// cb is the callbacks struct to use.
	cb *Callbacks
	// The buffer of the screen.
	buf *uv.RenderBuffer
	// The cur of the screen.
	cur, saved Cursor
	// scroll is the scroll region.
	scroll uv.Rectangle
	// scrollback is the scrollback buffer for lines that have scrolled off the top.
	scrollback *Scrollback
}

// NewScreen creates a new screen.
func NewScreen(w, h int) *Screen {
	s := Screen{}
	s.scrollback = NewScrollback(0) // Use default size
	s.buf = uv.NewRenderBuffer(w, h)
	s.scroll = s.buf.Bounds()
	return &s
}

// Reset resets the screen.
// It clears the screen, sets the cursor to the top left corner, reset the
// cursor styles, and resets the scroll region.
func (s *Screen) Reset() {
	s.buf.Clear()
	s.cur = Cursor{}
	s.saved = Cursor{}
	s.scroll = s.buf.Bounds()
}

// Bounds returns the bounds of the screen.
func (s *Screen) Bounds() uv.Rectangle {
	return s.buf.Bounds()
}

// Touched returns touched lines in the screen buffer.
func (s *Screen) Touched() []*uv.LineData {
	return s.buf.Touched
}

// CellAt returns the cell at the given x, y position.
func (s *Screen) CellAt(x int, y int) *uv.Cell {
	return s.buf.CellAt(x, y)
}

// SetCell sets the cell at the given x, y position.
func (s *Screen) SetCell(x, y int, c *uv.Cell) {
	s.buf.SetCell(x, y, c)
}

// Height returns the height of the screen.
func (s *Screen) Height() int {
	return s.buf.Height()
}

// Resize resizes the screen.
func (s *Screen) Resize(width int, height int) {
	s.buf.Resize(width, height)
	// Resize the Touched slice to match the new height.
	if h := s.buf.Height(); len(s.buf.Touched) != h {
		s.buf.Touched = make([]*uv.LineData, h)
	}
	s.scroll = s.buf.Bounds()
}

// Width returns the width of the screen.
func (s *Screen) Width() int {
	return s.buf.Width()
}

// Clear clears the screen with blank cells.
func (s *Screen) Clear() {
	s.ClearArea(s.Bounds())
}

// ClearArea clears the given area.
func (s *Screen) ClearArea(area uv.Rectangle) {
	s.buf.ClearArea(area)
}

// Fill fills the screen or part of it.
func (s *Screen) Fill(c *uv.Cell) {
	s.FillArea(c, s.Bounds())
}

// FillArea fills the given area with the given cell.
func (s *Screen) FillArea(c *uv.Cell, area uv.Rectangle) {
	s.buf.FillArea(c, area)
}

// setHorizontalMargins sets the horizontal margins.
func (s *Screen) setHorizontalMargins(left, right int) {
	s.scroll.Min.X = left
	s.scroll.Max.X = right
}

// setVerticalMargins sets the vertical margins.
func (s *Screen) setVerticalMargins(top, bottom int) {
	s.scroll.Min.Y = top
	s.scroll.Max.Y = bottom
}

// setCursorX sets the cursor X position. If margins is true, the cursor is
// only set if it is within the scroll margins.
func (s *Screen) setCursorX(x int, margins bool) {
	s.setCursor(x, s.cur.Y, margins)
}

// setCursor sets the cursor position. If margins is true, the cursor is only
// set if it is within the scroll margins. This follows how [ansi.CUP] works.
func (s *Screen) setCursor(x, y int, margins bool) {
	old := s.cur.Position
	if !margins {
		y = clamp(y, 0, s.buf.Height()-1)
		x = clamp(x, 0, s.buf.Width()-1)
	} else {
		y = clamp(s.scroll.Min.Y+y, s.scroll.Min.Y, s.scroll.Max.Y-1)
		x = clamp(s.scroll.Min.X+x, s.scroll.Min.X, s.scroll.Max.X-1)
	}
	s.cur.X, s.cur.Y = x, y

	if s.cb.CursorPosition != nil && (old.X != x || old.Y != y) {
		s.cb.CursorPosition(old, uv.Pos(x, y))
	}
}

// moveCursor moves the cursor by the given x and y deltas. If the cursor
// position is inside the scroll region, it is bounded by the scroll region.
// Otherwise, it is bounded by the screen bounds.
// This follows how [ansi.CUU], [ansi.CUD], [ansi.CUF], [ansi.CUB], [ansi.CNL],
// [ansi.CPL].
func (s *Screen) moveCursor(dx, dy int) {
	scroll := s.scroll
	old := s.cur.Position
	if old.X < scroll.Min.X {
		scroll.Min.X = 0
	}
	if old.X >= scroll.Max.X {
		scroll.Max.X = s.buf.Width()
	}

	pt := uv.Pos(s.cur.X+dx, s.cur.Y+dy)

	var x, y int
	if old.In(scroll) {
		y = clamp(pt.Y, scroll.Min.Y, scroll.Max.Y-1)
		x = clamp(pt.X, scroll.Min.X, scroll.Max.X-1)
	} else {
		y = clamp(pt.Y, 0, s.buf.Height()-1)
		x = clamp(pt.X, 0, s.buf.Width()-1)
	}

	s.cur.X, s.cur.Y = x, y

	if s.cb.CursorPosition != nil && (old.X != x || old.Y != y) {
		s.cb.CursorPosition(old, uv.Pos(x, y))
	}
}

// Cursor returns the cursor.
func (s *Screen) Cursor() Cursor {
	return s.cur
}

// CursorPosition returns the cursor position.
func (s *Screen) CursorPosition() (x, y int) {
	return s.cur.X, s.cur.Y
}

// ScrollRegion returns the scroll region.
func (s *Screen) ScrollRegion() uv.Rectangle {
	return s.scroll
}

// SaveCursor saves the cursor.
func (s *Screen) SaveCursor() {
	s.saved = s.cur
}

// RestoreCursor restores the cursor.
func (s *Screen) RestoreCursor() {
	old := s.cur.Position
	s.cur = s.saved

	if s.cb.CursorPosition != nil && (old.X != s.cur.X || old.Y != s.cur.Y) {
		s.cb.CursorPosition(old, s.cur.Position)
	}
}

// setCursorHidden sets the cursor hidden.
func (s *Screen) setCursorHidden(hidden bool) {
	changed := s.cur.Hidden != hidden
	s.cur.Hidden = hidden
	if changed && s.cb.CursorVisibility != nil {
		s.cb.CursorVisibility(!hidden)
	}
}

// setCursorStyle sets the cursor style.
func (s *Screen) setCursorStyle(style CursorStyle, blink bool) {
	changed := s.cur.Style != style || s.cur.Steady != !blink
	s.cur.Style = style
	s.cur.Steady = !blink
	if changed && s.cb.CursorStyle != nil {
		s.cb.CursorStyle(style, !blink)
	}
}

// cursorPen returns the cursor pen.
func (s *Screen) cursorPen() uv.Style {
	return s.cur.Pen
}

// cursorLink returns the cursor link.
func (s *Screen) cursorLink() uv.Link {
	return s.cur.Link
}

// ShowCursor shows the cursor.
func (s *Screen) ShowCursor() {
	s.setCursorHidden(false)
}

// HideCursor hides the cursor.
func (s *Screen) HideCursor() {
	s.setCursorHidden(true)
}

// InsertCell inserts n blank characters at the cursor position pushing out
// cells to the right and out of the screen.
func (s *Screen) InsertCell(n int) {
	if n <= 0 {
		return
	}

	x, y := s.cur.X, s.cur.Y
	line, n, ok := s.shiftBounds(x, y, n)
	if !ok {
		return
	}
	right := s.scroll.Max.X

	// Copied by assignment rather than through the buffer's Set, which blanks
	// the other half of any wide rune it lands on. That is right for an
	// overwrite but ruinous inside a shift, where the cells being moved are
	// still live: blanking the neighbour of a cell that has just been copied
	// erases the copy, and a single shift over a line of CJK empties the line.
	for i := right - 1; i >= x+n; i-- {
		line[i] = line[i-n]
	}
	blank := s.blankCell()
	for i := x; i < x+n; i++ {
		putBlank(line, i, blank)
	}
	repairWide(line)
	s.buf.TouchLine(x, y, right-x)
}

// DeleteCell deletes n cells at the cursor position moving cells to the left.
// This has no effect if the cursor is outside the scroll region.
func (s *Screen) DeleteCell(n int) {
	if n <= 0 {
		return
	}

	x, y := s.cur.X, s.cur.Y
	line, n, ok := s.shiftBounds(x, y, n)
	if !ok {
		return
	}
	right := s.scroll.Max.X

	for i := x; i < right-n; i++ {
		line[i] = line[i+n]
	}
	blank := s.blankCell()
	for i := right - n; i < right; i++ {
		putBlank(line, i, blank)
	}
	repairWide(line)
	s.buf.TouchLine(x, y, right-x)
}

// shiftBounds validates a cell shift at (x, y) and returns the row it operates
// on together with the count clamped to the space between x and the right
// margin. It reports false when the position is outside the margins or the
// screen, which is the case every caller treats as a no-op.
func (s *Screen) shiftBounds(x, y, n int) (uv.Line, int, bool) {
	area := s.scroll
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= s.buf.Height() ||
		x < area.Min.X || x >= area.Max.X || x >= s.buf.Width() {
		return nil, 0, false
	}
	if x+n > area.Max.X {
		n = area.Max.X - x
	}
	return s.buf.Lines[y], n, true
}

// putBlank overwrites one column with the erase cell. Like the shift itself it
// assigns rather than calling Set, because the columns it fills have already
// been vacated and any wide rune the fill cuts is dealt with by repairWide.
func putBlank(line uv.Line, x int, blank *uv.Cell) {
	if blank == nil {
		line[x] = uv.EmptyCell
		return
	}
	line[x] = *blank
}

// repairWide blanks every half of a wide rune whose partner a shift left
// behind. A rune that moved by an odd number of columns, or whose second half
// fell off the right margin, leaves a cell claiming a column it no longer
// shares with anything; a terminal has no way to draw that, so both ends of the
// broken pair become spaces.
//
// One pass afterwards is deliberate. Proving which end of which shift can orphan
// which half is fiddly and easy to get subtly wrong, whereas the invariant here
// (a lead of width w is followed by exactly w-1 continuation cells, and no
// continuation stands alone) is stated once and checked over the whole row.
func repairWide(line uv.Line) {
	for i := 0; i < len(line); i++ {
		w := line[i].Width
		switch {
		case w > 1:
			whole := true
			for j := 1; j < w; j++ {
				if i+j >= len(line) || line[i+j].Width != 0 {
					whole = false
					break
				}
			}
			if !whole {
				line[i].Empty()
				continue
			}
			i += w - 1
		case w == 0:
			// Reached without being skipped over above, so there is no lead in
			// front of it.
			line[i].Empty()
		}
	}
}

// ScrollUp scrolls the content up n lines within the given region. Lines
// scrolled past the top margin are saved to the scrollback buffer if the
// scroll region encompasses the full screen width and starts at the top.
// This is equivalent to [ansi.SU] which moves the cursor to the top margin
// and performs a [ansi.DL] operation.
func (s *Screen) ScrollUp(n int) {
	if n <= 0 {
		return
	}

	scroll := s.scroll
	width := s.buf.Width()

	// Only save to scrollback if we're scrolling the main screen area
	// (not a limited scroll region) and the scroll region starts at Y=0
	if scroll.Min.Y == 0 && scroll.Min.X == 0 && scroll.Dx() == width {
		// Save the top n lines to scrollback before they're deleted
		for i := 0; i < n && i < scroll.Dy(); i++ {
			y := scroll.Min.Y + i
			line := extractLine(s.buf.Buffer, y, width)
			s.scrollback.PushLine(line)
		}
	}

	x, y := s.CursorPosition()
	s.setCursor(s.cur.X, 0, true)
	s.DeleteLine(n)
	s.setCursor(x, y, false)
}

// ScrollDown scrolls the content down n lines within the given region. Lines
// scrolled past the bottom margin are lost. This is equivalent to [ansi.SD]
// which moves the cursor to top margin and performs a [ansi.IL] operation.
func (s *Screen) ScrollDown(n int) {
	x, y := s.CursorPosition()
	s.setCursor(s.cur.X, 0, true)
	s.InsertLine(n)
	s.setCursor(x, y, false)
}

// InsertLine inserts n blank lines at the cursor position Y coordinate.
// Only operates if cursor is within scroll region. Lines below cursor Y
// are moved down, with those past bottom margin being discarded.
// It returns true if the operation was successful.
func (s *Screen) InsertLine(n int) bool {
	if n <= 0 {
		return false
	}

	x, y := s.cur.X, s.cur.Y

	// Only operate if cursor Y is within scroll region
	if y < s.scroll.Min.Y || y >= s.scroll.Max.Y ||
		x < s.scroll.Min.X || x >= s.scroll.Max.X {
		return false
	}

	s.buf.InsertLineArea(y, n, s.blankCell(), s.scroll)

	return true
}

// DeleteLine deletes n lines at the cursor position Y coordinate.
// Only operates if cursor is within scroll region. Lines below cursor Y
// are moved up, with blank lines inserted at the bottom of scroll region.
// It returns true if the operation was successful.
func (s *Screen) DeleteLine(n int) bool {
	if n <= 0 {
		return false
	}

	scroll := s.scroll
	x, y := s.cur.X, s.cur.Y

	// Only operate if cursor Y is within scroll region
	if y < scroll.Min.Y || y >= scroll.Max.Y ||
		x < scroll.Min.X || x >= scroll.Max.X {
		return false
	}

	s.buf.DeleteLineArea(y, n, s.blankCell(), scroll)

	return true
}

// blankCell returns the cursor blank cell with the background color set to the
// current pen background color. If the pen background color is nil, the return
// value is nil.
func (s *Screen) blankCell() *uv.Cell {
	if s.cur.Pen.Bg == nil {
		return nil
	}

	c := uv.EmptyCell
	c.Style.Bg = s.cur.Pen.Bg
	return &c
}

// Scrollback returns the scrollback buffer for this screen.
func (s *Screen) Scrollback() *Scrollback {
	return s.scrollback
}

// ClearScrollback clears all lines from the scrollback buffer.
func (s *Screen) ClearScrollback() {
	if s.scrollback != nil {
		s.scrollback.Clear()
	}
}

// ScrollbackLen returns the number of lines currently in the scrollback buffer.
func (s *Screen) ScrollbackLen() int {
	if s.scrollback == nil {
		return 0
	}
	return s.scrollback.Len()
}

// ScrollbackLine returns the line at the specified index in the scrollback buffer.
// Index 0 is the oldest line. Returns nil if the index is out of bounds.
func (s *Screen) ScrollbackLine(index int) uv.Line {
	if s.scrollback == nil {
		return nil
	}
	return s.scrollback.Line(index)
}

// SetScrollbackMaxLines sets the maximum number of lines for the scrollback buffer.
func (s *Screen) SetScrollbackMaxLines(maxLines int) {
	if s.scrollback != nil {
		s.scrollback.SetMaxLines(maxLines)
	}
}
