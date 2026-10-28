// Ported from pele src/diagrams/c4/route.ts at 8aff183be57021b533f319a29341a529be934b08.

package c4

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/svg"
)

// Box is a laid out shape or boundary: top-left corner, size, and the shared shape it is drawn as.
type Box struct {
	x, y, w, h float64
	form       string
}

type Route struct {
	d string
	// Where the markers go: the tip, and the way it points.
	sx, sy, sdx, sdy float64
	ex, ey, edx, edy float64
	// The stretch the label may sit on.
	x0, y0, x1, y1 float64
	// The side of that stretch the label is pushed to, as a unit vector; zero centers it on the stretch.
	nx, ny float64
	// When set, the label stays over the line and moves that way by at most this much.
	lean float64
	// The widest the label should be.
	room float64
	// The corners of the line, as x, y pairs.
	points []float64
}

const LOOP = 28
const LANE = 20
const LANE_STEP = 7
const CORNER = 8

// LEAN: a wide label on an upright lane leans this far out from it, to the middle of the gap between columns.
const LEAN = 30

// NARROW and WIDE: label widths that fit the gap between two columns, and where there is room to spare.
const NARROW = 88

// LABEL_PAD is the space a label's background adds to the width of its text.
const LABEL_PAD = 8
const WIDE = 160

// HEAD: the shared person shape is a head of radius 13 centered 14 below the top, over a body that starts 26 below it.
const HEAD = 26

// cylRy and queueRx are the cap radii drawShape uses for the two cylinders.
func cylRy(w float64) float64   { return math.Min(12, math.Max(6, w/14)) }
func queueRx(h float64) float64 { return math.Min(12, h/4) }

// hitRect is how far along the segment from (px, py) by (dx, dy) it first meets the rectangle; Infinity when it misses.
func hitRect(x0, y0, x1, y1, px, py, dx, dy float64) float64 {
	enter := 0.0
	leave := 1.0
	if dx == 0 {
		if px < x0 || px > x1 {
			return math.Inf(1)
		}
	} else {
		a := (x0 - px) / dx
		b := (x1 - px) / dx
		enter = math.Max(enter, math.Min(a, b))
		leave = math.Min(leave, math.Max(a, b))
	}
	if dy == 0 {
		if py < y0 || py > y1 {
			return math.Inf(1)
		}
	} else {
		a := (y0 - py) / dy
		b := (y1 - py) / dy
		enter = math.Max(enter, math.Min(a, b))
		leave = math.Min(leave, math.Max(a, b))
	}
	if enter <= leave {
		return enter
	}
	return math.Inf(1)
}

func hitEllipse(cx, cy, rx, ry, px, py, dx, dy float64) float64 {
	ux := dx / rx
	uy := dy / ry
	vx := (px - cx) / rx
	vy := (py - cy) / ry
	a := float64(ux*ux) + float64(uy*uy)
	b := 2 * (float64(ux*vx) + float64(uy*vy))
	c := float64(vx*vx) + float64(vy*vy) - 1
	disc := float64(b*b) - float64(4*a*c)
	if a == 0 || disc < 0 {
		return math.Inf(1)
	}
	t := (-b - math.Sqrt(disc)) / (2 * a)
	if t >= 0 && t <= 1 {
		return t
	}
	if c <= 0 {
		return 0
	}
	return math.Inf(1)
}

// hit is where a segment from outside first meets the outline of a shape, as a fraction of the segment.
func hit(box *Box, px, py, qx, qy float64) float64 {
	x, y, w, h := box.x, box.y, box.w, box.h
	dx := qx - px
	dy := qy - py
	switch box.form {
	case "person":
		return math.Min(hitRect(x, y+HEAD, x+w, y+h, px, py, dx, dy), hitEllipse(x+w/2, y+14, 13, 13, px, py, dx, dy))
	case "cyl":
		ry := cylRy(w)
		return math.Min(math.Min(
			hitRect(x, y+ry, x+w, y+h-ry, px, py, dx, dy),
			hitEllipse(x+w/2, y+ry, w/2, ry, px, py, dx, dy)),
			hitEllipse(x+w/2, y+h-ry, w/2, ry, px, py, dx, dy))
	case "h-cyl":
		rx := queueRx(h)
		return math.Min(math.Min(
			hitRect(x+rx, y, x+w-rx, y+h, px, py, dx, dy),
			hitEllipse(x+rx, y+h/2, rx, h/2, px, py, dx, dy)),
			hitEllipse(x+w-rx, y+h/2, rx, h/2, px, py, dx, dy))
	}
	return hitRect(x, y, x+w, y+h, px, py, dx, dy)
}

func crosses(points []float64, x0, y0, x1, y1 float64) bool {
	for i := 2; i < len(points); i += 2 {
		px := points[i-2]
		py := points[i-1]
		if hitRect(x0, y0, x1, y1, px, py, points[i]-px, points[i+1]-py) <= 1 {
			return true
		}
	}
	return false
}

func contains(outer, inner *Box) bool {
	return outer.x <= inner.x &&
		outer.y <= inner.y &&
		outer.x+outer.w >= inner.x+inner.w &&
		outer.y+outer.h >= inner.y+inner.h
}

// connect is the straight line from a to b: x, y of its start, then of its end.
func connect(a, b *Box, shift float64) (float64, float64, float64, float64) {
	ax := a.x + a.w/2
	ay := a.y + a.h/2
	bx := b.x + b.w/2
	by := b.y + b.h/2
	down := contains(a, b)
	if down || contains(b, a) {
		// One holds the other: a short line from the nearest side of the outer box.
		outer, inner := b, a
		if down {
			outer, inner = a, b
		}
		ix := inner.x + inner.w/2
		iy := inner.y + inner.h/2
		left := inner.x - outer.x
		right := outer.x + outer.w - inner.x - inner.w
		top := inner.y - outer.y
		bottom := outer.y + outer.h - inner.y - inner.h
		least := math.Min(math.Min(left, right), math.Min(top, bottom))
		ox := ix
		if least == left {
			ox = outer.x
		} else if least == right {
			ox = outer.x + outer.w
		}
		oy := outer.y
		if least == left || least == right {
			oy = iy
		} else if least == bottom {
			oy = outer.y + outer.h
		}
		t := math.Min(hit(inner, ox, oy, ix, iy), 1)
		mx := ox + float64((ix-ox)*t)
		my := oy + float64((iy-oy)*t)
		if down {
			return ox, oy, mx, my
		}
		return mx, my, ox, oy
	}

	px := ax
	py := ay
	qx := bx
	qy := by
	// Shapes that share a column or a row are joined by an upright or level line through the shared span.
	x0 := math.Max(a.x, b.x)
	x1 := math.Min(a.x+a.w, b.x+b.w)
	y0 := math.Max(a.y, b.y)
	y1 := math.Min(a.y+a.h, b.y+b.h)
	if x0 < x1 {
		px = (x0 + x1) / 2
		qx = px
	} else if y0 < y1 {
		py = (y0 + y1) / 2
		qy = py
	}
	length := js.Hypot(qx-px, qy-py)
	if length == 0 || math.IsNaN(length) {
		length = 1
	}
	nx := float64((-(qy - py) / length) * shift)
	ny := float64(((qx - px) / length) * shift)
	ta := hit(a, qx+nx, qy+ny, px+nx, py+ny)
	tb := hit(b, px+nx, py+ny, qx+nx, qy+ny)
	if ta <= 1 && tb <= 1 {
		px += nx
		py += ny
		qx += nx
		qy += ny
	} else {
		px = ax
		py = ay
		qx = bx
		qy = by
		ta = math.Min(hit(a, qx, qy, px, py), 1)
		tb = math.Min(hit(b, px, py, qx, qy), 1)
	}
	return qx + float64((px-qx)*ta), qy + float64((py-qy)*ta), px + float64((qx-px)*tb), py + float64((qy-py)*tb)
}

// NEAR: parallel stretches closer than this read as one line, and a line FRAME_NEAR close to a
// boundary's frame reads as part of it.
const NEAR = 6
const FRAME_NEAR = 3
const PORT_STEP = 12

// Lanes holds what the routes drawn so far take up, so that the next keeps apart from them: upright stretches
// as x, top, bottom; level ones as y, left, right; and where lines leave or reach each side of a shape.
type Lanes struct {
	upright []float64
	level   []float64
	frames  [2][]float64
	taken   map[*Box][]float64
	// squeezed counts routes that found no lane of their own: a line left straight behind a shape, or a lane that
	// runs along another.
	squeezed int
}

func newLanes() *Lanes {
	return &Lanes{taken: map[*Box][]float64{}}
}

func near(list []float64, gap, at, lo, hi float64) bool {
	for i := 0; i < len(list); i += 3 {
		if math.Abs(list[i]-at) < gap && list[i+1] < hi && list[i+2] > lo {
			return true
		}
	}
	return false
}

func (l *Lanes) free(upright bool, at, a0, a1 float64) bool {
	lo := math.Min(a0, a1)
	hi := math.Max(a0, a1)
	list, frames := l.level, l.frames[1]
	if upright {
		list, frames = l.upright, l.frames[0]
	}
	return !near(list, NEAR, at, lo, hi) && !near(frames, FRAME_NEAR, at, lo, hi)
}

func (l *Lanes) crossings(points []float64) int {
	count := 0
	for i := 2; i < len(points); i += 2 {
		upright := points[i] == points[i-2]
		var at, lo, hi float64
		list := l.upright
		if upright {
			at = points[i]
			lo = math.Min(points[i-1], points[i+1])
			hi = math.Max(points[i-1], points[i+1])
			list = l.level
		} else {
			at = points[i+1]
			lo = math.Min(points[i-2], points[i])
			hi = math.Max(points[i-2], points[i])
		}
		for j := 0; j < len(list); j += 3 {
			if list[j] > lo && list[j] < hi && list[j+1] < at && list[j+2] > at {
				count++
			}
		}
	}
	return count
}

func (l *Lanes) frame(box *Box) {
	l.frames[0] = append(l.frames[0], box.x, box.y, box.y+box.h, box.x+box.w, box.y, box.y+box.h)
	l.frames[1] = append(l.frames[1], box.y, box.x, box.x+box.w, box.y+box.h, box.x, box.x+box.w)
}

func (l *Lanes) take(points []float64) {
	for i := 2; i < len(points); i += 2 {
		if points[i] == points[i-2] {
			l.upright = append(l.upright, points[i], math.Min(points[i-1], points[i+1]), math.Max(points[i-1], points[i+1]))
		} else if points[i+1] == points[i-1] {
			l.level = append(l.level, points[i+1], math.Min(points[i-2], points[i]), math.Max(points[i-2], points[i]))
		}
	}
}

// ports gives where on one side of a shape (0 left, 1 right, 2 top, 3 bottom) a line can leave or arrive,
// between lo and hi: the places clear of the lines already there, nearest `want` first.
func (l *Lanes) ports(box *Box, side int, want, lo, hi float64) []float64 {
	used := l.taken[box]
	var out []float64
	for k := 0; k < 9 && len(out) < 4; k++ {
		sign := -1.0
		if k&1 != 0 {
			sign = 1
		}
		at := want + float64(sign*math.Ceil(float64(k)/2)*PORT_STEP)
		if at < lo || at > hi {
			continue
		}
		clear := true
		for i := 0; i < len(used); i += 2 {
			if used[i] == float64(side) && math.Abs(used[i+1]-at) < PORT_STEP {
				clear = false
			}
		}
		if clear {
			out = append(out, at)
		}
	}
	if len(out) > 0 {
		return out
	}
	return []float64{want}
}

func (l *Lanes) claim(box *Box, side int, at float64) {
	l.taken[box] = append(l.taken[box], float64(side), at)
}

// truthy is a number as a condition: neither zero nor NaN.
func truthy(x float64) bool { return x != 0 && !math.IsNaN(x) }

func path(points []float64, startTrim, endTrim float64) string {
	n := len(points)
	ux := js.Sign(points[2] - points[0])
	uy := js.Sign(points[3] - points[1])
	var d strings.Builder
	svg.WriteStrings(&d, "M", svg.Num(points[0]+float64(ux*startTrim)), ",", svg.Num(points[1]+float64(uy*startTrim)))
	for i := 2; i < n-2; i += 2 {
		x := points[i]
		y := points[i+1]
		ix := js.Sign(x - points[i-2])
		iy := js.Sign(y - points[i-1])
		ox := js.Sign(points[i+2] - x)
		oy := js.Sign(points[i+3] - y)
		r := math.Min(math.Min(
			CORNER,
			math.Abs(x-points[i-2]+y-points[i-1])/2),
			math.Abs(points[i+2]-x+points[i+3]-y)/2)
		if truthy(ix) {
			svg.WriteStrings(&d, "H", svg.Num(x-float64(ix*r)))
		} else {
			svg.WriteStrings(&d, "V", svg.Num(y-float64(iy*r)))
		}
		svg.WriteStrings(&d, "Q", svg.Num(x), ",", svg.Num(y), " ", svg.Num(x+float64(ox*r)), ",", svg.Num(y+float64(oy*r)))
	}
	lx := js.Sign(points[n-2] - points[n-4])
	ly := js.Sign(points[n-1] - points[n-3])
	if truthy(lx) {
		svg.WriteStrings(&d, "H", svg.Num(points[n-2]-float64(lx*endTrim)))
	} else {
		svg.WriteStrings(&d, "V", svg.Num(points[n-1]-float64(ly*endTrim)))
	}
	return d.String()
}

func clearOf(points []float64, others []*Box, a, b *Box) bool {
	for _, other := range others {
		if other != a && other != b && crosses(points, other.x, other.y, other.x+other.w, other.y+other.h) {
			return false
		}
	}
	return true
}

// bent is the route for a line with corners through `points`, from a's outline to b's. `along` is the
// stretch its label sits on; nx, lean and room place the label.
func bent(points []float64, along int, startTrim, endTrim, nx, lean, room float64) Route {
	n := len(points)
	return Route{
		d:      path(points, startTrim, endTrim),
		sx:     points[0],
		sy:     points[1],
		sdx:    -js.Sign(points[2] - points[0]),
		sdy:    -js.Sign(points[3] - points[1]),
		ex:     points[n-2],
		ey:     points[n-1],
		edx:    js.Sign(points[n-2] - points[n-4]),
		edy:    js.Sign(points[n-1] - points[n-3]),
		x0:     points[along],
		y0:     points[along+1],
		x1:     points[along+2],
		y1:     points[along+3],
		nx:     nx,
		lean:   lean,
		room:   room,
		points: points,
	}
}

func across(lo, hi float64) []float64 {
	var out []float64
	mid := (lo + hi) / 2
	step := math.Max(LANE_STEP, (hi-lo)/4)
	for k := 0; k < 9; k++ {
		sign := -1.0
		if k&1 != 0 {
			sign = 1
		}
		at := mid + float64(sign*math.Ceil(float64(k)/2)*step)
		if at >= lo+NEAR && at <= hi-NEAR {
			out = append(out, at)
		}
	}
	return out
}

type elbowBest struct {
	points  []float64
	cost    float64
	upright bool
	sides   [2]int
}

func elbow(a, b *Box, others []*Box, lanes *Lanes, startTrim, endTrim float64) (Route, bool) {
	ax := a.x + a.w/2
	ay := a.y + a.h/2
	bx := b.x + b.w/2
	by := b.y + b.h/2
	var best *elbowBest
	// consider is handed a scratch array; it copies the points only when they are the best yet.
	var scratch [8]float64
	consider := func(points []float64, upright bool, sides [2]int) {
		if !clearOf(points, others, a, b) {
			return
		}
		for i := 2; i < len(points); i += 2 {
			up := points[i] == points[i-2]
			var ok bool
			if up {
				ok = lanes.free(up, points[i], points[i-1], points[i+1])
			} else {
				ok = lanes.free(up, points[i+1], points[i-2], points[i])
			}
			if !ok {
				return
			}
		}
		cost := float64(lanes.crossings(points)) * 1e6
		for i := 2; i < len(points); i += 2 {
			cost += math.Abs(points[i]-points[i-2]) + math.Abs(points[i+1]-points[i-1])
		}
		if best == nil || cost < best.cost {
			best = &elbowBest{append([]float64(nil), points...), cost, upright, sides}
		}
	}
	if a.x+a.w <= b.x || b.x+b.w <= a.x {
		right := bx > ax
		down := js.Sign(by - ay)
		sides := [2]int{0, 1}
		if right {
			sides = [2]int{1, 0}
		}
		var lanesAcross []float64
		if right {
			lanesAcross = across(a.x+a.w, b.x)
		} else {
			lanesAcross = across(b.x+b.w, a.x)
		}
	searchX:
		for _, y0 := range lanes.ports(a, sides[0], ay+(down*a.h)/4, a.y+8, a.y+a.h-8) {
			for _, y1 := range lanes.ports(b, sides[1], by-(down*b.h)/4, b.y+8, b.y+b.h-8) {
				for _, lane := range lanesAcross {
					x0 := lane + float64((ax-lane)*math.Min(hit(a, lane, y0, ax, y0), 1))
					x1 := lane + float64((bx-lane)*math.Min(hit(b, lane, y1, bx, y1), 1))
					before := best
					scratch = [8]float64{x0, y0, lane, y0, lane, y1, x1, y1}
					consider(scratch[:], true, sides)
					if best != before {
						break searchX
					}
				}
			}
		}
	}
	if a.y+a.h <= b.y || b.y+b.h <= a.y {
		below := by > ay
		sideways := js.Sign(bx - ax)
		sides := [2]int{2, 3}
		if below {
			sides = [2]int{3, 2}
		}
		var lanesAcross []float64
		if below {
			lanesAcross = across(a.y+a.h, b.y)
		} else {
			lanesAcross = across(b.y+b.h, a.y)
		}
	searchY:
		for _, x0 := range lanes.ports(a, sides[0], ax+(sideways*a.w)/4, a.x+8, a.x+a.w-8) {
			for _, x1 := range lanes.ports(b, sides[1], bx-(sideways*b.w)/4, b.x+8, b.x+b.w-8) {
				for _, lane := range lanesAcross {
					y0 := lane + float64((ay-lane)*math.Min(hit(a, x0, lane, x0, ay), 1))
					y1 := lane + float64((by-lane)*math.Min(hit(b, x1, lane, x1, by), 1))
					before := best
					scratch = [8]float64{x0, y0, x0, lane, x1, lane, x1, y1}
					consider(scratch[:], false, sides)
					if best != before {
						break searchY
					}
				}
			}
		}
	}
	if best == nil {
		return Route{}, false
	}
	points, upright, sides := best.points, best.upright, best.sides
	lanes.take(points)
	if upright {
		lanes.claim(a, sides[0], points[1])
		lanes.claim(b, sides[1], points[7])
	} else {
		lanes.claim(a, sides[0], points[0])
		lanes.claim(b, sides[1], points[6])
	}
	// The label sits on the middle stretch, across the gap.
	room := float64(WIDE)
	if upright {
		room = NARROW
	}
	return bent(points, 2, startTrim, endTrim, 0, 0, room), true
}

type detour struct {
	points []float64
	side   int
	p0, p1 float64
	step   int
}

// route routes a relation from a to b. `shift` moves the line sideways when another runs the opposite way.
// `others` are the shapes to steer clear of; nil skips that check. `lanes` holds what the
// routes before it take up, so that this one keeps apart from them.
func route(a, b *Box, shift float64, others []*Box, lanes *Lanes, startTrim, endTrim float64) Route {
	if a == b {
		spread := math.Min(a.h/2-4, 10)
		length := js.Hypot(LOOP, spread)
		x := a.x + a.w
		cy := a.y + a.h/2
		ux := LOOP / length
		uy := spread / length
		var d strings.Builder
		svg.WriteStrings(&d, "M", svg.Num(x+float64(ux*startTrim)), ",", svg.Num(cy-spread-float64(uy*startTrim)), "C", svg.Num(x+LOOP), ",", svg.Num(cy-float64(spread*2)), " ",
			svg.Num(x+LOOP), ",", svg.Num(cy+float64(spread*2)), " ", svg.Num(x+float64(ux*endTrim)), ",", svg.Num(cy+spread+float64(uy*endTrim)))
		return Route{
			d:    d.String(),
			sx:   x,
			sy:   cy - spread,
			sdx:  -ux,
			sdy:  uy,
			ex:   x,
			ey:   cy + spread,
			edx:  -ux,
			edy:  -uy,
			x0:   x + LOOP + 4,
			y0:   cy,
			x1:   x + LOOP + 4,
			y1:   cy,
			nx:   1,
			room: 64,
		}
	}

	sx, sy, ex, ey := connect(a, b, shift)
	upright := sx == ex
	blocked := false
	if others != nil && !contains(a, b) && !contains(b, a) {
		for _, other := range others {
			if other != a && other != b && hitRect(other.x, other.y, other.x+other.w, other.y+other.h, sx, sy, ex-sx, ey-sy) <= 1 {
				blocked = true
			}
		}
	}
	if others != nil && blocked && !upright && sy != ey {
		if around, ok := elbow(a, b, others, lanes, startTrim, endTrim); ok {
			return around
		}
		lanes.squeezed++
	}
	if others != nil && blocked && (upright || sy == ey) {
		laneOn := func(forward bool) float64 {
			lane := math.Inf(1)
			if forward {
				lane = math.Inf(-1)
			}
			widen := func(box *Box) {
				edge, extent := box.y, box.h
				if upright {
					edge, extent = box.x, box.w
				}
				if forward {
					lane = math.Max(lane, edge+extent)
				} else {
					lane = math.Min(lane, edge)
				}
			}
			widen(a)
			widen(b)
			for _, other := range others {
				if other != a && other != b && hitRect(other.x, other.y, other.x+other.w, other.y+other.h, sx, sy, ex-sx, ey-sy) <= 1 {
					widen(other)
				}
			}
			return lane
		}
		var lo, hi float64
		if upright {
			lo, hi = math.Min(a.y, b.y), math.Max(a.y+a.h, b.y+b.h)
		} else {
			lo, hi = math.Min(a.x, b.x), math.Max(a.x+a.w, b.x+b.w)
		}
		outside := func(forward bool, lane float64) bool {
			for _, other := range others {
				if other == a || other == b {
					continue
				}
				var isNear bool
				var start, end float64
				if upright {
					isNear = other.y < hi && other.y+other.h > lo
					start, end = other.x, other.x+other.w
				} else {
					isNear = other.x < hi && other.x+other.w > lo
					start, end = other.y, other.y+other.h
				}
				if isNear && ((forward && end > lane) || (!forward && start < lane)) {
					return false
				}
			}
			return true
		}
		ax := a.x + a.w/2
		ay := a.y + a.h/2
		bx := b.x + b.w/2
		by := b.y + b.h/2
		towards := js.Sign(bx - ax)
		if upright {
			towards = js.Sign(by - ay)
		}
		// The route on one side: each end leaves its shape a quarter of the way from the middle towards
		// the other, clear of the straight lines that meet the middle of that side and of other routes
		// there. The lane runs a little further out for each route already along that gap, and never
		// behind a shape. `step` is how many lanes out it went; 5 when none was free.
		along := func(forward bool) detour {
			lane := laneOn(forward)
			sign := -1.0
			if forward {
				sign = 1
			}
			var side int
			switch {
			case upright && forward:
				side = 1
			case upright:
				side = 0
			case forward:
				side = 3
			default:
				side = 2
			}
			var p0, p1 float64
			if upright {
				p0 = lanes.ports(a, side, ay+(towards*a.h)/4, a.y+8, a.y+a.h-8)[0]
				p1 = lanes.ports(b, side, by-(towards*b.h)/4, b.y+8, b.y+b.h-8)[0]
			} else {
				p0 = lanes.ports(a, side, ax+(towards*a.w)/4, a.x+8, a.x+a.w-8)[0]
				p1 = lanes.ports(b, side, bx-(towards*b.w)/4, b.x+8, b.x+b.w-8)[0]
			}
			pointsAt := func(at float64) []float64 {
				if upright {
					x0 := at + float64((ax-at)*math.Min(hit(a, at, p0, ax, p0), 1))
					x1 := at + float64((bx-at)*math.Min(hit(b, at, p1, bx, p1), 1))
					return []float64{x0, p0, at, p0, at, p1, x1, p1}
				}
				y0 := at + float64((ay-at)*math.Min(hit(a, p0, at, p0, ay), 1))
				y1 := at + float64((by-at)*math.Min(hit(b, p1, at, p1, by), 1))
				return []float64{p0, y0, p0, at, p1, at, p1, y1}
			}
			points := pointsAt(lane + float64(sign*LANE))
			for k := 0; k <= 4; k++ {
				at := lane + float64(sign*(LANE+float64(float64(k)*LANE_STEP)))
				tried := pointsAt(at)
				if !clearOf(tried, others, a, b) {
					continue
				}
				points = tried
				if lanes.free(upright, at, p0, p1) {
					return detour{points, side, p0, p1, k}
				}
			}
			return detour{points, side, p0, p1, 5}
		}
		// A detour takes the outside of the drawing when only one side is outside, leaving the gaps
		// between columns and rows to the relations that cross them. Otherwise it takes the side where
		// it crosses fewer lines, then the one where it runs nearer its shapes, so that detours spread
		// over both sides.
		natural := ex < sx
		if upright {
			natural = ey > sy
		}
		out := outside(natural, laneOn(natural))
		back := outside(!natural, laneOn(!natural))
		first := along(natural)
		var other *detour
		if out == back {
			o := along(!natural)
			other = &o
		}
		cost := func(way detour) int { return lanes.crossings(way.points)*8 + way.step }
		forward := natural
		if back && !out {
			forward = !natural
		} else if other != nil && cost(*other) < cost(first) {
			forward = !natural
		}
		var way detour
		switch {
		case forward == natural:
			way = first
		case other != nil:
			way = *other
		default:
			way = along(!natural)
		}
		sign := -1.0
		if forward {
			sign = 1
		}
		if way.step > 4 {
			lanes.squeezed++
		}
		lanes.take(way.points)
		lanes.claim(a, way.side, way.p0)
		lanes.claim(b, way.side, way.p1)
		if upright {
			return bent(way.points, 2, startTrim, endTrim, sign, LEAN, NARROW)
		}
		return bent(way.points, 2, startTrim, endTrim, 0, 0, WIDE)
	}

	length := js.Hypot(ex-sx, ey-sy)
	if length == 0 || math.IsNaN(length) {
		length = 1
	}
	dx := (ex - sx) / length
	dy := (ey - sy) / length
	if upright || sy == ey {
		lanes.take([]float64{sx, sy, ex, ey})
	}
	var d strings.Builder
	svg.WriteStrings(&d, "M", svg.Num(sx+float64(dx*startTrim)), ",", svg.Num(sy+float64(dy*startTrim)), "L", svg.Num(ex-float64(dx*endTrim)), ",", svg.Num(ey-float64(dy*endTrim)))
	r := Route{
		d:      d.String(),
		sx:     sx,
		sy:     sy,
		sdx:    -dx,
		sdy:    -dy,
		ex:     ex,
		ey:     ey,
		edx:    dx,
		edy:    dy,
		x0:     sx,
		y0:     sy,
		x1:     ex,
		y1:     ey,
		room:   WIDE,
		points: []float64{sx, sy, ex, ey},
	}
	if truthy(shift) {
		r.nx = -dy
		r.ny = dx
	}
	if math.Abs(dx) > math.Abs(dy) {
		r.room = NARROW
	}
	return r
}
