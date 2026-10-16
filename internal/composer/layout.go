package composer

import "sort"

// vline is one row of the draft on screen: the cells [start, end). A row
// that ends on a line break does not hold the break.
type vline struct {
	start, end int
	// soft says the row was wrapped, so the next row starts at end and the
	// cursor cannot stand at end on this row.
	soft bool
}

// layout is the draft broken into rows for one width. It is kept until the
// draft or the width changes, so a frame with no edit does no work.
type layout struct {
	ver   uint64
	width int
	rows  []vline
}

// cellWidth is c's width on a row of width cells. A block wider than the row
// is cut to it when drawn.
func cellWidth(c cell, width int) int {
	return min(c.w, width)
}

// rows breaks the draft into rows of at most width cells, at a space when
// the row has one and inside a word when it does not.
func (m *Model) rows(width int) []vline {
	width = max(width, 1)
	if m.lay.rows != nil && m.lay.ver == m.ver && m.lay.width == width {
		return m.lay.rows
	}
	rows := m.lay.rows[:0]
	cells := m.cells
	start, w, brk := 0, 0, -1
	for i := 0; i < len(cells); {
		c := &cells[i]
		if c.s == "\n" {
			rows = append(rows, vline{start: start, end: i})
			start, w, brk = i+1, 0, -1
			i++
			continue
		}
		cw := cellWidth(*c, width)
		if w+cw > width && i > start {
			if brk > start {
				rows = append(rows, vline{start: start, end: brk, soft: true})
				w = 0
				for _, k := range cells[brk:i] {
					w += cellWidth(k, width)
				}
				start = brk
			} else {
				rows = append(rows, vline{start: start, end: i, soft: true})
				start, w = i, 0
			}
			brk = -1
			continue
		}
		w += cw
		if c.isSpace() {
			brk = i + 1
		}
		i++
	}
	rows = append(rows, vline{start: start, end: len(cells)})
	m.lay = layout{ver: m.ver, width: width, rows: rows}
	return rows
}

// rowOf is the row the cursor at index i stands on: the last row that starts
// at or before i. The end of a wrapped row is the start of the next one, so
// the cursor there is drawn at the start of the next row.
func rowOf(rows []vline, i int) int {
	return max(sort.Search(len(rows), func(r int) bool { return rows[r].start > i })-1, 0)
}

// colOf is how many cells from the start of its row the index i is.
func (m *Model) colOf(row vline, i, width int) int {
	col := 0
	for _, c := range m.cells[row.start:min(i, row.end)] {
		col += cellWidth(c, width)
	}
	return col
}

// indexAt is the index on row whose column is nearest col without passing
// it. A wrapped row cannot take the cursor at its end, which is the next
// row's start.
func (m *Model) indexAt(row vline, col, width int) int {
	last := row.end
	if row.soft && last > row.start {
		last--
	}
	w := 0
	for i := row.start; i < last; i++ {
		cw := cellWidth(m.cells[i], width)
		if w+cw > col {
			// Nearer to the cell's start or to its end.
			if col-w > (w+cw)-col {
				return i + 1
			}
			return i
		}
		w += cw
	}
	return last
}
