package composer

import "sort"

type vline struct {
	start, end int
	soft       bool
}

type layout struct {
	ver   uint64
	width int
	rows  []vline
}

func cellWidth(c cell, width int) int {
	return min(c.w, width)
}

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

func rowOf(rows []vline, i int) int {
	return max(sort.Search(len(rows), func(r int) bool { return rows[r].start > i })-1, 0)
}

func (m *Model) colOf(row vline, i, width int) int {
	col := 0
	for _, c := range m.cells[row.start:min(i, row.end)] {
		col += cellWidth(c, width)
	}
	return col
}

func (m *Model) indexAt(row vline, col, width int) int {
	last := row.end
	if row.soft && last > row.start {
		last--
	}
	w := 0
	for i := row.start; i < last; i++ {
		cw := cellWidth(m.cells[i], width)
		if w+cw > col {
			if col-w > (w+cw)-col {
				return i + 1
			}
			return i
		}
		w += cw
	}
	return last
}
