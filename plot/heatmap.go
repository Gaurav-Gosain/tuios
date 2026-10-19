package plot

import "math"

// Heatmap is a grid of values drawn as shades of the Heat1 to Heat5 roles,
// with row labels at the left, the first and last column labels under it,
// and a scale.
type Heatmap struct {
	Values    [][]float64 // rows of columns; NaN is an empty cell
	RowLabels []string
	ColLabels []string
	// Min and Max fix the range. Both 0 means the range of the values.
	Min, Max float64
	Format   func(float64) string
}

// asciiShades are the heat levels in ASCII, lightest first.
var asciiShades = [...]string{".", ":", "+", "#", "@"}

func (hm Heatmap) level(v, lo, hi float64) int {
	return max(0, min(len(heat)-1, int(math.Floor(unit(v, lo, hi)*float64(len(heat))*0.9999))))
}

// Draw draws the heatmap into a w by h grid. A row of values takes one
// row of cells when they fit, and half a row each (upper and lower half
// blocks) when they do not.
func (hm Heatmap) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	rows := len(hm.Values)
	cols := 0
	for _, r := range hm.Values {
		cols = max(cols, len(r))
	}
	if g.W == 0 || g.H == 0 || rows == 0 || cols == 0 {
		return g
	}
	lo, hi := hm.Min, hm.Max
	if lo == 0 && hi == 0 || !finite(lo) || !finite(hi) || hi <= lo {
		var ok bool
		if lo, hi, ok = extentRows(hm.Values); !ok {
			return g
		}
	}
	format := hm.Format
	if format == nil {
		format = Compact
	}
	labelW := 0
	for _, l := range hm.RowLabels {
		labelW = max(labelW, width(l))
	}
	labelW = min(labelW, g.W/4)
	x0 := 0
	if labelW > 0 {
		x0 = labelW + 1
	}
	// The bottom rows: column labels, then the scale and its labels.
	foot := 0
	if g.H >= 5 {
		foot = 2
	}
	colRow := len(hm.ColLabels) > 0 && g.H >= 6
	if colRow {
		foot++
	}
	area := g.H - foot
	half := rows > area && m != ASCII
	if half {
		rows = min(rows, area*2)
	} else {
		rows = min(rows, area)
	}
	// A value is two cells wide when there is room: a cell is about twice
	// as tall as it is wide, so two cells read about square.
	cellW := 1
	if (g.W-x0)/cols >= 2 {
		cellW = 2
	}
	cols = min(cols, (g.W-x0)/cellW)
	at := func(r, c int) float64 {
		if r < len(hm.Values) && c < len(hm.Values[r]) {
			return hm.Values[r][c]
		}
		return math.NaN()
	}
	if half {
		for y := 0; y*2 < rows; y++ {
			if labelW > 0 && 2*y < len(hm.RowLabels) {
				g.Text(0, y, truncate(hm.RowLabels[2*y], labelW, ellipsis(m)), Dim, false)
			}
			for c := 0; c < cols; c++ {
				up, down := at(2*y, c), at(2*y+1, c)
				if 2*y+1 >= rows {
					down = math.NaN()
				}
				for k := 0; k < cellW; k++ {
					x := x0 + c*cellW + k
					switch {
					case finite(up) && finite(down):
						g.setBg(x, y, "▀", heat[hm.level(up, lo, hi)], heat[hm.level(down, lo, hi)])
					case finite(up):
						g.set(x, y, "▀", heat[hm.level(up, lo, hi)])
					case finite(down):
						g.set(x, y, "▄", heat[hm.level(down, lo, hi)])
					}
				}
			}
		}
	} else {
		for y := 0; y < rows; y++ {
			if labelW > 0 && y < len(hm.RowLabels) {
				g.Text(0, y, truncate(hm.RowLabels[y], labelW, ellipsis(m)), Dim, false)
			}
			for c := 0; c < cols; c++ {
				v := at(y, c)
				if !finite(v) {
					// No data: a faint dot, distinct from the lowest value.
					for k := 0; k < cellW; k++ {
						g.set(x0+c*cellW+k, y, block(m, "·", " "), Edge)
					}
					continue
				}
				lv := hm.level(v, lo, hi)
				for k := 0; k < cellW; k++ {
					if m == ASCII {
						g.set(x0+c*cellW+k, y, asciiShades[lv], heat[lv])
					} else {
						g.set(x0+c*cellW+k, y, "█", heat[lv])
					}
				}
			}
		}
	}
	used := rows
	if half {
		used = (rows + 1) / 2
	}
	y := min(area, used)
	if colRow {
		n := len(hm.ColLabels)
		g.Text(x0, y, truncate(hm.ColLabels[0], max(cols*cellW/2-1, 0), ""), Mute, false)
		if n >= cols && cols > 1 {
			// The right label names the column at the right edge.
			g.TextRight(x0+cols*cellW, y, truncate(hm.ColLabels[cols-1], max(cols*cellW/2-1, 0), ""), Mute, false)
		}
		y++
	}
	if foot >= 2 && y+1 < g.H {
		// The scale: three cells of each shade, with the low value, the
		// middle and the high value under it.
		sw := 3
		for i, r := range heat {
			for k := 0; k < sw; k++ {
				if m == ASCII {
					g.set(x0+sw*i+k, y, asciiShades[i], r)
				} else {
					g.set(x0+sw*i+k, y, "█", r)
				}
			}
		}
		end := x0 + sw*len(heat)
		g.Text(x0, y+1, format(lo), Mute, false)
		mid := format((lo + hi) / 2)
		g.Text(x0+(sw*len(heat)-width(mid))/2, y+1, mid, Mute, false)
		g.TextRight(end, y+1, format(hi), Mute, false)
	}
	return g
}

func extentRows(rows [][]float64) (float64, float64, bool) {
	return extent(rows...)
}
