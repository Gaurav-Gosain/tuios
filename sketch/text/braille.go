package text

type dotGrid struct {
	w, h   int
	layers [][]uint8
	roles  []uint8
	merge  bool
}

func newDotGrid(w, h int) *dotGrid {
	return &dotGrid{w: max(w, 0), h: max(h, 0)}
}

func (d *dotGrid) layer(role uint8) int {
	d.layers = append(d.layers, make([]uint8, d.w*d.h))
	d.roles = append(d.roles, role)
	return len(d.layers) - 1
}

var brailleBits = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

func (d *dotGrid) set(l, x, y int) {
	if x < 0 || y < 0 || x >= d.w*2 || y >= d.h*4 {
		return
	}
	d.layers[l][(y/4)*d.w+x/2] |= brailleBits[y%4][x%2]
}

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
