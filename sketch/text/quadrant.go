package text

import (
	"math"

	"tuios.dev/sketch"
	"tuios.dev/sketch/internal/types"
)

// A quadrant chart is a box split into four, each quadrant named in its outer
// corner. The y axis names its low and high ends left of the bottom and the top
// half, and the x axis under the left and the right half. Points are dots with
// their labels beside them: right of the dot, else left of it, else on the
// row above or below. When the columns leave too little room left of the box,
// the names of the y axis go above and below it.

func drawQuadrant(l *sketch.Layout, opt Options) (*canvas, error) {
	q := l.Quadrant
	if q == nil {
		return nil, ErrUnsupported
	}
	if c, err := quadrantDraw(q, opt, true); err != ErrTooWide {
		return c, err
	}
	return quadrantDraw(q, opt, false)
}

func quadrantDraw(q *types.GeoQuadrant, opt Options, side bool) (*canvas, error) {
	// The names of the y axis, in a margin left of the box when side is set.
	margin := 0
	var yHigh, yLow []string
	if side && (q.YHigh != "" || q.YLow != "") {
		margin = min(max(width(q.YHigh), width(q.YLow)), 14) + 1
		yHigh = wrap(q.YHigh, margin-1)
		yLow = wrap(q.YLow, margin-1)
	}
	boxW := 64
	if opt.Cols > 0 {
		boxW = min(opt.Cols-margin, 90)
	}
	if boxW < 24 || (side && margin > 0 && boxW < 40) {
		return nil, ErrTooWide
	}
	// Two halves of equal width either side of the middle line.
	half := (boxW - 3) / 2
	boxW = 2*half + 3
	if !side {
		yHigh = wrap(q.YHigh, boxW)
		yLow = wrap(q.YLow, boxW)
	}
	halfH := min(max(half/4, 5), 10)
	wd := margin + boxW
	var title []string
	for _, line := range q.Title {
		title = append(title, wrap(line, wd)...)
	}
	var xLow, xHigh []string
	if q.XHigh != "" {
		xLow = wrap(q.XLow, half)
		xHigh = wrap(q.XHigh, half)
	} else {
		xLow = wrap(q.XLow, boxW)
	}

	y := 0
	if len(title) > 0 {
		y = len(title) + 1
	}
	if !side && len(yHigh) > 0 {
		y += len(yHigh)
	}
	top := y
	mid := top + 1 + halfH
	bottom := mid + 1 + halfH
	height := bottom + 1 + max(len(xLow), len(xHigh))
	if !side {
		height += len(yLow)
	}
	c, err := newCanvas(wd, height)
	if err != nil {
		return nil, err
	}
	for i, line := range title {
		c.text((wd-width(line))/2, i, line, roleTitle)
	}
	x0, x1 := margin, margin+boxW-1
	midX := x0 + 1 + half
	c.box(x0, top, boxW, bottom-top+1, nil, roleBox, roleText)
	c.hline(x0, x1, mid, light, roleBox)
	c.vline(midX, top, bottom, light, roleBox)

	// taken marks the cells that a label must not cover.
	taken := make([]bool, wd*height)
	take := func(x, y, w int) {
		for i := max(x, 0); i < min(x+w, wd); i++ {
			if y >= 0 && y < height {
				taken[y*wd+i] = true
			}
		}
	}
	free := func(x, y, w int, cross bool) bool {
		if x <= x0 || x+w > x1 || y <= top || y >= bottom || y == mid {
			return false
		}
		for i := x; i < x+w; i++ {
			if taken[y*wd+i] || (!cross && i == midX) {
				return false
			}
		}
		return true
	}

	// The places of the points.
	type spot struct{ x, y int }
	dots := make([]spot, len(q.Points))
	for i, p := range q.Points {
		px := x0 + 1 + int(math.Round(p.X*float64(boxW-3)))
		py := top + 1 + int(math.Round((1-p.Y)*float64(bottom-top-2)))
		dots[i] = spot{px, py}
	}
	dotIn := func(x0, x1, y int) bool {
		for _, d := range dots {
			if d.y == y && d.x >= x0 && d.x <= x1 {
				return true
			}
		}
		return false
	}

	// The names of the quadrants in their outer corners: 1 top right, 2 top
	// left, 3 bottom left, 4 bottom right. A name moves in a row from the edge
	// while a point is in its way.
	for k, name := range q.Quadrants {
		s := fit(name, half-2)
		if s == "" {
			continue
		}
		x := x0 + 2
		if k == 0 || k == 3 {
			x = x1 - 1 - width(s)
		}
		ty, step := top+1, 1
		if k >= 2 {
			ty, step = bottom-1, -1
		}
		for range halfH - 1 {
			if !dotIn(x-1, x+width(s), ty) {
				break
			}
			ty += step
		}
		c.text(x, ty, s, roleTitle)
		take(x-1, ty, width(s)+2)
	}

	// The axis names.
	if side {
		for i, line := range yHigh {
			c.text(margin-1-width(line), top+1+(halfH-len(yHigh))/2+i, line, roleHead)
		}
		for i, line := range yLow {
			c.text(margin-1-width(line), mid+1+(halfH-len(yLow))/2+i, line, roleHead)
		}
	} else {
		for i, line := range yHigh {
			c.text(x0, top-len(yHigh)+i, line, roleHead)
		}
	}
	for i, line := range xLow {
		x := x0 + 1 + (half-width(line))/2
		if len(xHigh) == 0 {
			x = x0
		}
		c.text(x, bottom+1+i, line, roleHead)
	}
	for i, line := range xHigh {
		c.text(midX+1+(half-width(line))/2, bottom+1+i, line, roleHead)
	}
	if !side {
		for i, line := range yLow {
			c.text(x0, bottom+1+max(len(xLow), len(xHigh))+i, line, roleHead)
		}
	}

	// The dots, then their labels, which keep clear of every dot.
	for _, d := range dots {
		c.put(d.x, d.y, '●', seriesRole(0))
		take(d.x, d.y, 1)
	}
	for i, p := range q.Points {
		s := fit(p.Label, half-2)
		w := width(s)
		if w == 0 {
			continue
		}
		d := dots[i]
		tries := []spot{{d.x + 2, d.y}, {d.x - 1 - w, d.y}, {d.x - w/2, d.y - 1}, {d.x - w/2, d.y + 1},
			{d.x + 2, d.y - 1}, {d.x - 1 - w, d.y - 1}, {d.x + 2, d.y + 1}, {d.x - 1 - w, d.y + 1},
			{d.x - w/2, d.y - 2}, {d.x - w/2, d.y + 2}}
		placed := false
		for _, cross := range []bool{false, true} {
			for _, t := range tries {
				if free(t.x-1, t.y, w+2, cross) || (free(t.x, t.y, w, cross) && t.x == x0+1) {
					c.text(t.x, t.y, s, roleText)
					take(t.x, t.y, w)
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
		if !placed {
			// No free place: beside the dot, over what is there.
			x := d.x + 2
			if x+w > x1 {
				x = max(d.x-1-w, x0+1)
			}
			c.text(x, d.y, s, roleText)
			take(x, d.y, w)
		}
	}
	// The dots again, over any label that had no free place.
	for _, d := range dots {
		c.put(d.x, d.y, '●', seriesRole(0))
	}
	return c, nil
}
