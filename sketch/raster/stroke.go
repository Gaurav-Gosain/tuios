package raster

import "math"

type strokeStyle struct {
	width      float64
	cap        string // butt, round, square
	join       string // miter, round, bevel
	miterLimit float64
	dashes     []float64
	dashOffset float64
}

// polygon is a closed outline to fill. Every polygon a stroke makes winds the
// same way, so that where they overlap the coverage adds up and is clamped,
// which fills their union.
type polygon []pt

func area(p polygon) float64 {
	a := 0.0
	for i := range p {
		j := (i + 1) % len(p)
		a += p[i].x*p[j].y - p[j].x*p[i].y
	}
	return a / 2
}

func positive(p polygon) polygon {
	if area(p) < 0 {
		for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
			p[i], p[j] = p[j], p[i]
		}
	}
	return p
}

// circlePoly approximates a circle closely enough at its size.
func circlePoly(c pt, r float64) polygon {
	n := int(math.Ceil(math.Pi / math.Acos(math.Max(-1, 1-0.15/math.Max(r, 0.15)))))
	if n < 8 {
		n = 8
	}
	if n > 96 {
		n = 96
	}
	p := make(polygon, n)
	for i := range p {
		s, co := math.Sincos(2 * math.Pi * float64(i) / float64(n))
		p[i] = pt{c.x + r*co, c.y + r*s}
	}
	return p
}

// dash cuts polylines into the dashes of a dash array.
func dash(lines []polyline, dashes []float64, offset float64) []polyline {
	total := 0.0
	for _, d := range dashes {
		if d < 0 {
			return lines
		}
		total += d
	}
	if total <= 0 {
		return lines
	}
	if len(dashes)%2 == 1 {
		dashes = append(append([]float64{}, dashes...), dashes...)
	}
	var out []polyline
	for _, pl := range lines {
		pts := pl.pts
		if pl.closed && len(pts) > 1 {
			pts = append(append([]pt{}, pts...), pts[0])
		}
		// Where in the dash array the line starts.
		idx := 0
		left := dashes[0]
		pos := math.Mod(offset, total)
		if pos < 0 {
			pos += total
		}
		for pos > 0 {
			if pos >= left {
				pos -= left
				idx = (idx + 1) % len(dashes)
				left = dashes[idx]
			} else {
				left -= pos
				pos = 0
			}
		}
		var cur []pt
		on := idx%2 == 0
		if on {
			cur = []pt{pts[0]}
		}
		for i := 1; i < len(pts); i++ {
			a, b := pts[i-1], pts[i]
			segLen := math.Hypot(b.x-a.x, b.y-a.y)
			done := 0.0
			for segLen-done > left {
				done += left
				t := done / segLen
				q := pt{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t}
				if on {
					cur = append(cur, q)
					out = append(out, polyline{pts: cur})
					cur = nil
				} else {
					cur = []pt{q}
				}
				on = !on
				idx = (idx + 1) % len(dashes)
				left = dashes[idx]
				if len(out) > maxPolygons {
					return out
				}
			}
			left -= segLen - done
			if on {
				cur = append(cur, b)
			}
		}
		if on && len(cur) > 1 {
			out = append(out, polyline{pts: cur})
		}
	}
	return out
}

// maxPolygons bounds the outlines one stroke can make.
const maxPolygons = 100000

// stroke outlines polylines as polygons to fill.
func stroke(lines []polyline, st strokeStyle) []polygon {
	if st.width <= 0 {
		return nil
	}
	if len(st.dashes) > 0 {
		lines = dash(lines, st.dashes, st.dashOffset)
	}
	hw := st.width / 2
	var out []polygon
	for _, pl := range lines {
		pts := dedupe(pl.pts)
		if len(pts) == 1 {
			// A zero-length subpath shows only with round or square caps.
			switch st.cap {
			case "round":
				out = append(out, circlePoly(pts[0], hw))
			case "square":
				c := pts[0]
				out = append(out, polygon{{c.x - hw, c.y - hw}, {c.x + hw, c.y - hw}, {c.x + hw, c.y + hw}, {c.x - hw, c.y + hw}})
			}
			continue
		}
		if pl.closed && len(pts) > 2 {
			if f, l := pts[0], pts[len(pts)-1]; math.Abs(f.x-l.x) < 1e-6 && math.Abs(f.y-l.y) < 1e-6 {
				pts = pts[:len(pts)-1]
			}
		}
		closed := pl.closed && len(pts) > 2
		n := len(pts)
		segs := n - 1
		if closed {
			segs = n
		}
		for i := 0; i < segs; i++ {
			a, b := pts[i], pts[(i+1)%n]
			dx, dy := b.x-a.x, b.y-a.y
			l := math.Hypot(dx, dy)
			ux, uy := dx/l, dy/l
			nx, ny := -uy*hw, ux*hw
			if !closed && st.cap == "square" {
				if i == 0 {
					a = pt{a.x - ux*hw, a.y - uy*hw}
				}
				if i == segs-1 {
					b = pt{b.x + ux*hw, b.y + uy*hw}
				}
			}
			out = append(out, positive(polygon{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}}))
			if len(out) > maxPolygons {
				return out
			}
		}
		// Joins.
		first, last := 1, n-1
		if closed {
			first, last = 0, n
		}
		for i := first; i < last; i++ {
			prev := pts[(i-1+n)%n]
			at := pts[i%n]
			next := pts[(i+1)%n]
			if j := join(prev, at, next, hw, st); j != nil {
				out = append(out, j)
			}
		}
		if !closed && st.cap == "round" {
			out = append(out, circlePoly(pts[0], hw), circlePoly(pts[n-1], hw))
		}
	}
	return out
}

func dedupe(pts []pt) []pt {
	out := make([]pt, 0, len(pts))
	for _, p := range pts {
		if len(out) > 0 {
			q := out[len(out)-1]
			if math.Abs(p.x-q.x) < 1e-6 && math.Abs(p.y-q.y) < 1e-6 {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// join fills the outer corner between two segments that meet at `at`.
func join(prev, at, next pt, hw float64, st strokeStyle) polygon {
	ax, ay := at.x-prev.x, at.y-prev.y
	bx, by := next.x-at.x, next.y-at.y
	la, lb := math.Hypot(ax, ay), math.Hypot(bx, by)
	if la == 0 || lb == 0 {
		return nil
	}
	ax, ay, bx, by = ax/la, ay/la, bx/lb, by/lb
	cross := ax*by - ay*bx
	dot := ax*bx + ay*by
	if math.Abs(cross) < 1e-9 && dot > 0 {
		return nil
	}
	// The outer side is to the left of the turn when it turns right, and the other way.
	side := 1.0
	if cross > 0 {
		side = -1
	}
	p1 := pt{at.x - ay*hw*side, at.y + ax*hw*side}
	p2 := pt{at.x - by*hw*side, at.y + bx*hw*side}
	switch st.join {
	case "round":
		// A slight turn needs only the bevel; the arc would differ by a fraction of a pixel.
		if math.Acos(math.Max(-1, math.Min(1, dot))) < 0.2 {
			return positive(polygon{at, p1, p2})
		}
		return circlePoly(at, hw)
	case "bevel":
		return positive(polygon{at, p1, p2})
	}
	// Miter, or bevel past the miter limit.
	theta := math.Acos(math.Max(-1, math.Min(1, -dot)))
	limit := st.miterLimit
	if limit <= 0 {
		limit = 4
	}
	if s := math.Sin(theta / 2); s == 0 || 1/s > limit {
		return positive(polygon{at, p1, p2})
	}
	// The miter tip lies where the two outer edges meet.
	mx, my := (p1.x+p2.x)/2-at.x, (p1.y+p2.y)/2-at.y
	ml := math.Hypot(mx, my)
	if ml == 0 {
		return nil
	}
	reach := hw / math.Sin(theta/2)
	tip := pt{at.x + mx/ml*reach, at.y + my/ml*reach}
	return positive(polygon{at, p1, tip, p2})
}
