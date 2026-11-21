package text

var roundShapes = map[string]bool{"rounded": true, "ellipse": true, "delay": true, "cloud": true, "lin-cyl": true, "cyl": true, "h-cyl": true}

var pillShapes = map[string]bool{"stadium": true, "circle": true, "dbl-circ": true}

func pointRows(shape string, n int) int {
	h := n + 2
	if shape == "diam" {
		h = n + 4
	}
	return h + 1 - h%2
}

func shapeRoom(shape string, n, h int) (dw, dh int) {
	switch {
	case pointShapes[shape]:
		dh = pointRows(shape, n) - (n + 2)
		if shape == "diam" {
			return 2 * (n / 2), dh
		}
		return 0, dh
	case shape == "dbl-circ", shape == "fr-rect":
		return 2, 0
	case shape == "lean-r", shape == "lean-l":
		return h - 1, 0
	case shape == "trap-b", shape == "trap-t":
		return 2 * (h - 2), 0
	case shape == "odd":
		return (h + 1) / 2, 0
	case cylShapes[shape]:
		return 0, 1
	}
	return 0, 0
}

func sides(c *canvas, b rect, left, right []int) {
	h := b.y1 - b.y0 + 1
	for r := 0; r < h; r++ {
		y := b.y0 + r
		a, z := left[r], left[r+1]
		switch {
		case z < a:
			c.put(b.x0+z, y, '╱', roleBox)
		case z > a:
			c.put(b.x0+a, y, '╲', roleBox)
		case r > 0 && r < h-1:
			c.put(b.x0+a, y, '▏', roleBox)
		}
		a, z = right[r], right[r+1]
		switch {
		case z < a:
			c.put(b.x1-z, y, '╲', roleBox)
		case z > a:
			c.put(b.x1-a, y, '╱', roleBox)
		case r > 0 && r < h-1:
			c.put(b.x1-a, y, '▕', roleBox)
		}
	}
	from := func(side []int, k, next int) int {
		if side[next] > side[k] {
			return side[k] + 1
		}
		return side[k]
	}
	for x := b.x0 + from(left, 0, 1); x <= b.x1-from(right, 0, 1); x++ {
		c.put(x, b.y0, '▔', roleBox)
	}
	for x := b.x0 + from(left, h, h-1); x <= b.x1-from(right, h, h-1); x++ {
		c.put(x, b.y1, '▁', roleBox)
	}
}

func outline(c *canvas, b rect, shape string) {
	h := b.y1 - b.y0 + 1
	hline := func(x0, x1, y int, r rune) {
		for x := x0; x <= x1; x++ {
			c.put(x, y, r, roleBox)
		}
	}
	vline := func(x, y0, y1 int, r rune) {
		for y := y0; y <= y1; y++ {
			c.put(x, y, r, roleBox)
		}
	}
	box := func(tl, tr, bl, br rune) {
		hline(b.x0+1, b.x1-1, b.y0, '─')
		hline(b.x0+1, b.x1-1, b.y1, '─')
		vline(b.x0, b.y0+1, b.y1-1, '│')
		vline(b.x1, b.y0+1, b.y1-1, '│')
		c.put(b.x0, b.y0, tl, roleBox)
		c.put(b.x1, b.y0, tr, roleBox)
		c.put(b.x0, b.y1, bl, roleBox)
		c.put(b.x1, b.y1, br, roleBox)
	}
	edges := func(f func(k int) int) []int {
		out := make([]int, h+1)
		for k := range out {
			out[k] = f(k)
		}
		return out
	}
	fromEdge := func(k int) int { return min(k, h-k) }
	switch {
	case pillShapes[shape]:
		box('╭', '╮', '╰', '╯')
		open, close := "(", ")"
		if shape == "dbl-circ" {
			open, close = "((", "))"
		}
		for y := b.y0 + 1; y < b.y1; y++ {
			c.text(b.x0, y, open, roleBox)
			c.text(b.x1-len(close)+1, y, close, roleBox)
		}
	case pointShapes[shape] && h >= 3:
		steps := 1
		if shape == "diam" {
			steps = (h - 1) / 2
		}
		side := edges(func(k int) int { return max(steps-fromEdge(k), 0) })
		sides(c, b, side, side)
	case shape == "fr-rect":
		box('┌', '┐', '└', '┘')
		vline(b.x0+1, b.y0+1, b.y1-1, '│')
		vline(b.x1-1, b.y0+1, b.y1-1, '│')
		c.put(b.x0+1, b.y0, '┬', roleBox)
		c.put(b.x1-1, b.y0, '┬', roleBox)
		c.put(b.x0+1, b.y1, '┴', roleBox)
		c.put(b.x1-1, b.y1, '┴', roleBox)
	case cylShapes[shape] && h >= 4:
		box('╭', '╮', '╰', '╯')
		hline(b.x0+1, b.x1-1, b.y0+1, '─')
		c.put(b.x0, b.y0+1, '├', roleBox)
		c.put(b.x1, b.y0+1, '┤', roleBox)
	case slantShapes[shape]:
		in := edges(func(k int) int { return h - k })
		out := edges(func(k int) int { return k })
		switch shape {
		case "lean-r":
			sides(c, b, in, out)
		case "lean-l":
			sides(c, b, out, in)
		case "trap-b":
			sides(c, b, in, in)
		default:
			sides(c, b, out, out)
		}
	case shape == "odd" && h >= 3:
		notch := edges(func(k int) int { return min(fromEdge(k), (h-1)/2) })
		sides(c, b, notch, make([]int, h+1))
	case roundShapes[shape]:
		box('╭', '╮', '╰', '╯')
	default:
		box('┌', '┐', '└', '┘')
	}
}
