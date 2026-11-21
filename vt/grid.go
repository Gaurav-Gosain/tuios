package vt

import (
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

type grid struct {
	rows    []uv.Line
	width   int
	ext     []int
	wrap    []rowFlag
	tail    []uv.Line
	prot    [][]bool
	rowsWin rowWindow[uv.Line]
	extWin  rowWindow[int]
	wrapWin rowWindow[rowFlag]
	tailWin rowWindow[uv.Line]
	protWin rowWindow[[]bool]
}

type rowWindow[T any] struct {
	base []T
	off  int
}

func (w *rowWindow[T]) scroll(cur []T, n int) []T {
	h := len(cur)
	if n <= 0 || n >= h {
		return cur
	}
	if w.off+h > len(w.base) || &w.base[w.off] != &cur[0] {
		w.base = make([]T, 2*h)
		copy(w.base, cur)
		w.off = 0
	} else if w.off+h+n > len(w.base) {
		copy(w.base, w.base[w.off:w.off+h])
		clear(w.base[h:])
		w.off = 0
	}
	copy(w.base[w.off+h:w.off+h+n], w.base[w.off:w.off+n])
	clear(w.base[w.off : w.off+n])
	w.off += n
	return w.base[w.off : w.off+h : w.off+h]
}

func (g *grid) scrollWindow(n int) {
	g.rows = g.rowsWin.scroll(g.rows, n)
	g.ext = g.extWin.scroll(g.ext, n)
	g.wrap = g.wrapWin.scroll(g.wrap, n)
	if g.tail != nil {
		g.tail = g.tailWin.scroll(g.tail, n)
	}
	if g.prot != nil {
		g.prot = g.protWin.scroll(g.prot, n)
	}
}

func (g *grid) Protected(x, y int) bool {
	if y < 0 || y >= len(g.prot) || x < 0 {
		return false
	}
	row := g.prot[y]
	return x < len(row) && row[x]
}

func (g *grid) setProtected(x, y, n int, on bool) {
	if y < 0 || y >= len(g.rows) {
		return
	}
	x0, x1 := max(x, 0), min(x+n, g.width)
	if x0 >= x1 {
		return
	}
	if !on {
		g.clearProtected(y, x0, x1)
		return
	}
	if g.prot == nil {
		g.prot = make([][]bool, len(g.rows))
	}
	if g.prot[y] == nil {
		g.prot[y] = make([]bool, g.width)
	}
	for i := x0; i < x1; i++ {
		g.prot[y][i] = true
	}
}

func (g *grid) clearProtected(y, x0, x1 int) {
	if y < 0 || y >= len(g.prot) || g.prot[y] == nil {
		return
	}
	row := g.prot[y]
	for i := max(x0, 0); i < x1 && i < len(row); i++ {
		row[i] = false
	}
}

func (g *grid) clearProtectedRows(y, end int) {
	if g.prot == nil {
		return
	}
	clear(g.prot[max(y, 0):min(end, len(g.prot))])
}

func (g *grid) shiftProtected(y, dst, src, n int) {
	if y < 0 || y >= len(g.prot) || g.prot[y] == nil || n <= 0 {
		return
	}
	copy(g.prot[y][dst:dst+n], g.prot[y][src:src+n])
}

func (g *grid) protectedRuns() []CellRun {
	var runs []CellRun
	for y, row := range g.prot {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < len(row) && row[x] {
				x++
			}
			runs = append(runs, CellRun{X: start, Y: y, N: x - start})
		}
	}
	return runs
}

func (g *grid) restoreProtected(runs []CellRun) {
	g.prot = nil
	for _, r := range runs {
		if r.N > 0 {
			g.setProtected(r.X, r.Y, min(r.N, g.width), true)
		}
	}
}

type rowFlag uint8

const (
	rowWrapped rowFlag = 1 << iota
	rowPadded
)

func wrapFlag(wrapped bool) rowFlag {
	if wrapped {
		return rowWrapped
	}
	return 0
}

func (g *grid) rowTail(y int) uv.Line {
	if g.tail == nil || y < 0 || y >= len(g.tail) {
		return nil
	}
	return g.tail[y]
}

func (g *grid) setTail(y int, t uv.Line) {
	if g.tail == nil {
		g.tail = make([]uv.Line, len(g.rows))
	}
	g.tail[y] = t
}

func (g *grid) dropTail(y int) {
	if g.tail != nil && y >= 0 && y < len(g.tail) {
		g.tail[y] = nil
	}
}

func (g *grid) hasTail() bool {
	for _, t := range g.tail {
		if t != nil {
			return true
		}
	}
	return false
}

func (g *grid) withTail(y int, row uv.Line) uv.Line {
	t := g.rowTail(y)
	if len(t) == 0 {
		return row
	}
	return append(row[:len(row):len(row)], t...)
}

var gridBlank = uv.EmptyCell

func newGrid(width, height int) *grid {
	return &grid{rows: make([]uv.Line, height), ext: make([]int, height), wrap: make([]rowFlag, height), width: width}
}

func (g *grid) raiseExt(y, end int) {
	if end > g.ext[y] {
		g.ext[y] = min(end, g.width)
	}
}

func (g *grid) Width() int { return g.width }

func (g *grid) Height() int { return len(g.rows) }

func (g *grid) Bounds() uv.Rectangle { return uv.Rect(0, 0, g.width, len(g.rows)) }

func (g *grid) CellAt(x, y int) *uv.Cell {
	if y < 0 || y >= len(g.rows) || x < 0 || x >= g.width {
		return nil
	}
	row := g.rows[y]
	if row == nil {
		return &gridBlank
	}
	return &row[x]
}

func (g *grid) Row(y int) uv.Line {
	if y < 0 || y >= len(g.rows) {
		return nil
	}
	return g.rows[y]
}

func (g *grid) row(y int) uv.Line {
	g.dropTail(y)
	g.ext[y] = g.width
	if g.rows[y] == nil {
		g.rows[y] = newBlankLine(g.width)
	}
	return g.rows[y]
}

func newBlankLine(width int) uv.Line {
	line := make(uv.Line, width)
	for x := range line {
		line[x] = uv.EmptyCell
	}
	return line
}

func isBlankFill(c *uv.Cell) bool {
	return c == nil || isBlankCell(c)
}

func (g *grid) SetCell(x, y int, c *uv.Cell) {
	if y < 0 || y >= len(g.rows) {
		return
	}
	g.dropTail(y)
	if g.rows[y] == nil {
		if isBlankFill(c) || x < 0 || x >= g.width {
			return
		}
		g.rows[y] = newBlankLine(g.width)
	}
	g.rows[y].Set(x, c)
	if g.prot != nil {
		w := 1
		if c != nil {
			w = max(c.Width, 1)
		}
		g.clearProtected(y, x, x+w)
	}
	if !isBlankFill(c) && x >= 0 {
		g.raiseExt(y, x+max(c.Width, 1))
	}
}

func (g *grid) Resize(width, height int) {
	if width != g.width {
		for y, row := range g.rows {
			if row == nil {
				continue
			}
			if width > len(row) {
				g.rows[y] = append(row, newBlankLine(width-len(row))...)
			} else {
				g.rows[y] = row[:width]
			}
		}
		g.width = width
		for y := range g.ext {
			g.ext[y] = min(g.ext[y], width)
		}
		clear(g.wrap)
		g.tail = nil
		for y, row := range g.prot {
			if row == nil {
				continue
			}
			if width > len(row) {
				g.prot[y] = append(row, make([]bool, width-len(row))...)
			} else {
				g.prot[y] = row[:width]
			}
		}
	}
	if height > len(g.rows) {
		g.ext = append(g.ext, make([]int, height-len(g.rows))...)
		g.wrap = append(g.wrap, make([]rowFlag, height-len(g.rows))...)
		if g.tail != nil {
			g.tail = append(g.tail, make([]uv.Line, height-len(g.rows))...)
		}
		if g.prot != nil {
			g.prot = append(g.prot, make([][]bool, height-len(g.rows))...)
		}
		g.rows = append(g.rows, make([]uv.Line, height-len(g.rows))...)
	} else if height < len(g.rows) {
		clear(g.rows[height:])
		g.rows = g.rows[:height]
		g.ext = g.ext[:height]
		g.wrap = g.wrap[:height]
		if g.tail != nil {
			clear(g.tail[height:])
			g.tail = g.tail[:height]
		}
		if g.prot != nil {
			clear(g.prot[height:])
			g.prot = g.prot[:height]
		}
	}
}

func (g *grid) Clear() {
	for y, row := range g.rows {
		for x := range row[:g.ext[y]] {
			row[x] = uv.EmptyCell
		}
		g.ext[y] = 0
	}
	clear(g.wrap)
	clear(g.tail)
	g.prot = nil
}

func (g *grid) SoftWrapped(y int) bool {
	return y >= 0 && y < len(g.wrap) && g.wrap[y]&rowWrapped != 0
}

func (g *grid) setSoftWrapped(y int, wrapped bool) {
	if y >= 0 && y < len(g.wrap) {
		g.wrap[y] = wrapFlag(wrapped)
	}
}

func (g *grid) setPadded(y int) {
	if y >= 0 && y < len(g.wrap) {
		g.wrap[y] |= rowPadded
	}
}

func (g *grid) ClearArea(area uv.Rectangle) {
	g.FillArea(nil, area)
}

func (g *grid) FillArea(c *uv.Cell, area uv.Rectangle) {
	if area.Max.X >= g.width && area.Min.X < g.width {
		for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.wrap); y++ {
			g.wrap[y] = 0
		}
	}
	for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.tail); y++ {
		g.tail[y] = nil
	}
	for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.prot); y++ {
		g.clearProtected(y, area.Min.X, area.Max.X)
	}
	blank := isBlankFill(c)
	if c != nil && c.Width > 1 {
		for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.rows); y++ {
			if g.rows[y] == nil && blank {
				continue
			}
			for x := area.Min.X; x < area.Max.X; x += c.Width {
				g.SetCell(x, y, c)
			}
		}
		return
	}
	fill := uv.EmptyCell
	if c != nil {
		fill = *c
	}
	x0 := max(area.Min.X, 0)
	for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.rows); y++ {
		x1 := min(area.Max.X, g.width)
		if blank {
			x1 = min(x1, g.ext[y])
		}
		if x0 >= x1 {
			continue
		}
		if g.rows[y] == nil {
			g.rows[y] = newBlankLine(g.width)
		}
		row := g.rows[y]
		row.Set(x0, c)
		row.Set(x1-1, c)
		for x := x0; x < x1; x++ {
			row[x] = fill
		}
		switch {
		case !blank:
			g.raiseExt(y, x1)
		case x1 == g.ext[y]:
			g.ext[y] = x0
		}
	}
}

func (g *grid) fullWidth(area uv.Rectangle) bool {
	return area.Min.X <= 0 && area.Max.X >= g.width
}

func (g *grid) blankRows(y, end int, c *uv.Cell) {
	clear(g.wrap[y:end])
	if g.tail != nil {
		clear(g.tail[y:end])
	}
	g.clearProtectedRows(y, end)
	if isBlankFill(c) {
		for i := y; i < end; i++ {
			blankLine(g.rows[i][:g.ext[i]])
			g.ext[i] = 0
		}
		return
	}
	if c.Width > 1 {
		for i := y; i < end; i++ {
			for x := 0; x < g.width; x++ {
				g.SetCell(x, i, c)
			}
		}
		return
	}
	for i := y; i < end; i++ {
		row := g.row(i)
		for x := range row {
			row[x] = *c
		}
	}
}

func blankLine(line uv.Line) {
	for x := range line {
		if c := &line[x]; plainPen(c) {
			c.Content = " "
		} else {
			*c = uv.EmptyCell
		}
	}
}

func (g *grid) InsertLineArea(y, n int, c *uv.Cell, area uv.Rectangle) {
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= len(g.rows) {
		return
	}
	if y+n > area.Max.Y {
		n = area.Max.Y - y
	}
	end := min(area.Max.Y, len(g.rows))
	if y+n > end {
		n = end - y
	}

	if g.fullWidth(area) {
		var scratch [16]uv.Line
		var dropped []uv.Line
		if n <= len(scratch) {
			dropped = scratch[:n]
		} else {
			dropped = make([]uv.Line, n)
		}
		copy(dropped, g.rows[end-n:end])
		copy(g.rows[y+n:end], g.rows[y:end-n])
		copy(g.rows[y:y+n], dropped)
		g.rotateExt(y, end, end-n)
		g.blankRows(y, y+n, c)
		return
	}

	if !g.anyRow(y, end) && isBlankFill(c) {
		return
	}
	for i := y; i < end; i++ {
		g.row(i)
	}
	for i := end - 1; i >= y+n; i-- {
		for x := area.Min.X; x < area.Max.X; x++ {
			g.rows[i][x] = g.rows[i-n][x]
		}
		g.moveProtected(i, i-n, area.Min.X, area.Max.X)
	}
	for i := y; i < y+n; i++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			g.SetCell(x, i, c)
		}
	}
}

func (g *grid) DeleteLineArea(y, n int, c *uv.Cell, area uv.Rectangle) {
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= len(g.rows) {
		return
	}
	end := min(area.Max.Y, len(g.rows))
	if n > end-y {
		n = end - y
	}

	if g.fullWidth(area) {
		var scratch [16]uv.Line
		var dropped []uv.Line
		if n <= len(scratch) {
			dropped = scratch[:n]
		} else {
			dropped = make([]uv.Line, n)
		}
		copy(dropped, g.rows[y:y+n])
		copy(g.rows[y:end-n], g.rows[y+n:end])
		copy(g.rows[end-n:end], dropped)
		g.rotateExt(y, end, y+n)
		g.blankRows(end-n, end, c)
		return
	}

	if !g.anyRow(y, end) && isBlankFill(c) {
		return
	}
	for i := y; i < end; i++ {
		g.row(i)
	}
	for dst := y; dst < end-n; dst++ {
		src := dst + n
		for x := area.Min.X; x < area.Max.X; x++ {
			g.rows[dst][x] = g.rows[src][x]
		}
		g.moveProtected(dst, src, area.Min.X, area.Max.X)
	}
	for i := end - n; i < end; i++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			g.SetCell(x, i, c)
		}
	}
}

func (g *grid) rotateExt(y, end, mid int) {
	rotateLeft(g.ext[y:end], mid-y)
	rotateLeft(g.wrap[y:end], mid-y)
	if g.tail != nil {
		rotateLeft(g.tail[y:end], mid-y)
	}
	if g.prot != nil {
		rotateLeft(g.prot[y:end], mid-y)
	}
}

func (g *grid) moveProtected(dst, src, x0, x1 int) {
	if g.prot == nil {
		return
	}
	if g.prot[src] == nil {
		g.clearProtected(dst, x0, x1)
		return
	}
	if g.prot[dst] == nil {
		g.prot[dst] = make([]bool, g.width)
	}
	copy(g.prot[dst][x0:x1], g.prot[src][x0:x1])
}

func rotateLeft[T any](s []T, k int) {
	n := len(s)
	if k <= 0 || k >= n {
		return
	}
	var buf [16]T
	switch {
	case k <= len(buf):
		copy(buf[:k], s[:k])
		copy(s, s[k:])
		copy(s[n-k:], buf[:k])
	case n-k <= len(buf):
		m := n - k
		copy(buf[:m], s[k:])
		copy(s[m:], s[:k])
		copy(s, buf[:m])
	default:
		slices.Reverse(s[:k])
		slices.Reverse(s[k:])
		slices.Reverse(s)
	}
}

func (g *grid) anyRow(y, end int) bool {
	for i := y; i < end; i++ {
		if g.rows[i] != nil {
			return true
		}
	}
	return false
}

func (g *grid) String() string {
	var b strings.Builder
	for y, row := range g.rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		if row != nil {
			writeLineText(&b, row[:min(g.ext[y], len(row))])
		}
	}
	return b.String()
}

func writeLineText(b *strings.Builder, line uv.Line) {
	pending := 0
	for i := range line {
		c := &line[i]
		if c.IsZero() {
			continue
		}
		if isBlankCell(c) {
			pending++
			continue
		}
		for ; pending > 0; pending-- {
			b.WriteByte(' ')
		}
		b.WriteString(c.Content)
	}
}

func (g *grid) Render() string {
	var b strings.Builder
	for y, line := range g.rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		if line == nil {
			for range g.width {
				b.WriteByte(' ')
			}
			continue
		}
		renderRowBreakingClusters(&b, line)
	}
	return b.String()
}
