package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

type screen struct {
	cb         *Callbacks
	buf        *grid
	cur, saved Cursor
	savedExtra savedExtras
	scroll     uv.Rectangle
	scrollback *Scrollback
	rf         reflowScratch
	wideCol    bool
	wideColX   int
	wideColAt  uv.Position
}

func (s *screen) cursorCol() int {
	if s.wideCol && s.cur.Position == s.wideColAt {
		return s.wideColX
	}
	return s.cur.X
}

func newScreen(w, h int) *screen {
	s := screen{}
	s.scrollback = NewScrollback(0)
	s.buf = newGrid(w, h)
	s.scroll = s.buf.Bounds()
	return &s
}

func newAltScreen() *screen {
	s := screen{}
	s.buf = newGrid(1, 1)
	s.scroll = s.buf.Bounds()
	return &s
}

func (s *screen) Reset() {
	s.buf.Clear()
	s.cur = Cursor{}
	s.saved = Cursor{}
	s.savedExtra = savedExtras{}
	s.scroll = s.buf.Bounds()
}

func (s *screen) Bounds() uv.Rectangle {
	return s.buf.Bounds()
}

func (s *screen) CellAt(x int, y int) *uv.Cell {
	return s.buf.CellAt(x, y)
}

func (s *screen) SetCell(x, y int, c *uv.Cell) {
	s.wideCol = false
	pre := s.buf.CellAt(x, y)
	wasPaired := pre != nil && (pre.Width > 1 || (pre.Width == 0 && pre.Content == ""))
	s.buf.SetCell(x, y, c)
	if !wasPaired && (c == nil || c.Width <= 1) {
		return
	}
	w := 1
	if c != nil && c.Width > 1 {
		w = c.Width
	}
	for _, nx := range [2]int{x - 1, x + w} {
		if nx < 0 {
			continue
		}
		nc := s.buf.CellAt(nx, y)
		if nc == nil || nc.Width != 0 || nc.Content != "" {
			continue
		}
		if lead := s.buf.CellAt(nx-1, y); nx > 0 && lead != nil && lead.Width == 2 {
			continue
		}
		s.buf.SetCell(nx, y, nil)
	}
}

func (s *screen) Height() int {
	return s.buf.Height()
}

func (s *screen) Resize(width int, height int) {
	s.shrinkRows(height)
	s.buf.Resize(width, height)
	s.blankWideRunesCutByTheEdge()
	s.scroll = s.buf.Bounds()

	s.cur.X = clamp(s.cur.X, 0, max(s.buf.Width()-1, 0))
	s.cur.Y = clamp(s.cur.Y, 0, max(s.buf.Height()-1, 0))
	s.saved.Y = clamp(s.saved.Y, 0, max(s.buf.Height()-1, 0))
}

func (s *screen) shrinkRows(height int) {
	excess := s.buf.Height() - height
	if excess <= 0 {
		return
	}
	for y := s.buf.Height() - 1; excess > 0 && y > s.cur.Y && !s.rowHoldsText(y); y-- {
		excess--
		s.buf.rows = s.buf.rows[:y]
		s.buf.ext = s.buf.ext[:y]
		s.buf.wrap = s.buf.wrap[:y]
		if s.buf.tail != nil {
			s.buf.tail = s.buf.tail[:y]
		}
		if s.buf.prot != nil {
			s.buf.prot[y] = nil
			s.buf.prot = s.buf.prot[:y]
		}
	}
	if excess <= 0 {
		return
	}
	s.scroll = s.buf.Bounds()
	s.rotateWholeScreenUp(excess, s.scrollback != nil)
	if s.wideCol && s.cur.Position == s.wideColAt {
		s.wideColAt.Y = max(s.wideColAt.Y-excess, 0)
	}
	s.cur.Y = max(s.cur.Y-excess, 0)
	s.saved.Y = max(s.saved.Y-excess, 0)
}

func (s *screen) rowHoldsText(y int) bool {
	if len(s.buf.rowTail(y)) > 0 {
		return true
	}
	row := s.buf.rows[y]
	for x := range row[:min(s.buf.ext[y], len(row))] {
		if !isBlankCell(&row[x]) {
			return true
		}
	}
	return false
}

func (s *screen) blankWideRunesCutByTheEdge() {
	x := s.buf.Width() - 1
	if x < 0 {
		return
	}
	for y := range s.buf.Height() {
		if c := s.buf.CellAt(x, y); c != nil && c.Width > 1 {
			s.buf.SetCell(x, y, nil)
		}
	}
}

func (s *screen) Width() int {
	return s.buf.Width()
}

func (s *screen) Clear() {
	s.ClearArea(s.Bounds())
}

func (s *screen) ClearArea(area uv.Rectangle) {
	s.buf.ClearArea(area)
}

func (s *screen) Fill(c *uv.Cell) {
	s.FillArea(c, s.Bounds())
}

func (s *screen) FillArea(c *uv.Cell, area uv.Rectangle) {
	s.buf.FillArea(c, area)
}

func (s *screen) setHorizontalMargins(left, right int) {
	s.scroll.Min.X = left
	s.scroll.Max.X = right
}

func (s *screen) setVerticalMargins(top, bottom int) {
	s.scroll.Min.Y = top
	s.scroll.Max.Y = bottom
}

func (s *screen) setCursorX(x int, margins bool) {
	s.setCursor(x, s.cur.Y, margins)
}

func (s *screen) setCursor(x, y int, margins bool) {
	old := s.cur.Position
	w := max(1, s.buf.Width())
	h := max(1, s.buf.Height())
	if !margins {
		x = clamp(x, 0, w-1)
		y = clamp(y, 0, h-1)
	} else {
		minX := min(s.scroll.Min.X, w-1)
		maxX := max(minX+1, s.scroll.Max.X)
		x = clamp(s.scroll.Min.X+x, minX, maxX-1)

		minY := min(s.scroll.Min.Y, h-1)
		maxY := max(minY+1, s.scroll.Max.Y)
		y = clamp(s.scroll.Min.Y+y, minY, maxY-1)
	}
	s.cur.X, s.cur.Y = x, y

	if s.cb.CursorPosition != nil && (old.X != x || old.Y != y) {
		s.cb.CursorPosition(old, uv.Pos(x, y))
	}
}

func (s *screen) moveCursor(dx, dy int) {
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

func (s *screen) Cursor() Cursor {
	return s.cur
}

func (s *screen) CursorPosition() (x, y int) {
	return s.cur.X, s.cur.Y
}

func (s *screen) ScrollRegion() uv.Rectangle {
	return s.scroll
}

func (s *screen) SaveCursor() {
	s.saved = s.cur
}

type savedExtras struct {
	phantom    bool
	origin     bool
	charsets   [4]CharSet
	charsetIDs [4]byte
	gl, gr     int
}

func (s *screen) RestoreCursor() {
	old := s.cur.Position
	s.cur = s.saved
	s.cur.X = clamp(s.cur.X, 0, max(s.buf.Width()-1, 0))
	s.cur.Y = clamp(s.cur.Y, 0, max(s.buf.Height()-1, 0))

	if s.cb.CursorPosition != nil && (old.X != s.cur.X || old.Y != s.cur.Y) {
		s.cb.CursorPosition(old, s.cur.Position)
	}
}

func (s *screen) setCursorHidden(hidden bool) {
	changed := s.cur.Hidden != hidden
	s.cur.Hidden = hidden
	if changed && s.cb.CursorVisibility != nil {
		s.cb.CursorVisibility(!hidden)
	}
}

func (s *screen) cursorPen() uv.Style {
	return s.cur.Pen
}

func (s *screen) cursorLink() uv.Link {
	return s.cur.Link
}

func (s *screen) ShowCursor() {
	s.setCursorHidden(false)
}

func (s *screen) HideCursor() {
	s.setCursorHidden(true)
}

func (s *screen) InsertCell(n int) {
	s.insertCellAt(s.cur.X, s.cur.Y, n)
}

func (s *screen) insertCellAt(x, y, n int) {
	if n <= 0 {
		return
	}

	line, n, ok := s.shiftBounds(x, y, n)
	if !ok {
		return
	}
	right := s.scroll.Max.X

	for i := right - 1; i >= x+n; i-- {
		line[i] = line[i-n]
	}
	blank := s.blankCell()
	for i := x; i < x+n; i++ {
		putBlank(line, i, blank)
	}
	repairWide(line)
	s.buf.shiftProtected(y, x+n, x, right-x-n)
	s.buf.clearProtected(y, x, x+n)
}

func (s *screen) DeleteCell(n int) {
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
	s.buf.shiftProtected(y, x, x+n, right-x-n)
	s.buf.clearProtected(y, right-n, right)
	s.buf.setSoftWrapped(y, false)
}

func (s *screen) shiftBounds(x, y, n int) (uv.Line, int, bool) {
	area := s.scroll
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= s.buf.Height() ||
		x < area.Min.X || x >= area.Max.X || x >= s.buf.Width() {
		return nil, 0, false
	}
	if x+n > area.Max.X {
		n = area.Max.X - x
	}
	if s.buf.Row(y) == nil && s.blankCell() == nil {
		return nil, 0, false
	}
	return s.buf.row(y), n, true
}

func putBlank(line uv.Line, x int, blank *uv.Cell) {
	if blank == nil {
		line[x] = uv.EmptyCell
		return
	}
	line[x] = *blank
}

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
			line[i].Empty()
		}
	}
}

func (s *screen) ScrollUp(n int) {
	if n <= 0 {
		return
	}

	scroll := s.scroll
	width := s.buf.Width()

	save := s.scrollback != nil && scroll.Min.Y == 0 && scroll.Min.X == 0 && scroll.Dx() == width

	x, y := s.CursorPosition()
	s.setCursor(s.cur.X, 0, true)
	if !s.rotateWholeScreenUp(n, save) {
		if save {
			for i := 0; i < n && i < scroll.Dy(); i++ {
				s.scrollback.PushLine(s.buf.withTail(scroll.Min.Y+i, extractLine(s.buf, scroll.Min.Y+i, width)))
				s.scrollback.markNewest(s.buf.wrap[scroll.Min.Y+i])
			}
		}
		s.DeleteLine(n)
	}
	s.setCursor(x, y, false)
}

func (s *screen) rotateWholeScreenUp(n int, save bool) bool {
	lines := s.buf.rows
	height := len(lines)
	area := s.scroll
	if height == 0 || area.Min.X != 0 || area.Min.Y != 0 ||
		area.Max.Y != height || area.Dx() != s.buf.Width() {
		return false
	}

	if n >= height {
		n = height
	}

	if save {
		for i, row := range lines[:n] {
			if row == nil {
				s.scrollback.PushBlankLine(s.buf.Width())
			} else {
				if t := s.buf.rowTail(i); len(t) > 0 {
					s.scrollback.PushLine(s.buf.withTail(i, row))
				} else {
					s.scrollback.pushTrimmed(row, s.buf.ext[i])
				}
			}
			s.scrollback.markNewest(s.buf.wrap[i])
		}
	}
	s.buf.scrollWindow(n)

	s.buf.blankRows(height-n, height, s.blankCell())
	return true
}

func (s *screen) ScrollDown(n int) {
	x, y := s.CursorPosition()
	s.setCursor(s.cur.X, 0, true)
	s.InsertLine(n)
	s.setCursor(x, y, false)
}

func (s *screen) InsertLine(n int) bool {
	if n <= 0 {
		return false
	}

	x, y := s.cur.X, s.cur.Y

	if y < s.scroll.Min.Y || y >= s.scroll.Max.Y ||
		x < s.scroll.Min.X || x >= s.scroll.Max.X {
		return false
	}

	s.buf.InsertLineArea(y, n, s.blankCell(), s.scroll)
	s.blankWideRunesCutByMargins()

	return true
}

func (s *screen) DeleteLine(n int) bool {
	if n <= 0 {
		return false
	}

	scroll := s.scroll
	x, y := s.cur.X, s.cur.Y

	if y < scroll.Min.Y || y >= scroll.Max.Y ||
		x < scroll.Min.X || x >= scroll.Max.X {
		return false
	}

	s.buf.DeleteLineArea(y, n, s.blankCell(), scroll)
	s.blankWideRunesCutByMargins()

	return true
}

func (s *screen) blankWideRunesCutByMargins() {
	w := s.buf.Width()
	for _, x := range [2]int{s.scroll.Min.X, s.scroll.Max.X} {
		if x <= 0 || x >= w {
			continue
		}
		for y := s.scroll.Min.Y; y < s.scroll.Max.Y; y++ {
			if lead := s.buf.CellAt(x-1, y); lead != nil && lead.Width > 1 {
				var keep *uv.Cell
				if c := s.buf.CellAt(x, y); c != nil && (c.Width != 0 || c.Content != "") {
					saved := *c
					keep = &saved
				}
				s.buf.SetCell(x-1, y, nil)
				if keep != nil {
					s.buf.SetCell(x, y, keep)
				}
			}
			if cont := s.buf.CellAt(x, y); cont != nil && cont.Width == 0 && cont.Content == "" {
				s.buf.SetCell(x, y, nil)
			}
		}
	}
}

func (s *screen) withBlankPen(fn func()) {
	bg := s.cur.Pen.Bg
	s.cur.Pen.Bg = nil
	defer func() { s.cur.Pen.Bg = bg }()
	fn()
}

func (s *screen) blankCell() *uv.Cell {
	if s.cur.Pen.Bg == nil {
		return nil
	}

	c := uv.EmptyCell
	c.Style.Bg = s.cur.Pen.Bg
	return &c
}

func (s *screen) Scrollback() *Scrollback {
	return s.scrollback
}

func (s *screen) DisableScrollback() {
	s.scrollback = nil
}

func (s *screen) ClearScrollback() {
	if s.scrollback != nil {
		s.scrollback.Clear()
	}
}

func (s *screen) ScrollbackLen() int {
	if s.scrollback == nil {
		return 0
	}
	return s.scrollback.Len()
}

func (s *screen) ScrollbackLine(index int) uv.Line {
	if s.scrollback == nil {
		return nil
	}
	return s.scrollback.Line(index)
}

func (s *screen) SetScrollbackMaxLines(maxLines int) {
	if s.scrollback != nil {
		s.scrollback.SetMaxLines(maxLines)
	}
}
