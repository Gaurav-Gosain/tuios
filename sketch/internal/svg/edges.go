// Ported from pele src/svg/edges.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package svg

import (
	"math"
	"slices"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/layout"
	"tuios.dev/sketch/internal/text"
)

const ARROW = 8

type EdgePath struct {
	D   string
	Sx  float64
	Sy  float64
	Sdx float64
	Sdy float64
	Ex  float64
	Ey  float64
	Edx float64
	Edy float64
}

// Marker types are Mermaid's: arrow_point, arrow_circle, arrow_cross; anything else draws nothing.

// The most a corner is rounded by in a stepped route.
const edgeCorner = 12

// How far the handles of a wide turn reach along its two legs. A little more than a circle's,
// which makes the turn fuller.
const HANDLE = 0.62

// LEAD is how far an edge runs straight before a marker at its end, where it has the room.
const LEAD = 6

// SideReach is how far from each side of each node the edges that meet it start to turn: as far as the
// largest marker there needs, for every edge on the side, so that none turns sooner than its
// neighbour and crosses it. `Add` takes a node, the side of it, and the marker of an end there.
type SideReach struct{ reach map[int]float64 }

func NewSideReach() *SideReach { return &SideReach{reach: map[int]float64{}} }

func (s *SideReach) Add(node, side int, typ string) {
	trim := MarkerTrim(typ)
	if side >= 0 && trim > 0 && trim+LEAD > s.reach[node*4+side] {
		s.reach[node*4+side] = trim + LEAD
	}
}

func (s *SideReach) Of(node, side int) float64 {
	if side < 0 {
		return 0
	}
	return s.reach[node*4+side]
}

// DefaultReach is how far from its node an edge starts to turn by default: far enough to clear its own marker.
func DefaultReach(trim float64) float64 {
	if trim > 0 {
		return trim + LEAD
	}
	return 0
}

// RoutePath builds the path for a route of x, y, axis triples, trimming both ends to leave room for markers.
// Between two points that are not in line, a route is one S that leaves and arrives along the
// flow, or a step in a stepped route. Where the route itself turns a corner, a stepped route
// rounds it a little, and any other but a linear one turns as widely as it can: the turn takes
// the whole of a leg that ends at a node and half of a leg it shares with the next turn.
// `startReach` and `endReach` say how far from its nodes the edge starts to turn, where the
// other edges on the same side of a node need it to; by default (DefaultReach), far enough to clear its own marker.
func RoutePath(route []float64, curve string, startTrim, endTrim, startReach, endReach float64) EdgePath {
	linear := curve == "linear"
	stepped := curve == "step" || curve == "stepBefore" || curve == "stepAfter"
	var xs, ys, axes []float64
	near := func(ax, ay, bx, by float64) bool { return math.Abs(ax-bx) < 0.01 && math.Abs(ay-by) < 0.01 }
	for i := 0; i < len(route); i += 3 {
		x := route[i]
		y := route[i+1]
		axis := route[i+2]
		last := len(xs) - 1
		// A stepped route turns at right angles where a smooth one would curve.
		if stepped && last >= 0 && math.Abs(x-xs[last]) >= 0.01 && math.Abs(y-ys[last]) >= 0.01 {
			px := xs[last]
			py := ys[last]
			my := (py + y) / 2
			mx := (px + x) / 2
			if curve == "stepBefore" {
				my, mx = py, px
			} else if curve == "stepAfter" {
				my, mx = y, x
			}
			turns := [4]float64{mx, py, mx, y}
			if axis == 0 {
				turns = [4]float64{px, my, x, my}
			}
			for k := 0; k < 4; k += 2 {
				if near(turns[k], turns[k+1], px, py) || near(turns[k], turns[k+1], x, y) {
					continue
				}
				xs = append(xs, turns[k])
				ys = append(ys, turns[k+1])
				axes = append(axes, axis)
			}
		}
		xs = append(xs, x)
		ys = append(ys, y)
		axes = append(axes, axis)
	}
	last := len(xs) - 1
	aligned := func(i int) bool { return math.Abs(xs[i]-xs[i-1]) < 0.01 || math.Abs(ys[i]-ys[i-1]) < 0.01 }
	straight := func(i int) bool { return linear || aligned(i) }
	// The way the route heads from point i to point j: along the flow where an S joins them.
	unit := func(i, j int, axis float64) (float64, float64) {
		if !straight(max(i, j)) {
			if axis == 0 {
				return 0, js.Or(js.Sign(ys[j]-ys[i]), 1)
			}
			return js.Or(js.Sign(xs[j]-xs[i]), 1), 0
		}
		dx := xs[j] - xs[i]
		dy := ys[j] - ys[i]
		length := js.Or(js.Hypot(dx, dy), 1)
		return dx / length, dy / length
	}
	sdx, sdy := unit(0, 1, axes[1])
	edx, edy := unit(last-1, last, axes[last])
	sx := xs[0]
	sy := ys[0]
	ex := xs[last]
	ey := ys[last]
	xs[0] += sdx * startTrim
	ys[0] += sdy * startTrim
	xs[last] -= edx * endTrim
	ys[last] -= edy * endTrim
	length := func(i int) float64 { return math.Abs(xs[i]-xs[i-1]) + math.Abs(ys[i]-ys[i-1]) }
	// Whether the route turns a corner at point i, from running one way to running the other.
	turns := func(i int) bool {
		return !linear && i > 0 && i < last && aligned(i) && aligned(i+1) && length(i) >= 0.01 && length(i+1) >= 0.01 &&
			(math.Abs(xs[i]-xs[i-1]) < 0.01) != (math.Abs(xs[i+1]-xs[i]) < 0.01)
	}
	// An edge that curves into a marker runs straight for a little way first, where it has the
	// room, so that the marker does not sit on the bend.
	along := func(i int, ux, uy float64) float64 {
		return math.Abs(xs[i]-xs[i-1])*math.Abs(ux) + math.Abs(ys[i]-ys[i-1])*math.Abs(uy)
	}
	// Of the way from the end of a run to where it turns, the part that makes up for a larger
	// marker beside this one is kept where it can be, and the rest is at most a quarter of what
	// is left, so that ends on one side of a node turn equally far from it.
	lead := func(reach, trim, run float64) float64 {
		room := math.Min(math.Max(0, reach-LEAD-trim), run/4)
		return room + math.Min(reach-trim-room, (run-room)/4)
	}
	arrives := 0.0
	if endReach > endTrim && (!straight(last) || turns(last-1)) {
		arrives = lead(endReach, endTrim, along(last, edx, edy))
	}
	leaves := 0.0
	if startReach > startTrim && (!straight(1) || turns(1)) {
		leaves = lead(startReach, startTrim, along(1, sdx, sdy))
	}
	if arrives >= 1 {
		xs = slices.Insert(xs, last, xs[last]-edx*arrives)
		ys = slices.Insert(ys, last, ys[last]-edy*arrives)
		axes = slices.Insert(axes, last, axes[last])
		last++
	}
	if leaves >= 1 {
		xs = slices.Insert(xs, 1, xs[0]+sdx*leaves)
		ys = slices.Insert(ys, 1, ys[0]+sdy*leaves)
		axes = slices.Insert(axes, 1, axes[1])
		last++
	}

	d := "M" + Num(xs[0]) + "," + Num(ys[0])
	atX := xs[0]
	atY := ys[0]
	lineTo := func(x, y float64) {
		if near(x, y, atX, atY) {
			return
		}
		d += "L" + Num(x) + "," + Num(y)
		atX = x
		atY = y
	}
	for i := 1; i <= last; i++ {
		x := xs[i]
		y := ys[i]
		if !straight(i) {
			down := axes[i] == 0
			var al, ac float64
			if down {
				al, ac = y-atY, x-atX
			} else {
				al, ac = x-atX, y-atY
			}
			k := math.Abs(al) * layout.Ease(ac, al)
			ux, uy := js.Sign(al)*k, 0.0
			if down {
				ux, uy = 0, js.Sign(al)*k
			}
			d += "C" + Num(atX+ux) + "," + Num(atY+uy) + " " + Num(x-ux) + "," + Num(y-uy) + " " + Num(x) + "," + Num(y)
			atX = x
			atY = y
			continue
		}
		if !turns(i) {
			// A point part of the way along one straight run adds nothing to the path.
			if i < last && aligned(i) && aligned(i+1) && (x-xs[i-1])*(xs[i+1]-x)+(y-ys[i-1])*(ys[i+1]-y) > 0 {
				continue
			}
			lineTo(x, y)
			continue
		}
		// How much of the leg before the turn and of the leg after it the turn takes: all of one
		// that ends at a node, short of the straight run into a marker, and half of one it shares.
		before := length(i)
		if turns(i - 1) {
			before = length(i) / 2
		}
		after := length(i + 1)
		if turns(i + 1) {
			after = length(i+1) / 2
		}
		if stepped {
			before = math.Min(edgeCorner, math.Min(before, after))
			after = before
		}
		ux, uy := unit(i-1, i, axes[i])
		vx, vy := unit(i, i+1, axes[i+1])
		lineTo(x-ux*before, y-uy*before)
		atX = x + vx*after
		atY = y + vy*after
		if stepped {
			d += "Q" + Num(x) + "," + Num(y) + " " + Num(atX) + "," + Num(atY)
		} else {
			d += "C" + Num(x-ux*before*(1-HANDLE)) + "," + Num(y-uy*before*(1-HANDLE)) + " " + Num(x+vx*after*(1-HANDLE)) + "," + Num(y+vy*after*(1-HANDLE)) + " " + Num(atX) + "," + Num(atY)
		}
	}
	return EdgePath{D: d, Sx: sx, Sy: sy, Sdx: -sdx, Sdy: -sdy, Ex: ex, Ey: ey, Edx: edx, Edy: edy}
}

func MarkerTrim(typ string) float64 {
	switch typ {
	case "arrow_point":
		return ARROW - 1
	case "arrow_circle":
		return ARROW
	case "arrow_cross":
		return 4
	}
	return 0
}

// Marker draws a marker whose tip is at (x, y), pointing along (dx, dy).
func Marker(typ string, x, y, dx, dy float64, color string) string {
	nx := -dy
	ny := dx
	switch typ {
	case "arrow_point":
		bx := x - dx*ARROW
		by := y - dy*ARROW
		half := ARROW * 0.42
		return `<path class="pele-marker" d="M` + Num(x) + `,` + Num(y) + `L` + Num(bx+nx*half) + `,` + Num(by+ny*half) + `L` +
			Num(bx-nx*half) + `,` + Num(by-ny*half) + `Z" fill="` + color + `" stroke="none"/>`
	case "arrow_circle":
		return `<circle class="pele-marker" cx="` + Num(x-dx*4) + `" cy="` + Num(y-dy*4) + `" r="3.5" fill="` + color + `" stroke="none"/>`
	case "arrow_cross":
		cx := x - dx*4
		cy := y - dy*4
		a := 3.5
		ux := (dx + nx) * a
		uy := (dy + ny) * a
		vx := (dx - nx) * a
		vy := (dy - ny) * a
		return `<path class="pele-marker" d="M` + Num(cx-ux) + `,` + Num(cy-uy) + `L` + Num(cx+ux) + `,` + Num(cy+uy) + `M` +
			Num(cx-vx) + `,` + Num(cy-vy) + `L` + Num(cx+vx) + `,` + Num(cy+vy) + `"/>`
	}
	return ""
}

// EdgeLabelSvg is an edge label on a patch of the background, so the line does not run through the text.
func EdgeLabelSvg(id string, label text.Label, x, y float64, attrs string, icons IconResolver) string {
	w := label.Width + 8
	h := label.Height
	return `<g class="pele-edge-label" data-id="` + EscText(id) + `">` +
		`<rect x="` + Num(x-w/2) + `" y="` + Num(y-h/2) + `" width="` + Num(w) + `" height="` + Num(h) + `" rx="3" fill="var(--_bg)"/>` +
		LabelSvg(label, x, y, attrs, icons) +
		`</g>`
}

// LoopLabel receives where LoopPath puts the edge's label.
type LoopLabel struct {
	LabelW, LabelH, LabelX, LabelY float64
}

// LoopPath is a self-loop beside a node of drawn size w by h: out of the side that lies across the flow and
// back in. `k` counts the loops already on the node, so each one reaches a little further.
// Also places the edge's label beyond the loop.
func LoopPath(cx, cy, w, h float64, k int, sideways bool, base, trim float64, e *LoopLabel) EdgePath {
	half := w / 2
	if !sideways {
		half = h / 2
	}
	spread := math.Min(half-4, 8+float64(k)*6)
	reach := base + float64(k)*8
	length := js.Or(js.Hypot(reach, spread), 1)
	// Local frame: `out` points away from the node, `along` runs along its side.
	at := func(out, along float64) string {
		if sideways {
			return Num(cx+along) + "," + Num(cy+h/2+out)
		}
		return Num(cx+w/2+out) + "," + Num(cy+along)
	}
	tx := (reach / length) * trim
	ty := (spread / length) * trim
	if sideways {
		e.LabelX = cx
		e.LabelY = cy + h/2 + reach + 4 + e.LabelH/2
	} else {
		e.LabelX = cx + w/2 + reach + 4 + e.LabelW/2
		e.LabelY = cy
	}
	p := EdgePath{D: "M" + at(0, -spread) + "C" + at(reach, -spread*2) + " " + at(reach, spread*2) + " " + at(tx, spread+ty)}
	if sideways {
		p.Sx, p.Sy, p.Sdx, p.Sdy = cx-spread, cy+h/2, 0, -1
		p.Ex, p.Ey, p.Edx, p.Edy = cx+spread, cy+h/2, -spread/length, -reach/length
	} else {
		p.Sx, p.Sy, p.Sdx, p.Sdy = cx+w/2, cy-spread, -1, 0
		p.Ex, p.Ey, p.Edx, p.Edy = cx+w/2, cy+spread, -reach/length, -spread/length
	}
	return p
}
