package raster

import (
	"math"
	"strconv"
	"strings"
)

type pt struct{ x, y float64 }

// matrix is an affine transform: x' = a*x + c*y + e, y' = b*x + d*y + f.
type matrix struct{ a, b, c, d, e, f float64 }

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) mul(n matrix) matrix {
	// m then... returns m ∘ n: apply n first, then m.
	return matrix{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

func (m matrix) apply(p pt) pt {
	return pt{m.a*p.x + m.c*p.y + m.e, m.b*p.x + m.d*p.y + m.f}
}

// scale is how much the transform scales lengths, for stroke widths.
func (m matrix) scale() float64 {
	return math.Sqrt(math.Abs(m.a*m.d - m.b*m.c))
}

func translate(x, y float64) matrix { return matrix{1, 0, 0, 1, x, y} }

func rotate(deg float64) matrix {
	r := deg * math.Pi / 180
	s, c := math.Sincos(r)
	return matrix{c, s, -s, c, 0, 0}
}

// parseTransform reads translate, rotate, scale, skewX, skewY and matrix.
func parseTransform(s string) matrix {
	m := identity
	for {
		s = strings.TrimLeft(s, " ,\t\n")
		open := strings.IndexByte(s, '(')
		close := strings.IndexByte(s, ')')
		if open < 0 || close < open {
			return m
		}
		name := strings.TrimSpace(s[:open])
		args := numbers(s[open+1 : close])
		s = s[close+1:]
		arg := func(i int, def float64) float64 {
			if i < len(args) {
				return args[i]
			}
			return def
		}
		var t matrix
		switch name {
		case "translate":
			t = translate(arg(0, 0), arg(1, 0))
		case "rotate":
			t = rotate(arg(0, 0))
			if len(args) >= 3 {
				t = translate(args[1], args[2]).mul(t).mul(translate(-args[1], -args[2]))
			}
		case "scale":
			sx := arg(0, 1)
			t = matrix{sx, 0, 0, arg(1, sx), 0, 0}
		case "skewX":
			t = matrix{1, 0, math.Tan(arg(0, 0) * math.Pi / 180), 1, 0, 0}
		case "skewY":
			t = matrix{1, math.Tan(arg(0, 0) * math.Pi / 180), 0, 1, 0, 0}
		case "matrix":
			if len(args) == 6 {
				t = matrix{args[0], args[1], args[2], args[3], args[4], args[5]}
			} else {
				t = identity
			}
		default:
			t = identity
		}
		m = m.mul(t)
	}
}

// numberScanner reads SVG numbers: signs, decimals, exponents, and numbers
// that run together such as "1.5.5" or "-1-2".
type numberScanner struct {
	s string
	i int
}

func (n *numberScanner) skipSep() {
	for n.i < len(n.s) {
		c := n.s[n.i]
		if c == ' ' || c == ',' || c == '\t' || c == '\n' || c == '\r' {
			n.i++
		} else {
			return
		}
	}
}

func (n *numberScanner) number() (float64, bool) {
	n.skipSep()
	start := n.i
	i := n.i
	if i < len(n.s) && (n.s[i] == '+' || n.s[i] == '-') {
		i++
	}
	digits := false
	for i < len(n.s) && n.s[i] >= '0' && n.s[i] <= '9' {
		i++
		digits = true
	}
	if i < len(n.s) && n.s[i] == '.' {
		i++
		for i < len(n.s) && n.s[i] >= '0' && n.s[i] <= '9' {
			i++
			digits = true
		}
	}
	if !digits {
		return 0, false
	}
	if i < len(n.s) && (n.s[i] == 'e' || n.s[i] == 'E') {
		j := i + 1
		if j < len(n.s) && (n.s[j] == '+' || n.s[j] == '-') {
			j++
		}
		k := j
		for k < len(n.s) && n.s[k] >= '0' && n.s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	v, err := strconv.ParseFloat(n.s[start:i], 64)
	if err != nil {
		return 0, false
	}
	n.i = i
	return v, true
}

// flag reads an arc flag, which may run into what follows it.
func (n *numberScanner) flag() (float64, bool) {
	n.skipSep()
	if n.i < len(n.s) && (n.s[n.i] == '0' || n.s[n.i] == '1') {
		v := float64(n.s[n.i] - '0')
		n.i++
		return v, true
	}
	return 0, false
}

func numbers(s string) []float64 {
	sc := numberScanner{s: s}
	var out []float64
	for {
		v, ok := sc.number()
		if !ok {
			return out
		}
		out = append(out, v)
	}
}

// path is a list of subpaths, each a list of points with curves already
// turned into cubic Bézier segments.
type segKind uint8

const (
	segMove segKind = iota
	segLine
	segCubic
	segClose
)

type seg struct {
	kind segKind
	p    [3]pt
}

type path []seg

func (p *path) moveTo(a pt)        { *p = append(*p, seg{kind: segMove, p: [3]pt{a}}) }
func (p *path) lineTo(a pt)        { *p = append(*p, seg{kind: segLine, p: [3]pt{a}}) }
func (p *path) cubicTo(a, b, c pt) { *p = append(*p, seg{kind: segCubic, p: [3]pt{a, b, c}}) }
func (p *path) close()             { *p = append(*p, seg{kind: segClose}) }
func (p *path) quadTo(from, ctl, to pt) {
	p.cubicTo(pt{from.x + 2.0/3*(ctl.x-from.x), from.y + 2.0/3*(ctl.y-from.y)},
		pt{to.x + 2.0/3*(ctl.x-to.x), to.y + 2.0/3*(ctl.y-to.y)}, to)
}

// maxPathSegments bounds the work one path can ask for.
const maxPathSegments = 200000

// parsePath reads SVG path data. An error in the data ends the path there, as
// browsers do.
func parsePath(d string) path {
	var p path
	sc := numberScanner{s: d}
	var cur, start, lastCtl pt
	var lastCmd byte
	cmd := byte(0)
	for len(p) < maxPathSegments {
		sc.skipSep()
		if sc.i >= len(d) {
			break
		}
		c := d[sc.i]
		if (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') && c != 'e' && c != 'E' {
			cmd = c
			sc.i++
		} else if cmd == 0 {
			break
		}
		rel := cmd >= 'a'
		at := func(x, y float64) pt {
			if rel {
				return pt{cur.x + x, cur.y + y}
			}
			return pt{x, y}
		}
		ok := true
		num := func() float64 {
			v, k := sc.number()
			if !k {
				ok = false
			}
			return v
		}
		switch cmd | 0x20 {
		case 'm':
			x, y := num(), num()
			if !ok {
				return p
			}
			cur = at(x, y)
			start = cur
			p.moveTo(cur)
			// Pairs after a moveto are linetos.
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
			lastCmd = 'm'
			continue
		case 'l':
			x, y := num(), num()
			if !ok {
				return p
			}
			cur = at(x, y)
			p.lineTo(cur)
		case 'h':
			x := num()
			if !ok {
				return p
			}
			if rel {
				cur.x += x
			} else {
				cur.x = x
			}
			p.lineTo(cur)
		case 'v':
			y := num()
			if !ok {
				return p
			}
			if rel {
				cur.y += y
			} else {
				cur.y = y
			}
			p.lineTo(cur)
		case 'c':
			x1, y1, x2, y2, x, y := num(), num(), num(), num(), num(), num()
			if !ok {
				return p
			}
			c1, c2, e := at(x1, y1), at(x2, y2), at(x, y)
			p.cubicTo(c1, c2, e)
			lastCtl, cur = c2, e
		case 's':
			x2, y2, x, y := num(), num(), num(), num()
			if !ok {
				return p
			}
			c1 := cur
			if lastCmd == 'c' || lastCmd == 's' {
				c1 = pt{2*cur.x - lastCtl.x, 2*cur.y - lastCtl.y}
			}
			c2, e := at(x2, y2), at(x, y)
			p.cubicTo(c1, c2, e)
			lastCtl, cur = c2, e
		case 'q':
			x1, y1, x, y := num(), num(), num(), num()
			if !ok {
				return p
			}
			ctl, e := at(x1, y1), at(x, y)
			p.quadTo(cur, ctl, e)
			lastCtl, cur = ctl, e
		case 't':
			x, y := num(), num()
			if !ok {
				return p
			}
			ctl := cur
			if lastCmd == 'q' || lastCmd == 't' {
				ctl = pt{2*cur.x - lastCtl.x, 2*cur.y - lastCtl.y}
			}
			e := at(x, y)
			p.quadTo(cur, ctl, e)
			lastCtl, cur = ctl, e
		case 'a':
			rx, ry, rot := num(), num(), num()
			large, ok1 := sc.flag()
			sweep, ok2 := sc.flag()
			x, y := num(), num()
			if !ok || !ok1 || !ok2 {
				return p
			}
			e := at(x, y)
			arcTo(&p, cur, rx, ry, rot, large != 0, sweep != 0, e)
			cur = e
		case 'z':
			p.close()
			cur = start
			lastCmd = 'z'
			continue
		default:
			return p
		}
		lastCmd = cmd | 0x20
	}
	return p
}

// arcTo adds an elliptical arc as cubic Béziers, by the endpoint to center
// conversion of the SVG specification (appendix B.2.4).
func arcTo(p *path, from pt, rx, ry, rotDeg float64, large, sweep bool, to pt) {
	if from == to {
		return
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		p.lineTo(to)
		return
	}
	phi := rotDeg * math.Pi / 180
	sinPhi, cosPhi := math.Sincos(phi)
	dx2, dy2 := (from.x-to.x)/2, (from.y-to.y)/2
	x1p := cosPhi*dx2 + sinPhi*dy2
	y1p := -sinPhi*dx2 + cosPhi*dy2
	lambda := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry)
	if lambda > 1 {
		s := math.Sqrt(lambda)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	coef := 0.0
	if den != 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1p / ry
	cyp := -coef * ry * x1p / rx
	cx := cosPhi*cxp - sinPhi*cyp + (from.x+to.x)/2
	cy := sinPhi*cxp + cosPhi*cyp + (from.y+to.y)/2
	angle := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	theta1 := angle(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	delta := angle((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	} else if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(delta) / (math.Pi / 2)))
	if n < 1 {
		n = 1
	}
	step := delta / float64(n)
	k := 4.0 / 3 * math.Tan(step/4)
	point := func(t float64) (pt, pt) {
		s, c := math.Sincos(t)
		// The point and the derivative direction on the unit circle, mapped to the ellipse.
		px, py := rx*c, ry*s
		dx, dy := -rx*s, ry*c
		return pt{cosPhi*px - sinPhi*py + cx, sinPhi*px + cosPhi*py + cy},
			pt{cosPhi*dx - sinPhi*dy, sinPhi*dx + cosPhi*dy}
	}
	t := theta1
	a, da := point(t)
	for i := 0; i < n; i++ {
		b, db := point(t + step)
		end := b
		if i == n-1 {
			end = to
		}
		p.cubicTo(pt{a.x + k*da.x, a.y + k*da.y}, pt{b.x - k*db.x, b.y - k*db.y}, end)
		t += step
		a, da = b, db
	}
}

// polyline is one flattened subpath in device space.
type polyline struct {
	pts    []pt
	closed bool
}

// flatten turns a path into polylines in device space, curves split until
// they stay within tol pixels of the true curve.
func flatten(p path, m matrix, tol float64) []polyline {
	var out []polyline
	var cur *polyline
	var last pt
	start := func(a pt) {
		out = append(out, polyline{pts: []pt{a}})
		cur = &out[len(out)-1]
	}
	for _, s := range p {
		switch s.kind {
		case segMove:
			a := m.apply(s.p[0])
			start(a)
			last = a
		case segLine:
			a := m.apply(s.p[0])
			if cur == nil {
				start(last)
			}
			cur.pts = append(cur.pts, a)
			last = a
		case segCubic:
			if cur == nil {
				start(last)
			}
			c1, c2, e := m.apply(s.p[0]), m.apply(s.p[1]), m.apply(s.p[2])
			cur.pts = appendCubic(cur.pts, last, c1, c2, e, tol)
			last = e
		case segClose:
			if cur != nil {
				cur.closed = true
				first := cur.pts[0]
				// A path that goes on after a close starts again where the subpath started.
				out = append(out, polyline{pts: []pt{first}})
				cur = &out[len(out)-1]
				last = first
			}
		}
	}
	// Drop the empty starts left by close and move.
	kept := out[:0]
	for _, pl := range out {
		if len(pl.pts) > 1 || pl.closed {
			kept = append(kept, pl)
		}
	}
	return kept
}

func appendCubic(dst []pt, p0, p1, p2, p3 pt, tol float64) []pt {
	// The number of pieces from the size of the second differences.
	dd := math.Max(math.Hypot(p0.x-2*p1.x+p2.x, p0.y-2*p1.y+p2.y), math.Hypot(p1.x-2*p2.x+p3.x, p1.y-2*p2.y+p3.y))
	n := int(math.Ceil(math.Sqrt(0.75 * dd / tol)))
	if n < 1 {
		n = 1
	}
	if n > 200 {
		n = 200
	}
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
		dst = append(dst, pt{a*p0.x + b*p1.x + c*p2.x + d*p3.x, a*p0.y + b*p1.y + c*p2.y + d*p3.y})
	}
	return dst
}

// Shapes as paths.

// kappa places the handles of a cubic that draws a quarter circle.
const kappa = 0.5522847498307936

func ellipsePath(cx, cy, rx, ry float64) path {
	var p path
	kx, ky := rx*kappa, ry*kappa
	p.moveTo(pt{cx + rx, cy})
	p.cubicTo(pt{cx + rx, cy + ky}, pt{cx + kx, cy + ry}, pt{cx, cy + ry})
	p.cubicTo(pt{cx - kx, cy + ry}, pt{cx - rx, cy + ky}, pt{cx - rx, cy})
	p.cubicTo(pt{cx - rx, cy - ky}, pt{cx - kx, cy - ry}, pt{cx, cy - ry})
	p.cubicTo(pt{cx + kx, cy - ry}, pt{cx + rx, cy - ky}, pt{cx + rx, cy})
	p.close()
	return p
}

func rectPath(x, y, w, h, rx, ry float64) path {
	var p path
	if w <= 0 || h <= 0 {
		return p
	}
	rx = math.Min(math.Max(rx, 0), w/2)
	ry = math.Min(math.Max(ry, 0), h/2)
	if rx == 0 || ry == 0 {
		p.moveTo(pt{x, y})
		p.lineTo(pt{x + w, y})
		p.lineTo(pt{x + w, y + h})
		p.lineTo(pt{x, y + h})
		p.close()
		return p
	}
	kx, ky := rx*kappa, ry*kappa
	p.moveTo(pt{x + rx, y})
	p.lineTo(pt{x + w - rx, y})
	p.cubicTo(pt{x + w - rx + kx, y}, pt{x + w, y + ry - ky}, pt{x + w, y + ry})
	p.lineTo(pt{x + w, y + h - ry})
	p.cubicTo(pt{x + w, y + h - ry + ky}, pt{x + w - rx + kx, y + h}, pt{x + w - rx, y + h})
	p.lineTo(pt{x + rx, y + h})
	p.cubicTo(pt{x + rx - kx, y + h}, pt{x, y + h - ry + ky}, pt{x, y + h - ry})
	p.lineTo(pt{x, y + ry})
	p.cubicTo(pt{x, y + ry - ky}, pt{x + rx - kx, y}, pt{x + rx, y})
	p.close()
	return p
}

func polyPath(points []float64, closed bool) path {
	var p path
	for i := 0; i+1 < len(points); i += 2 {
		a := pt{points[i], points[i+1]}
		if i == 0 {
			p.moveTo(a)
		} else {
			p.lineTo(a)
		}
	}
	if closed && len(p) > 0 {
		p.close()
	}
	return p
}
