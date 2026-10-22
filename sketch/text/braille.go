package text

// dotGrid is a drawing in braille dots, two dots across and four down in a
// cell, in layers. A cell shows one colour, so where layers meet in a cell
// the last layer added gives the colour. Its dots alone show, unless merge
// is set.
type dotGrid struct {
	w, h   int // in cells
	layers [][]uint8
	roles  []uint8
	// merge shows the dots of every layer in a shared cell, in the colour
	// of the last: a line keeps its shape where another crosses it.
	merge bool
}

func newDotGrid(w, h int) *dotGrid {
	return &dotGrid{w: max(w, 0), h: max(h, 0)}
}

// layer adds a layer drawn in role, over the layers before it, and returns it.
func (d *dotGrid) layer(role uint8) int {
	d.layers = append(d.layers, make([]uint8, d.w*d.h))
	d.roles = append(d.roles, role)
	return len(d.layers) - 1
}

// brailleBits are the bits of the dots of a braille cell, by row and column.
var brailleBits = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// set puts the dot at x, y, in dots, on layer l. A dot outside does nothing.
func (d *dotGrid) set(l, x, y int) {
	if x < 0 || y < 0 || x >= d.w*2 || y >= d.h*4 {
		return
	}
	d.layers[l][(y/4)*d.w+x/2] |= brailleBits[y%4][x%2]
}

// line draws a line of dots from x0, y0 to x1, y1 on layer l. With every
// over 1 it puts one dot in every so many, for a dotted line.
func (d *dotGrid) line(l, x0, y0, x1, y1, every int) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	e := dx + dy
	for n := 0; ; n++ {
		if every <= 1 || n%every == 0 {
			d.set(l, x0, y0)
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x0 += sx
		}
		if e2 <= dx {
			e += dx
			y0 += sy
		}
	}
}

// paint writes the dots into the canvas from x0, y0, in cells that hold
// nothing else.
func (d *dotGrid) paint(c *canvas, x0, y0 int) {
	for y := 0; y < d.h; y++ {
		for x := 0; x < d.w; x++ {
			p := c.at(x0+x, y0+y)
			if p == nil || p.r != 0 || p.mask != 0 || p.wide {
				continue
			}
			var all uint8
			for l := range d.layers {
				all |= d.layers[l][y*d.w+x]
			}
			for l := len(d.layers) - 1; l >= 0; l-- {
				if bits := d.layers[l][y*d.w+x]; bits != 0 {
					if d.merge {
						bits = all
					}
					c.put(x0+x, y0+y, 0x2800+rune(bits), d.roles[l])
					break
				}
			}
		}
	}
}
