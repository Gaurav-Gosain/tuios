package vt

import (
	"sort"

	uv "github.com/charmbracelet/ultraviolet"
)

// Reflow of the main screen on resize.
//
// A row that autowrap carried on to the next row is part of one line the
// guest printed. When the width changes, those lines are laid out again at
// the new width, the way ghostty, kitty and tmux do, so a pane that narrows
// wraps its text instead of cutting it, and a pane that widens joins it back.
// Without this a narrowing resize dropped every column past the new edge from
// the screen for good.
//
// Only the screen is laid out again, together with the rows of history that
// belong to a line the screen continues, and the history lines a taller
// screen takes back. The rest of the history keeps the width it was written
// at and is clipped where it is drawn (ClipHistoryRow). That bounds the cost
// of a resize by the size of the screen, not of the history: a drag that
// fires a resize per motion step does not rewrap ten thousand lines each
// step.

// reflowScratch holds a screen's reflow buffers from one reflow to the next.
// A drag reflows a pane at every motion step, and these would otherwise be
// allocated again each time. Only the cells a reflow lays out are new each
// time, because the screen keeps them as its rows.
type reflowScratch struct {
	rows  []reflowRow
	ints  []int
	lines []logicalLine
	segs  []uv.Line
	flags []rowFlag
	// hist and histRows hold the history rows a reflow decodes.
	hist     []uv.Cell
	histRows []uv.Line
	// block is the cells the screen's rows were laid out in by the last
	// reflow, and spare the block before it, which nothing refers to once
	// a reflow has replaced the rows that were in it. Two blocks take
	// turns, so a drag allocates no cells.
	block, spare []uv.Cell
	// blank is a read-only blank row for the rows the screen never wrote.
	blank uv.Line
	// rowOff holds, for each laid out row, the column of its line the row
	// starts at, so a position is found by a search instead of a walk.
	rowOff []int
	// tails holds, for each laid out row of a frozen prompt, the cells past
	// the width.
	tails []uv.Line
	// cnt is the scratch for counting the rows of history lines a taller
	// screen takes back.
	cnt *reflowScratch
}

// reflowRow is a row a reflow reads: its cells and how it ends.
type reflowRow struct {
	cells uv.Line
	flags rowFlag
	// frozen marks a row of an open prompt: a line of its own, laid out on
	// one row, with what does not fit kept as the row's tail.
	frozen bool
}

// reflowPoint is a position a reflow carries across: an OSC 133 mark. abs is
// the row counted from the oldest history line, so a history row and a
// screen row are addressed alike, and col is the column.
type reflowPoint struct {
	abs, col int
}

// logicalLine is a line the guest printed, its soft-wrapped rows joined. It
// refers to the rows' cells rather than copying them.
type logicalLine struct {
	// segs are the parts of the rows the line is made of, in order. The
	// second half of a wide character is in them and is skipped on read.
	segs []uv.Line
	// cols is the number of columns the text takes.
	cols int
	// keep is the number of columns the line must keep however its text
	// ends: the cell the cursor stands on has to exist, even past the text.
	keep int
	// frozen marks a row of an open prompt (see freezePrompt).
	frozen bool
	// carry holds the wrap flags of the line's last row when it carries on
	// into the next row although the two are laid out apart, because one
	// of them is frozen. A padding column the row ends in stays, flagged.
	carry rowFlag
}

// reflowSource is what a reflow takes in: the lines, and for each row it read
// the line it belongs to and the column of the line the row starts at.
type reflowSource struct {
	lines    []logicalLine
	rowLine  []int
	rowStart []int
}

// cellCols is how many columns c takes, counting a narrow cell as one.
func cellCols(c *uv.Cell) int {
	return max(c.Width, 1)
}

// isWideSpacer reports whether c is the second half of a wide character.
func isWideSpacer(c *uv.Cell) bool {
	return c.Width == 0 && c.Content == ""
}

// startsWide reports whether a row's first character is a wide one.
func startsWide(cells uv.Line) bool {
	return len(cells) > 0 && cells[0].Width > 1
}

// joinRows turns rows into the lines they hold. A row that wrapped early
// before a wide character gives up its padding column, which the guest never
// wrote. The last row of a line drops its trailing blanks: they are the
// unwritten rest of the row, not text.
// sc, when not nil, lends the buffers.
func joinRows(rows []reflowRow, sc *reflowScratch) reflowSource {
	var at []int
	var lines []logicalLine
	var all []uv.Line // every line's segments, in one slice
	if sc != nil {
		at = grow(sc.ints[:0], 2*len(rows))
		lines = growCap(sc.lines[:0], len(rows))
		all = growCap(sc.segs[:0], len(rows))
		sc.ints, sc.lines, sc.segs = at, lines, all
	} else {
		at = make([]int, 2*len(rows))
		lines = make([]logicalLine, 0, len(rows))
		all = make([]uv.Line, 0, len(rows))
	}
	src := reflowSource{
		rowLine:  at[:len(rows):len(rows)],
		rowStart: at[len(rows) : 2*len(rows)],
		lines:    lines,
	}
	var cur logicalLine
	first := 0
	for i, r := range rows {
		src.rowLine[i] = len(src.lines)
		src.rowStart[i] = cur.cols
		cells := r.cells
		cur.frozen = r.frozen
		wrapped := r.flags&rowWrapped != 0 && i+1 < len(rows)
		if wrapped && (r.frozen || rows[i+1].frozen) {
			// A frozen row is laid out on its own, but the text still
			// carries on across it: the row keeps its wrap flag.
			wrapped, cur.carry = false, r.flags&(rowWrapped|rowPadded)
		}
		if wrapped && r.flags&rowPadded != 0 && len(cells) > 0 &&
			isBlankCell(&cells[len(cells)-1]) && startsWide(rows[i+1].cells) {
			cells = cells[:len(cells)-1]
		}
		for x := range cells {
			if c := &cells[x]; !isWideSpacer(c) {
				cur.cols += cellCols(c)
			}
		}
		if len(cells) > 0 {
			all = append(all, cells)
		}
		if wrapped {
			continue
		}
		cur.segs = all[first:len(all):len(all)]
		for cur.carry == 0 && len(cur.segs) > 0 {
			seg := cur.segs[len(cur.segs)-1]
			for len(seg) > 0 && isBlankCell(&seg[len(seg)-1]) {
				seg = seg[:len(seg)-1]
				cur.cols--
			}
			if len(seg) > 0 {
				cur.segs[len(cur.segs)-1] = seg
				break
			}
			cur.segs = cur.segs[:len(cur.segs)-1]
		}
		src.lines = append(src.lines, cur)
		cur = logicalLine{}
		first = len(all)
	}
	return src
}

// grow returns s with length n, reusing its storage when it is large enough.
func grow[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	s = s[:n]
	clear(s)
	return s
}

// growCap returns s emptied, with room for n, reusing its storage when it is
// large enough.
func growCap[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, 0, n)
	}
	return s[:0]
}

// offset is where point (row, col) of the source rows falls in its line, as
// a line index and a column of the line.
func (src *reflowSource) offset(row, col int) (line, at int) {
	return src.rowLine[row], src.rowStart[row] + col
}

// walk lays the line out in rows of width columns and calls fn with each
// character, the column of the line it starts at, the row and column it
// lands on, and whether it moved to a new row because it did not fit in the
// columns left on the row before. A character wider than the whole row lands
// as one column. fn returns false to stop. walk returns where a character
// after the last would land before any wrap, so endCol can equal width.
func (l *logicalLine) walk(width int, fn func(c *uv.Cell, off, row, col int, early bool) bool) (endRow, endCol int) {
	row, col, off := 0, 0, 0
	for _, seg := range l.segs {
		for x := range seg {
			c := &seg[x]
			if isWideSpacer(c) {
				continue
			}
			w := min(cellCols(c), width)
			early := false
			if l.frozen && col+w > width {
				return row, col // the rest is the row's tail
			}
			if col+w > width {
				early = col < width
				row++
				col = 0
			}
			if fn != nil && !fn(c, off, row, col, early) {
				return row, col
			}
			off += cellCols(c)
			col += w
		}
	}
	return row, col
}

// rows is the number of rows the line takes at width, counting the rows
// past the text that keep has to hold.
func (l *logicalLine) rows(width int) int {
	if l.frozen {
		return 1
	}
	endRow, endCol := l.walk(width, nil)
	n := endRow + 1
	if l.keep > l.cols {
		n += (endCol + l.keep - l.cols - 1) / width
	}
	return n
}

// reflowPlan is a run of lines laid out at a width, not yet applied.
type reflowPlan struct {
	src     reflowSource
	width   int
	lineRow []int // the first row of each line, then the total
	cells   []uv.Cell
	flags   []rowFlag
	rowOff  []int     // the column of its line each row starts at
	tails   []uv.Line // a frozen row's cells past the width, or nil
}

// newPlan lays out src at width, into one block of cells for all its rows.
// sc, when not nil, lends the buffers other than the cells.
func newPlan(src reflowSource, width int, sc *reflowScratch) reflowPlan {
	p := reflowPlan{src: src, width: width}
	if sc != nil {
		// The scratch's ints hold the source's row tables, so the line
		// table goes after them.
		used := 2 * len(src.rowLine)
		ints := sc.ints
		if cap(ints) < used+len(src.lines)+1 {
			ints = append(ints[:used:used], make([]int, len(src.lines)+1)...)
			sc.ints = ints
		}
		p.lineRow = ints[used : used+len(src.lines)+1]
		p.lineRow[0] = 0
	} else {
		p.lineRow = make([]int, len(src.lines)+1)
	}
	for i := range src.lines {
		p.lineRow[i+1] = p.lineRow[i] + src.lines[i].rows(width)
	}
	total := p.lineRow[len(src.lines)]
	if sc != nil && cap(sc.spare) >= total*width {
		p.cells = sc.spare[:total*width]
		sc.spare = nil
	} else {
		p.cells = make([]uv.Cell, total*width)
	}
	for i := range p.cells {
		p.cells[i] = uv.EmptyCell
	}
	if sc != nil {
		sc.flags = grow(sc.flags, total)
		sc.rowOff = grow(sc.rowOff, total)
		sc.tails = grow(sc.tails, total)
		p.flags, p.rowOff, p.tails = sc.flags, sc.rowOff, sc.tails
	} else {
		p.flags = make([]rowFlag, total)
		p.rowOff = make([]int, total)
		p.tails = make([]uv.Line, total)
	}
	for i := range src.lines {
		line := &src.lines[i]
		base := p.lineRow[i]
		for r := base; r < p.lineRow[i+1]-1; r++ {
			p.flags[r] = rowWrapped
		}
		placed := 0
		endRow, endCol := line.walk(width, func(c *uv.Cell, off, row, col int, early bool) bool {
			r := base + row
			placed++
			if col == 0 {
				p.rowOff[r] = off
			}
			if early {
				// The row before stopped short of the edge, before a wide
				// character that did not fit: its last column is padding.
				p.flags[r-1] |= rowPadded
			}
			cell := *c
			if cellCols(c) > width {
				cell = uv.Cell{Content: " ", Width: 1, Style: c.Style, Link: c.Link}
			}
			p.row(r).Set(col, &cell)
			return true
		})
		// Rows past the text that keep holds count on from its end.
		for r := base + endRow + 1; r < p.lineRow[i+1]; r++ {
			p.rowOff[r] = line.cols + (width - endCol) + (r-base-endRow-1)*width
		}
		if line.frozen {
			p.tails[base] = frozenTail(line, placed)
		}
		if line.carry != 0 {
			p.flags[p.lineRow[i+1]-1] |= line.carry
		}
	}
	return p
}

// frozenTail copies the cells of a frozen line after the first placed
// characters: what does not fit on its one row. It is a copy because the
// cells it comes from are about to be reused.
func frozenTail(line *logicalLine, placed int) uv.Line {
	var tail uv.Line
	n := 0
	for _, seg := range line.segs {
		for x := range seg {
			if !isWideSpacer(&seg[x]) {
				n++
			}
			if n > placed {
				tail = append(tail, seg[x])
			}
		}
	}
	return tail
}

// row is laid out row r.
func (p *reflowPlan) row(r int) uv.Line { return p.cells[r*p.width : (r+1)*p.width] }

// locate is where source row row, column col went, as a laid out row and a
// column. A column inside a wide character goes to that character. A column
// past the text counts on from the end of it. One past the last row the line
// has stays on that row, at a column past its last one: a mark the shell
// left after the text stays after it, even where the text fills the row, and
// a saved cursor keeps how far past the text it was. A cursor cannot stand
// there; the callers clamp as each needs.
//
// The row is found by a binary search over where each row starts, so a
// reflow that carries many marks, or a remap over many placements, costs a
// search each and not a walk of the line.
func (p *reflowPlan) locate(row, col int) (int, int) {
	l, at := p.src.offset(row, col)
	if last := len(p.src.lines) - 1; l > last {
		l, at = last, p.src.lines[last].cols
	}
	a, b := p.lineRow[l], p.lineRow[l+1]
	r := a + sort.Search(b-a, func(i int) bool { return p.rowOff[a+i] > at }) - 1
	r = max(r, a)
	return r, max(at-p.rowOff[r], 0)
}

// freezePrompt keeps rows start to end out of the reflow: each stays one row,
// and keeps its wrap flag, so the text reads the same across it. What does
// not fit the new width is kept as the row's tail (grid.tail), not cut, and
// comes back when the row is laid out wider again, unless the guest writes
// to the row first.
//
// Those rows are a prompt the shell marked with OSC 133 and is still editing,
// and the shell repaints it on SIGWINCH. It repaints by stepping up the
// number of rows the prompt took at the old width, so a prompt that reflow
// made a row taller leaves its first row behind, once per resize: fish's
// prompt fills the pane's width exactly and loses a row to every narrowing.
// Without the marks the prompt reflows like any other text.
func freezePrompt(rows []reflowRow, start, end int) {
	start, end = max(start, 0), min(end, len(rows)-1)
	for i := start; i <= end; i++ {
		rows[i].frozen = true
	}
}

// fitsWithoutReflow reports whether a resize of the main screen to width x
// height lays out exactly as cutting and padding the rows does, so the reflow
// can be skipped: no row carries on into another, every row's text fits the
// new width, the cursor and the saved cursor do too, and the screen takes
// nothing back from the history. A shell that prints short lines is that
// case, and a window drag then costs what it did before reflow.
func (s *Screen) fitsWithoutReflow(width, height int) bool {
	h0 := s.buf.Height()
	if s.cur.X >= width || s.saved.X >= width || s.buf.hasTail() || s.cursorCol() != s.cur.X {
		return false
	}
	if n := s.scrollback.Len(); n > 0 {
		if s.scrollback.lineFlags(n-1)&rowWrapped != 0 {
			return false
		}
		if height > h0 && s.cur.Y == h0-1 {
			return false
		}
	}
	for y := range h0 {
		if s.buf.wrap[y]&rowWrapped != 0 {
			return false
		}
		if width < s.buf.width && s.rowExtent(y) > width {
			return false
		}
	}
	return true
}

// rowExtent is the column after row y's last cell that is not a plain blank.
func (s *Screen) rowExtent(y int) int {
	row := s.buf.rows[y]
	n := min(s.buf.ext[y], len(row))
	for n > 0 && isBlankCell(&row[n-1]) {
		n--
	}
	return n
}

// historyRows reads history rows from to end-1 for a reflow.
// extra is room to leave for rows the caller appends.
func historyRows(sb *Scrollback, from, end, extra int, sc *reflowScratch) []reflowRow {
	var rows []reflowRow
	var lines []uv.Line
	if sc != nil {
		rows = growCap(sc.rows[:0], end-from+extra)
		lines, sc.hist = sb.decodeRows(from, end, sc.hist, sc.histRows)
		sc.histRows = lines
	} else {
		rows = make([]reflowRow, 0, end-from+extra)
		lines, _ = sb.decodeRows(from, end, nil, nil)
	}
	for i, cells := range lines {
		rows = append(rows, reflowRow{cells: cells, flags: sb.lineFlags(from + i)})
	}
	return rows
}

// reflowHistoryCap bounds how many rows of history a reflow takes, as a
// multiple of the screen height. A line that started further back than that
// is laid out from a row boundary inside it. The rows before it stay as they
// were, still marked as carrying on, so no text is lost and the line still
// reads whole; it only keeps its old break at that one boundary.
const reflowHistoryCap = 4

// reflow lays the main screen out again at width x height.
//
// It takes the history rows of the line the screen's first row continues.
// When bottom is set, because the cursor was on the last row, the cursor
// stays on the last row: a screen with room to spare takes history lines back
// to fill it, as ghostty and tmux do, so narrowing and widening again leaves
// a shell where it was. Otherwise the screen's first row stays at the top.
// Either way, rows the screen has no room for go into the history, and the
// cursor moves with the text it is on.
//
// prompt is the row, counted from the oldest history row, where a prompt the
// shell marked with OSC 133 starts and is still open, or -1. The rows from
// there on are not reflowed but cut at the new width (freezePrompt).
//
// marks are positions counted from the oldest history row that move with
// their text. They are rewritten in place. The returned remap does the same
// for a row's first column, before any line the pushes evicted from a full
// ring is taken off.
func (s *Screen) reflow(width, height int, bottom bool, phantom *bool, prompt int, marks []*reflowPoint, wantRemap bool) (remap func(abs int) int) {
	sb := s.scrollback
	n0 := sb.Len()
	h0 := s.buf.Height()
	w0 := s.buf.Width()
	limit := reflowHistoryCap * max(height, h0)
	sc := &s.rf

	// take reads the history rows from from on, then the screen, and joins
	// them into lines.
	take := func(from int) ([]reflowRow, reflowSource) {
		rows := historyRows(sb, from, n0, h0, sc)
		for y := range h0 {
			cells := s.buf.rows[y]
			if cells == nil {
				if len(sc.blank) != w0 {
					sc.blank = newBlankLine(w0)
				}
				cells = sc.blank // read only, shared by every unwritten row
			}
			if t := s.buf.rowTail(y); len(t) > 0 {
				cells = append(cells[:len(cells):len(cells)], t...)
			}
			rows = append(rows, reflowRow{cells: cells, flags: s.buf.wrap[y]})
		}
		sc.rows = rows
		curRow := n0 - from + s.cur.Y
		if prompt >= 0 {
			// The prompt runs from its mark to the end of the line the
			// cursor is on: the command being typed can carry on past it.
			end := curRow
			for end+1 < len(rows) && rows[end].flags&rowWrapped != 0 {
				end++
			}
			freezePrompt(rows, prompt-from, end)
		}
		src := joinRows(rows, sc)
		// The cell under the cursor has to exist after the reflow, even
		// past the end of the text, where a shell's cursor stands after
		// its prompt.
		cl, cat := src.offset(curRow, s.cursorCol())
		src.lines[cl].keep = max(src.lines[cl].keep, cat+1)

		// Lines past the cursor's that hold nothing are the unwritten
		// bottom of the screen.
		last := len(src.lines) - 1
		for last > cl && len(src.lines[last].segs) == 0 {
			last--
		}
		src.lines = src.lines[:last+1]
		return rows, src
	}
	countRows := func(src *reflowSource) int {
		n := 0
		for i := range src.lines {
			n += src.lines[i].rows(width)
		}
		return n
	}

	// The rows taken: the history rows of the line the screen continues,
	// then the screen.
	from := n0
	for from > 0 && n0-from < limit && sb.lineFlags(from-1)&rowWrapped != 0 {
		from--
	}
	rows, src := take(from)
	total := countRows(&src)

	// With the cursor on the last row and room to spare, history lines come
	// back onto the screen. The lines are counted first, then the rows are
	// taken again from the new start, so a line is laid out whole even when
	// it carries on from the history into the screen.
	if bottom && total < height {
		if sc.cnt == nil {
			sc.cnt = &reflowScratch{}
		}
		cnt := sc.cnt
		need := height - total
		back := from
		for need > 0 && back > 0 && n0-back < 2*limit {
			end := back
			lf := end - 1
			for lf > 0 && end-lf < limit && sb.lineFlags(lf-1)&rowWrapped != 0 {
				lf--
			}
			r := historyRows(sb, lf, end, 0, cnt)
			cnt.rows = r
			csrc := joinRows(r, cnt)
			need -= countRows(&csrc)
			back = lf
		}
		if back < from {
			from = back
			rows, src = take(from)
			total = countRows(&src)
		}
	}
	p := newPlan(src, width, sc)
	screenRow := n0 - from // the first screen row among the rows taken

	cr, cc := p.locate(screenRow+s.cur.Y, s.cursorCol())
	// A cursor on a frozen row can be past the new width. It stands on the
	// last column, and remembers where it was for the next reflow.
	wideX := cc
	cc = min(cc, width-1)
	newPhantom := false
	if phantom != nil && *phantom {
		// The cursor in pending wrap stands on the character it drew, with
		// the next one due after it. If that character now ends before the
		// last column, the next one simply goes after it.
		c := p.row(cr)[cc]
		if next := cc + cellCols(&c); next < width {
			cc = next
		} else {
			cc = width - 1
			newPhantom = true
		}
	}

	var top int
	if bottom {
		top = max(total-height, 0)
	} else {
		r0, _ := p.locate(screenRow, 0)
		top = max(r0, cr-height+1, total-height)
	}

	// Work out where the saved cursor and the marks go before the history
	// changes under them.
	taken := len(rows)
	savR, savC := p.locate(screenRow+s.saved.Y, s.saved.X)
	for _, m := range marks {
		if r := m.abs - from; m.abs >= from && r < taken {
			nr, nc := p.locate(r, m.col)
			m.abs, m.col = from+nr, min(nc, width)
		}
	}
	if wantRemap {
		// remap reads the scratch the plan's tables live in, so it holds
		// until the next resize.
		rp := p
		remap = func(abs int) int {
			if r := abs - from; abs >= from && r < taken {
				nr, _ := rp.locate(r, 0)
				return from + nr
			}
			if abs >= from+taken {
				// A row past the old screen: past the new one too.
				return abs - taken + total
			}
			return abs
		}
	}

	// Apply. The history gives back the rows taken, the laid out rows above
	// the screen go back into it, and the screen gets the rest. A push into
	// a full ring drops its oldest line, and the marks with it through the
	// ring's trim callback, after the marks have moved.
	sb.truncate(from)
	for r := range top {
		cells := p.row(r)
		if t := p.tails[r]; len(t) > 0 {
			// History keeps a row at any width, so a frozen row going
			// into it takes its tail back.
			cells = append(cells[:len(cells):len(cells)], t...)
		}
		sb.PushLine(cells)
		sb.markNewest(p.flags[r])
	}

	// The grid's tables are reused: the rows taken hold their own copy of
	// every row header the reflow reads.
	g := grid{
		rows:  grow(s.buf.rows, height),
		ext:   grow(s.buf.ext, height),
		wrap:  grow(s.buf.wrap, height),
		width: width,
	}
	if s.buf.tail != nil {
		g.tail = grow(s.buf.tail, height)
	}
	for y := range height {
		if top+y >= total {
			break
		}
		cells := p.row(top + y)
		ext := len(cells)
		for ext > 0 && isBlankCell(&cells[ext-1]) {
			ext--
		}
		if ext > 0 || len(p.tails[top+y]) > 0 {
			// A full slice expression, so the grid row cannot grow into the
			// next row of the plan's block.
			g.rows[y] = cells[:len(cells):len(cells)]
			g.ext[y] = ext
		}
		g.wrap[y] = p.flags[top+y]
		if t := p.tails[top+y]; len(t) > 0 {
			g.setTail(y, t)
		}
	}
	// The last screen row cannot carry on into a row below the screen.
	g.wrap[height-1] = 0
	*s.buf = g
	// The block the old rows were in is free now: the next reflow lays out
	// into it.
	sc.spare, sc.block = sc.block, p.cells

	s.cur.X, s.cur.Y = cc, clamp(cr-top, 0, height-1)
	s.wideCol = wideX > cc && !newPhantom
	s.wideColX, s.wideColAt = wideX, s.cur.Position
	if savR < top {
		// The saved cursor's text went into the history. It comes back to
		// the top of the screen at its first column, rather than at a
		// column of whatever row is there now, which the next reflow would
		// otherwise have to keep room for.
		savR, savC = top, 0
	}
	// The saved column is kept past the width when the text it followed
	// fills the row: DECRC clamps it, and the next reflow finds the same
	// place in the line from it.
	s.saved.X, s.saved.Y = savC, clamp(savR-top, 0, height-1)
	if phantom != nil {
		*phantom = newPhantom
	}
	s.scroll = s.buf.Bounds()
	return remap
}
