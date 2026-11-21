package vt

import (
	"slices"

	uv "github.com/charmbracelet/ultraviolet"
)

type reflowScratch struct {
	rows         []reflowRow
	ints         []int
	lines        []logicalLine
	segs         []uv.Line
	flags        []rowFlag
	hist         []uv.Cell
	histRows     []uv.Line
	block, spare []uv.Cell
	blank        uv.Line
	rowOff       []int
	tails        []uv.Line
	cnt          *reflowScratch
}

// ReleaseReflowScratch drops the buffers the main screen keeps from one
// reflow to the next. They make a drag cheap and are worth nothing to a pane
// that is not being resized, yet after two reflows they hold a spare block
// the size of the last layout and the history rows that layout decoded:
// 1.7 MB at 171x83. The next reflow allocates them again. The rows on screen
// keep the block they are in alive on their own, and a remap handed out by
// the last reflow keeps the tables it reads, so only what nothing reads is
// freed. It also gives back the escape-string buffer a large OSC, DCS or APC
// payload grew, which is worth nothing between sequences.
func (e *Emulator) ReleaseReflowScratch() {
	e.scrs[0].rf = reflowScratch{}
	e.parser.releaseData()
}

type reflowRow struct {
	cells  uv.Line
	flags  rowFlag
	frozen bool
}

type reflowPoint struct {
	abs, col int
}

type logicalLine struct {
	segs   []uv.Line
	cols   int
	keep   int
	frozen bool
	carry  rowFlag
}

type reflowSource struct {
	lines    []logicalLine
	rowLine  []int
	rowStart []int
}

func cellCols(c *uv.Cell) int {
	return max(c.Width, 1)
}

func isWideSpacer(c *uv.Cell) bool {
	return c.Width == 0 && c.Content == ""
}

func startsWide(cells uv.Line) bool {
	return len(cells) > 0 && cells[0].Width > 1
}

func joinRows(rows []reflowRow, sc *reflowScratch) reflowSource {
	var at []int
	var lines []logicalLine
	var all []uv.Line
	if sc != nil {
		at = grow(sc.ints[:0], 2*len(rows))
		lines = slices.Grow(sc.lines[:0], len(rows))
		all = slices.Grow(sc.segs[:0], len(rows))
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

func grow[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	s = s[:n]
	clear(s)
	return s
}

func (src *reflowSource) offset(row, col int) (line, at int) {
	return src.rowLine[row], src.rowStart[row] + col
}

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
				return row, col
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

type reflowPlan struct {
	src     reflowSource
	width   int
	lineRow []int
	cells   []uv.Cell
	flags   []rowFlag
	rowOff  []int
	tails   []uv.Line
}

func newPlan(src reflowSource, width int, sc *reflowScratch) reflowPlan {
	p := reflowPlan{src: src, width: width}
	if sc != nil {
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
				p.flags[r-1] |= rowPadded
			}
			cell := *c
			if cellCols(c) > width {
				cell = uv.Cell{Content: " ", Width: 1, Style: c.Style, Link: c.Link}
			}
			p.row(r).Set(col, &cell)
			return true
		})
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

func (p *reflowPlan) row(r int) uv.Line { return p.cells[r*p.width : (r+1)*p.width] }

func (p *reflowPlan) locate(row, col int) (int, int) {
	l, at := p.src.offset(row, col)
	if last := len(p.src.lines) - 1; l > last {
		l, at = last, p.src.lines[last].cols
	}
	a, b := p.lineRow[l], p.lineRow[l+1]
	i, found := slices.BinarySearch(p.rowOff[a:b], at)
	if !found {
		i--
	}
	r := max(a+i, a)
	return r, max(at-p.rowOff[r], 0)
}

func freezePrompt(rows []reflowRow, start, end int) {
	start, end = max(start, 0), min(end, len(rows)-1)
	for i := start; i <= end; i++ {
		rows[i].frozen = true
	}
}

func (s *screen) fitsWithoutReflow(width, height int) bool {
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

func (s *screen) rowExtent(y int) int {
	row := s.buf.rows[y]
	n := min(s.buf.ext[y], len(row))
	for n > 0 && isBlankCell(&row[n-1]) {
		n--
	}
	return n
}

func historyRows(sb *Scrollback, from, end, extra int, sc *reflowScratch) []reflowRow {
	var rows []reflowRow
	var lines []uv.Line
	if sc != nil {
		rows = slices.Grow(sc.rows[:0], end-from+extra)
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

const reflowHistoryCap = 4

func (s *screen) reflow(width, height int, bottom bool, phantom *bool, prompt int, marks []*reflowPoint, wantRemap bool) (remap func(abs int) int) {
	sb := s.scrollback
	n0 := sb.Len()
	h0 := s.buf.Height()
	w0 := s.buf.Width()
	limit := reflowHistoryCap * max(height, h0)
	sc := &s.rf

	take := func(from int) ([]reflowRow, reflowSource) {
		rows := historyRows(sb, from, n0, h0, sc)
		for y := range h0 {
			cells := s.buf.rows[y]
			if cells == nil {
				if len(sc.blank) != w0 {
					sc.blank = newBlankLine(w0)
				}
				cells = sc.blank
			}
			if t := s.buf.rowTail(y); len(t) > 0 {
				cells = append(cells[:len(cells):len(cells)], t...)
			}
			rows = append(rows, reflowRow{cells: cells, flags: s.buf.wrap[y]})
		}
		sc.rows = rows
		curRow := n0 - from + s.cur.Y
		if prompt >= 0 {
			end := curRow
			for end+1 < len(rows) && rows[end].flags&rowWrapped != 0 {
				end++
			}
			freezePrompt(rows, prompt-from, end)
		}
		src := joinRows(rows, sc)
		cl, cat := src.offset(curRow, s.cursorCol())
		src.lines[cl].keep = max(src.lines[cl].keep, cat+1)

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

	from := n0
	for from > 0 && n0-from < limit && sb.lineFlags(from-1)&rowWrapped != 0 {
		from--
	}
	rows, src := take(from)
	total := countRows(&src)

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
	screenRow := n0 - from

	cr, cc := p.locate(screenRow+s.cur.Y, s.cursorCol())
	wideX := cc
	cc = min(cc, width-1)
	newPhantom := false
	if phantom != nil && *phantom {
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

	taken := len(rows)
	savR, savC := p.locate(screenRow+s.saved.Y, s.saved.X)
	for _, m := range marks {
		if r := m.abs - from; m.abs >= from && r < taken {
			nr, nc := p.locate(r, m.col)
			m.abs, m.col = from+nr, min(nc, width)
		}
	}
	if wantRemap {
		rp := p
		remap = func(abs int) int {
			if r := abs - from; abs >= from && r < taken {
				nr, _ := rp.locate(r, 0)
				return from + nr
			}
			if abs >= from+taken {
				return abs - taken + total
			}
			return abs
		}
	}

	sb.truncate(from)
	for r := range top {
		cells := p.row(r)
		if t := p.tails[r]; len(t) > 0 {
			cells = append(cells[:len(cells):len(cells)], t...)
		}
		sb.PushLine(cells)
		sb.markNewest(p.flags[r])
	}

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
			g.rows[y] = cells[:len(cells):len(cells)]
			g.ext[y] = ext
		}
		g.wrap[y] = p.flags[top+y]
		if t := p.tails[top+y]; len(t) > 0 {
			g.setTail(y, t)
		}
	}
	g.wrap[height-1] = 0
	*s.buf = g
	sc.spare, sc.block = sc.block, p.cells

	s.cur.X, s.cur.Y = cc, clamp(cr-top, 0, height-1)
	s.wideCol = wideX > cc && !newPhantom
	s.wideColX, s.wideColAt = wideX, s.cur.Position
	if savR < top {
		savR, savC = top, 0
	}
	s.saved.X, s.saved.Y = savC, clamp(savR-top, 0, height-1)
	if phantom != nil {
		*phantom = newPhantom
	}
	s.scroll = s.buf.Bounds()
	return remap
}
