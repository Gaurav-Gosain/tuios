// Ported from pele src/svg/shapes.ts at 8aff183be57021b533f319a29341a529be934b08.

package svg

import (
	"math"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/layout"
)

type ShapeSize struct {
	W float64
	H float64
	// Vertical shift of the label from the shape's center.
	Dy float64
}

const (
	PAD_X = 18
	PAD_Y = 12
)

type sizer func(tw, th float64) ShapeSize
type drawer func(w, h float64, a string, line string) string

// Distance from the bounding box to the outline on a side: 0 top, 1 right, 2 bottom, 3 left.
// `t` is how far along the side from its middle, to the right or down.
type insetFn func(w, h float64, side int, t float64) float64

// How much of a side edges may spread along, centered on its middle: the top and bottom, or the
// left and right when `sideways`. A shape without one keeps its edges at the middle.
type spanFn func(w, h float64, sideways bool) float64

type shapeDef struct {
	size    sizer
	draw    drawer
	inset   insetFn
	span    spanFn
	noLabel bool
	// Ignores style fills.
	bare bool
}

// Kept clear at each end of a straight side, for the corner.
const shapeCorner = 12

func flat(w, h float64, sideways bool) float64 {
	if sideways {
		return h - 2*shapeCorner
	}
	return w - 2*shapeCorner
}

// Straight along the top and bottom except for `cut(h)` at each end, and not straight at the sides.
func flatAcross(cut func(h float64) float64) spanFn {
	return func(w, h float64, sideways bool) float64 {
		if sideways {
			return 0
		}
		return w - float64(2*cut(h)) - 2*shapeCorner
	}
}

// How far an ellipse's outline has fallen away from its bounding box, `t` from the middle of a side `half` long.
func bulge(t, half, depth float64) float64 {
	k := math.Min(1, math.Abs(t)/half)
	return depth * (1 - math.Sqrt(1-float64(k*k)))
}

func roundInset(w, h float64, side int, t float64) float64 {
	if side&1 != 0 {
		return bulge(t, h/2, w/2)
	}
	return bulge(t, w/2, h/2)
}

func roundSpan(w, h float64, sideways bool) float64 {
	if sideways {
		return h * 0.6
	}
	return w * 0.6
}

func capInset(w, _ float64, side int, t float64) float64 {
	if side&1 != 0 {
		return 0
	}
	return bulge(t, w/2, math.Min(12, math.Max(6, w/14)))
}

func fixed(w, h float64) sizer {
	return func(float64, float64) ShapeSize { return ShapeSize{w, h, 0} }
}

func triSize(tw, th float64, flip bool) ShapeSize {
	w := math.Max(float64(1.6*tw)+40, 80)
	h := (th + 10) / (1 - (tw+8)/w)
	dy := h/2 - 10 - th/2
	if flip {
		dy = -dy
	}
	return ShapeSize{w, h, dy}
}

func path(d, a string) string {
	return `<path d="` + d + `"` + a + `/>`
}

func leanInset(_, h float64, side int, _ float64) float64 {
	if side&1 != 0 {
		return h / 8
	}
	return 0
}

func box(tw, th float64) ShapeSize {
	return ShapeSize{math.Max(tw+2*PAD_X, 2*PAD_X+12), th + 2*PAD_Y, 0}
}

func poly(points []float64, a string) string {
	var d strings.Builder
	for i := 0; i < len(points); i += 2 {
		if i != 0 {
			d.WriteByte(' ')
		}
		d.WriteString(Num(points[i]) + "," + Num(points[i+1]))
	}
	return `<polygon points="` + d.String() + `"` + a + `/>`
}

func rect(w, h float64, a string, rx string) string {
	return `<rect x="` + Num(-w/2) + `" y="` + Num(-h/2) + `" width="` + Num(w) + `" height="` + Num(h) + `"` + rx + a + `/>`
}

func sideOdd(side int) bool { return side&1 != 0 }

var shapes = map[string]*shapeDef{
	"rect": {size: box, span: flat, draw: func(w, h float64, a, _ string) string { return rect(w, h, Rounded(a), "") }},
	"rounded": {
		size: box,
		span: flat,
		draw: func(w, h float64, a, _ string) string { return rect(w, h, a, ` rx="`+Num(math.Min(12, h/2))+`"`) },
	},
	"stadium": {
		inset: func(_, h float64, side int, t float64) float64 {
			if sideOdd(side) {
				return bulge(t, h/2, h/2)
			}
			return 0
		},
		span: func(w, h float64, sideways bool) float64 {
			if sideways {
				return h * 0.6
			}
			return w - h
		},
		size: func(tw, th float64) ShapeSize {
			h := th + 2*PAD_Y
			return ShapeSize{tw + float64(h*0.5) + 2*PAD_X - 8, h, 0}
		},
		draw: func(w, h float64, a, _ string) string { return rect(w, h, a, ` rx="`+Num(h/2)+`"`) },
	},
	"fr-rect": {
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 16, th + 2*PAD_Y, 0} },
		draw: func(w, h float64, a, line string) string {
			x := w/2 - 8
			return rect(w, h, Rounded(a), "") +
				`<path d="M` + Num(-x) + `,` + Num(-h/2) + `V` + Num(h/2) + `M` + Num(x) + `,` + Num(-h/2) + `V` + Num(h/2) + `" fill="none"` + line + `/>`
		},
	},
	"cyl": {
		inset: capInset,
		span: func(w, h float64, sideways bool) float64 {
			if sideways {
				return h - 2*shapeCorner - 12
			}
			return w * 0.6
		},
		size: func(tw, th float64) ShapeSize {
			w := math.Max(tw+2*PAD_X, 56)
			ry := math.Min(12, math.Max(6, w/14))
			return ShapeSize{w, th + 2*PAD_Y + float64(2*ry), ry / 2}
		},
		draw: func(w, h float64, a, line string) string {
			rx := w / 2
			ry := math.Min(12, math.Max(6, w/14))
			top := -h/2 + ry
			bottom := h/2 - ry
			return `<path d="M` + Num(-rx) + `,` + Num(top) + `A` + Num(rx) + `,` + Num(ry) + ` 0 0 1 ` + Num(rx) + `,` + Num(top) + `V` + Num(bottom) + `A` +
				Num(rx) + `,` + Num(ry) + ` 0 0 1 ` + Num(-rx) + `,` + Num(bottom) + `Z"` + a + `/>` +
				`<path d="M` + Num(-rx) + `,` + Num(top) + `A` + Num(rx) + `,` + Num(ry) + ` 0 0 0 ` + Num(rx) + `,` + Num(top) + `" fill="none"` + line + `/>`
		},
	},
	"circle": {
		inset: roundInset,
		span:  roundSpan,
		size: func(tw, th float64) ShapeSize {
			d := math.Max(math.Sqrt(float64(tw*tw)+float64(th*th))+16, 44)
			return ShapeSize{d, d, 0}
		},
		draw: func(w, _ float64, a, _ string) string { return `<circle r="` + Num(w/2) + `"` + a + `/>` },
	},
	"dbl-circ": {
		inset: roundInset,
		span:  roundSpan,
		size: func(tw, th float64) ShapeSize {
			d := math.Max(math.Sqrt(float64(tw*tw)+float64(th*th))+26, 54)
			return ShapeSize{d, d, 0}
		},
		draw: func(w, _ float64, a, line string) string {
			return `<circle r="` + Num(w/2) + `"` + a + `/><circle r="` + Num(w/2-5) + `" fill="none"` + line + `/>`
		},
	},
	"ellipse": {
		inset: roundInset,
		span:  roundSpan,
		size: func(tw, th float64) ShapeSize {
			return ShapeSize{float64(tw*1.3) + 2*PAD_X, float64(th*1.2) + 2*PAD_Y, 0}
		},
		draw: func(w, h float64, a, _ string) string {
			return `<ellipse rx="` + Num(w/2) + `" ry="` + Num(h/2) + `"` + a + `/>`
		},
	},
	"diam": {
		inset: func(w, h float64, side int, t float64) float64 {
			if sideOdd(side) {
				return (math.Abs(t) * w) / h
			}
			return (math.Abs(t) * h) / w
		},
		span: func(w, h float64, sideways bool) float64 {
			if sideways {
				return h * 0.6
			}
			return w * 0.6
		},
		size: func(tw, th float64) ShapeSize {
			a := tw + 16
			b := th + 8
			return ShapeSize{a + float64(1.6*b), a/1.6 + b, 0}
		},
		draw: func(w, h float64, a, _ string) string {
			return poly([]float64{0, -h / 2, w / 2, 0, 0, h / 2, -w / 2, 0}, a)
		},
	},
	"hex": {
		inset: func(_, _ float64, side int, t float64) float64 {
			if sideOdd(side) {
				return math.Abs(t) / 2
			}
			return 0
		},
		span: func(w, h float64, sideways bool) float64 {
			if sideways {
				return h * 0.5
			}
			return w - h/2 - shapeCorner
		},
		size: func(tw, th float64) ShapeSize {
			h := th + 2*PAD_Y
			return ShapeSize{tw + 2*PAD_X + h/2, h, 0}
		},
		draw: func(w, h float64, a, _ string) string {
			m := h / 4
			return poly([]float64{-w/2 + m, -h / 2, w/2 - m, -h / 2, w / 2, 0, w/2 - m, h / 2, -w/2 + m, h / 2, -w / 2, 0}, a)
		},
	},
	"odd": {
		inset: func(_, h float64, side int, _ float64) float64 {
			if side == 3 {
				return h / 4
			}
			return 0
		},
		span: flatAcross(func(float64) float64 { return 0 }),
		size: func(tw, th float64) ShapeSize {
			h := th + 2*PAD_Y
			return ShapeSize{tw + 2*PAD_X + h/4, h, 0}
		},
		draw: func(w, h float64, a, _ string) string {
			notch := h / 4
			return poly([]float64{-w / 2, -h / 2, w / 2, -h / 2, w / 2, h / 2, -w / 2, h / 2, -w/2 + notch, 0}, a)
		},
	},
	"lean-r": {
		inset: leanInset,
		span:  flatAcross(func(h float64) float64 { return h / 4 }),
		size:  leanSize,
		draw: func(w, h float64, a, _ string) string {
			s := h / 4
			return poly([]float64{-w/2 + s, -h / 2, w / 2, -h / 2, w/2 - s, h / 2, -w / 2, h / 2}, a)
		},
	},
	"lean-l": {
		inset: leanInset,
		span:  flatAcross(func(h float64) float64 { return h / 4 }),
		size:  leanSize,
		draw: func(w, h float64, a, _ string) string {
			s := h / 4
			return poly([]float64{-w / 2, -h / 2, w/2 - s, -h / 2, w / 2, h / 2, -w/2 + s, h / 2}, a)
		},
	},
	"trap-b": {
		inset: leanInset,
		span:  flatAcross(func(h float64) float64 { return h / 4 }),
		size:  leanSize,
		draw: func(w, h float64, a, _ string) string {
			s := h / 4
			return poly([]float64{-w/2 + s, -h / 2, w/2 - s, -h / 2, w / 2, h / 2, -w / 2, h / 2}, a)
		},
	},
	"trap-t": {
		inset: leanInset,
		span:  flatAcross(func(h float64) float64 { return h / 4 }),
		size:  leanSize,
		draw: func(w, h float64, a, _ string) string {
			s := h / 4
			return poly([]float64{-w / 2, -h / 2, w / 2, -h / 2, w/2 - s, h / 2, -w/2 + s, h / 2}, a)
		},
	},
	"text": {
		bare: true,
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 8, th + 8, 0} },
		draw: func(w, h float64, _, _ string) string { return rect(w, h, ` fill="none" stroke="none"`, "") },
	},
	"sm-circ": {
		noLabel: true,
		inset:   roundInset,
		span:    roundSpan,
		size:    fixed(14, 14),
		draw:    func(w, _ float64, a, _ string) string { return `<circle r="` + Num(w/2) + `"` + a + `/>` },
	},
	"f-circ": {
		noLabel: true,
		inset:   roundInset,
		span:    roundSpan,
		size:    fixed(14, 14),
		draw: func(w, _ float64, _, line string) string {
			return `<circle r="` + Num(w/2) + `" fill="var(--_l)"` + line + `/>`
		},
	},
	"fr-circ": {
		noLabel: true,
		inset:   roundInset,
		span:    roundSpan,
		size:    fixed(18, 18),
		draw: func(w, _ float64, a, line string) string {
			return `<circle r="` + Num(w/2) + `"` + a + `/><circle r="` + Num(w/2-4) + `" fill="var(--_l)"` + line + `/>`
		},
	},
	"fork": {
		noLabel: true,
		span: func(w, h float64, sideways bool) float64 {
			if sideways {
				return h - 8
			}
			return w - 8
		},
		size: fixed(72, 8),
		draw: func(w, h float64, _, line string) string { return rect(w, h, ` fill="var(--_l)"`+line, ` rx="2"`) },
	},
	"notch-rect": {
		size: box,
		span: flat,
		draw: func(w, h float64, a, _ string) string {
			c := 10.0
			return poly([]float64{-w/2 + c, -h / 2, w / 2, -h / 2, w / 2, h / 2, -w / 2, h / 2, -w / 2, -h/2 + c}, a)
		},
	},
	"lin-rect": {
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 8, th + 2*PAD_Y, 0} },
		draw: func(w, h float64, a, line string) string {
			return rect(w, h, Rounded(a), "") +
				`<path d="M` + Num(-w/2+8) + `,` + Num(-h/2) + `V` + Num(h/2) + `" fill="none"` + line + `/>`
		},
	},
	"div-rect": {
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X, th + 2*PAD_Y + 8, 4} },
		draw: func(w, h float64, a, line string) string {
			return rect(w, h, Rounded(a), "") +
				`<path d="M` + Num(-w/2) + `,` + Num(-h/2+8) + `H` + Num(w/2) + `" fill="none"` + line + `/>`
		},
	},
	"win-pane": {
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 8, th + 2*PAD_Y + 8, 4} },
		draw: func(w, h float64, a, line string) string {
			return rect(w, h, Rounded(a), "") +
				`<path d="M` + Num(-w/2+8) + `,` + Num(-h/2) + `V` + Num(h/2) + `M` + Num(-w/2) + `,` + Num(-h/2+8) + `H` + Num(w/2) + `" fill="none"` + line + `/>`
		},
	},
	"tri": {
		inset: func(w, _ float64, side int, _ float64) float64 {
			if sideOdd(side) {
				return w / 4
			}
			return 0
		},
		size: func(tw, th float64) ShapeSize { return triSize(tw, th, false) },
		draw: func(w, h float64, a, _ string) string {
			return poly([]float64{0, -h / 2, w / 2, h / 2, -w / 2, h / 2}, a)
		},
	},
	"flip-tri": {
		inset: func(w, _ float64, side int, _ float64) float64 {
			if sideOdd(side) {
				return w / 4
			}
			return 0
		},
		size: func(tw, th float64) ShapeSize { return triSize(tw, th, true) },
		draw: func(w, h float64, a, _ string) string {
			return poly([]float64{-w / 2, -h / 2, w / 2, -h / 2, 0, h / 2}, a)
		},
	},
	"sl-rect": {
		inset: func(_, _ float64, side int, _ float64) float64 {
			if side == 0 {
				return 5
			}
			return 0
		},
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X, th + 2*PAD_Y + 10, 5} },
		draw: func(w, h float64, a, _ string) string {
			return poly([]float64{-w / 2, -h/2 + 10, w / 2, -h / 2, w / 2, h / 2, -w / 2, h / 2}, a)
		},
	},
	"notch-pent": {
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X, th + 2*PAD_Y, 0} },
		draw: func(w, h float64, a, _ string) string {
			c := 10.0
			return poly([]float64{-w/2 + c, -h / 2, w/2 - c, -h / 2, w / 2, -h/2 + c, w / 2, h / 2, -w / 2, h / 2, -w / 2, -h/2 + c}, a)
		},
	},
	"hourglass": {
		noLabel: true,
		inset: func(w, _ float64, side int, _ float64) float64 {
			if sideOdd(side) {
				return w / 2
			}
			return 0
		},
		size: fixed(32, 40),
		draw: func(w, h float64, a, _ string) string {
			return poly([]float64{-w / 2, -h / 2, w / 2, -h / 2, -w / 2, h / 2, w / 2, h / 2}, a)
		},
	},
	"delay": {
		size: func(tw, th float64) ShapeSize {
			h := th + 2*PAD_Y
			return ShapeSize{tw + 2*PAD_X + h/4, h, 0}
		},
		draw: func(w, h float64, a, _ string) string {
			r := h / 2
			return `<path d="M` + Num(-w/2) + `,` + Num(-r) + `H` + Num(w/2-r) + `A` + Num(r) + `,` + Num(r) + ` 0 0 1 ` + Num(w/2-r) + `,` +
				Num(r) + `H` + Num(-w/2) + `Z"` + a + `/>`
		},
	},
	"h-cyl": {
		size: func(tw, th float64) ShapeSize {
			h := th + 2*PAD_Y
			rx := math.Min(12, h/4)
			return ShapeSize{tw + 2*PAD_X + float64(2*rx), h, 0}
		},
		draw: func(w, h float64, a, line string) string {
			ry := h / 2
			rx := math.Min(12, h/4)
			l := -w/2 + rx
			r := w/2 - rx
			return `<path d="M` + Num(l) + `,` + Num(-ry) + `H` + Num(r) + `A` + Num(rx) + `,` + Num(ry) + ` 0 0 1 ` + Num(r) + `,` + Num(ry) + `H` + Num(l) + `A` +
				Num(rx) + `,` + Num(ry) + ` 0 0 1 ` + Num(l) + `,` + Num(-ry) + `Z"` + a + `/>` +
				`<path d="M` + Num(r) + `,` + Num(-ry) + `A` + Num(rx) + `,` + Num(ry) + ` 0 0 0 ` + Num(r) + `,` + Num(ry) + `" fill="none"` + line + `/>`
		},
	},
	"cross-circ": {
		noLabel: true,
		inset:   roundInset,
		span:    roundSpan,
		size:    fixed(44, 44),
		draw: func(w, _ float64, a, line string) string {
			r := w / 2
			k := r * sqrt1_2
			return `<circle r="` + Num(r) + `"` + a + `/>` +
				`<path d="M` + Num(-k) + `,` + Num(-k) + `L` + Num(k) + `,` + Num(k) + `M` + Num(k) + `,` + Num(-k) + `L` + Num(-k) + `,` + Num(k) + `" fill="none"` + line + `/>`
		},
	},
	"st-rect": {
		span: func(w, h float64, sideways bool) float64 {
			if sideways {
				return h - 2*shapeCorner - 8
			}
			return w - 2*shapeCorner - 8
		},
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 8, th + 2*PAD_Y + 8, 4} },
		draw: func(w, h float64, a, _ string) string {
			bw := w - 8
			bh := h - 8
			out := ""
			for i := 0; i < 3; i++ {
				x := -w/2 + float64(float64(2-i)*4)
				y := -h/2 + float64(float64(i)*4)
				out += `<rect x="` + Num(x) + `" y="` + Num(y) + `" width="` + Num(bw) + `" height="` + Num(bh) + `"` + Rounded(a) + `/>`
			}
			return out
		},
	},
	"doc": {
		inset: func(_, _ float64, side int, _ float64) float64 {
			if side == 2 {
				return 10
			}
			return 0
		},
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X, th + 2*PAD_Y + 10, -5} },
		draw: func(w, h float64, a, _ string) string { return wave(w, h, a) },
	},
	"flag": {
		inset: func(_, _ float64, side int, _ float64) float64 {
			if sideOdd(side) {
				return 0
			}
			return 8
		},
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X, th + 2*PAD_Y + 16, 0} },
		draw: func(w, h float64, a, _ string) string {
			x := w / 2
			y := h/2 - 8
			q := w / 4
			return `<path d="M` + Num(-x) + `,` + Num(-y) + `q` + Num(q) + `,-10 ` + Num(2*q) + `,0t` + Num(2*q) + `,0V` + Num(y) + `q` + Num(-q) + `,10 ` + Num(-2*q) + `,0t` + Num(-2*q) + `,0Z"` + a + `/>`
		},
	},
	"datastore": {
		size: box,
		span: flat,
		draw: func(w, h float64, a, line string) string {
			return rect(w, h, strings.Replace(a, `stroke="var(--_b)"`, `stroke="none"`, 1), "") +
				`<path d="M` + Num(-w/2) + `,` + Num(-h/2) + `H` + Num(w/2) + `M` + Num(-w/2) + `,` + Num(h/2) + `H` + Num(w/2) + `" fill="none"` + line + `/>`
		},
	},
	"folder": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{math.Max(tw+2*PAD_X, 64), th + 2*PAD_Y + 8, 4} },
		draw: func(w, h float64, a, _ string) string {
			x := w / 2
			y := h / 2
			tab := math.Min(w*0.4, 48)
			return path(`M`+Num(-x)+`,`+Num(-y)+`H`+Num(-x+tab)+`L`+Num(-x+tab+8)+`,`+Num(-y+8)+`H`+Num(x)+`V`+Num(y)+`H`+Num(-x)+`Z`, a)
		},
	},
	"bucket": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 20, th + 2*PAD_Y + 10, 3} },
		inset: func(_, h float64, side int, _ float64) float64 {
			if sideOdd(side) {
				return h / 12
			}
			return 0
		},
		draw: func(w, h float64, a, line string) string {
			x := w / 2
			y := h / 2
			ry := 5.0
			taper := h / 6
			return path(`M`+Num(-x)+`,`+Num(-y+ry)+`A`+Num(x)+`,`+Num(ry)+` 0 0 1 `+Num(x)+`,`+Num(-y+ry)+`L`+Num(x-taper)+`,`+Num(y-ry)+
				`A`+Num(x-taper)+`,`+Num(ry)+` 0 0 1 `+Num(-x+taper)+`,`+Num(y-ry)+`Z`, a) +
				`<path d="M` + Num(-x) + `,` + Num(-y+ry) + `A` + Num(x) + `,` + Num(ry) + ` 0 0 0 ` + Num(x) + `,` + Num(-y+ry) + `" fill="none"` + line + `/>`
		},
	},
	"console": {
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{math.Max(tw+2*PAD_X, 72), th + 2*PAD_Y + 12, 6} },
		draw: func(w, h float64, a, line string) string {
			return rect(w, h, Rounded(a), "") +
				`<path d="M` + Num(-w/2) + `,` + Num(-h/2+12) + `H` + Num(w/2) + `M` + Num(-w/2+7) + `,` + Num(-h/2+4) + `l3,2.5l-3,2.5M` +
				Num(-w/2+13) + `,` + Num(-h/2+9) + `h4" fill="none"` + line + `/>`
		},
	},
	"browser": {
		span: flat,
		size: func(tw, th float64) ShapeSize { return ShapeSize{math.Max(tw+2*PAD_X, 72), th + 2*PAD_Y + 12, 6} },
		draw: func(w, h float64, a, line string) string {
			dots := ""
			for i := 0; i < 3; i++ {
				dots += `<circle cx="` + Num(-w/2+8+float64(float64(i)*7)) + `" cy="` + Num(-h/2+6) + `" r="1.5" fill="none"` + line + `/>`
			}
			return rect(w, h, Rounded(a), "") + `<path d="M` + Num(-w/2) + `,` + Num(-h/2+12) + `H` + Num(w/2) + `" fill="none"` + line + `/>` + dots
		},
	},
	"person": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{math.Max(tw+2*PAD_X, 56), th + 2*PAD_Y + 26, 13} },
		draw: func(w, h float64, a, _ string) string {
			top := -h / 2
			return `<rect x="` + Num(-w/2) + `" y="` + Num(top+26) + `" width="` + Num(w) + `" height="` + Num(h-26) + `" rx="12"` + a + `/>` +
				`<circle cy="` + Num(top+14) + `" r="13"` + a + `/>`
		},
	},
	"bang": {
		size:  func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 28, th + 2*PAD_Y + 24, 0} },
		inset: func(float64, float64, int, float64) float64 { return 3 },
		draw: func(w, h float64, a, _ string) string {
			const spikes = 14
			var pts []float64
			for i := 0; i < spikes*2; i++ {
				angle := (math.Pi*float64(i))/spikes - math.Pi/2
				k := 0.82
				if i%2 == 0 {
					k = 1
				}
				pts = append(pts, js.Cos(angle)*(w/2)*k, js.Sin(angle)*(h/2)*k)
			}
			return poly(pts, a)
		},
	},
	"cloud": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 32, th + 2*PAD_Y + 24, 0} },
		inset: func(_, _ float64, side int, _ float64) float64 {
			if sideOdd(side) {
				return 2
			}
			return 1
		},
		draw: func(w, h float64, a, _ string) string {
			x := w / 2
			y := h / 2
			r := float64(y * 0.55)
			return path(`M`+Num(-x+r)+`,`+Num(y)+`A`+Num(r)+`,`+Num(r)+` 0 0 1 `+Num(-x+float64(r*0.6))+`,`+Num(-y*0.1)+`A`+Num(x*0.42)+`,`+Num(y*0.7)+
				` 0 0 1 `+Num(-x*0.1)+`,`+Num(-y*0.72)+`A`+Num(x*0.4)+`,`+Num(y*0.62)+` 0 0 1 `+Num(x*0.62)+`,`+Num(-y*0.42)+
				`A`+Num(r*1.05)+`,`+Num(r*1.05)+` 0 0 1 `+Num(x-r)+`,`+Num(y)+`Z`, a)
		},
	},
	"brace": {
		bare: true,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 12, th + 2*PAD_Y, 0} },
		draw: func(w, h float64, _, line string) string {
			return rect(w, h, ` fill="none" stroke="none"`, "") + brace(-w/2+12, h, -1, line)
		},
	},
	"brace-r": {
		bare: true,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 12, th + 2*PAD_Y, 0} },
		draw: func(w, h float64, _, line string) string {
			return rect(w, h, ` fill="none" stroke="none"`, "") + brace(w/2-12, h, 1, line)
		},
	},
	"braces": {
		bare: true,
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 24, th + 2*PAD_Y, 0} },
		draw: func(w, h float64, _, line string) string {
			return rect(w, h, ` fill="none" stroke="none"`, "") + brace(-w/2+12, h, -1, line) + brace(w/2-12, h, 1, line)
		},
	},
	"bolt": {
		noLabel: true,
		size:    fixed(36, 56),
		inset: func(w, _ float64, side int, _ float64) float64 {
			if sideOdd(side) {
				return w / 4
			}
			return 0
		},
		draw: func(w, h float64, a, _ string) string {
			return poly([]float64{w * 0.15, -h / 2, -w / 2, h * 0.08, -w * 0.05, h * 0.08, -w * 0.15, h / 2, w / 2, -h * 0.08, w * 0.05, -h * 0.08}, a)
		},
	},
	"lin-cyl": {
		inset: capInset,
		span: func(w, h float64, sideways bool) float64 {
			if sideways {
				return h - 2*shapeCorner - 20
			}
			return w * 0.6
		},
		size: func(tw, th float64) ShapeSize {
			w := math.Max(tw+2*PAD_X, 56)
			ry := math.Min(12, math.Max(6, w/14))
			return ShapeSize{w, th + 2*PAD_Y + float64(2*ry) + 8, ry/2 + 4}
		},
		draw: func(w, h float64, a, line string) string {
			rx := w / 2
			ry := math.Min(12, math.Max(6, w/14))
			top := -h/2 + ry
			bottom := h/2 - ry
			arc := func(y float64) string {
				return `M` + Num(-rx) + `,` + Num(y) + `A` + Num(rx) + `,` + Num(ry) + ` 0 0 0 ` + Num(rx) + `,` + Num(y)
			}
			return path(`M`+Num(-rx)+`,`+Num(top)+`A`+Num(rx)+`,`+Num(ry)+` 0 0 1 `+Num(rx)+`,`+Num(top)+`V`+Num(bottom)+`A`+Num(rx)+`,`+
				Num(ry)+` 0 0 1 `+Num(-rx)+`,`+Num(bottom)+`Z`, a) + `<path d="` + arc(top) + arc(top+8) + `" fill="none"` + line + `/>`
		},
	},
	"curv-trap": {
		size: func(tw, th float64) ShapeSize {
			h := th + 2*PAD_Y
			return ShapeSize{tw + 2*PAD_X + float64(h*0.6), h, 0}
		},
		draw: func(w, h float64, a, _ string) string {
			x := w / 2
			y := h / 2
			k := float64(h * 0.3)
			return path(`M`+Num(-x+k)+`,`+Num(-y)+`H`+Num(x-k)+`A`+Num(k)+`,`+Num(y)+` 0 0 1 `+Num(x-k)+`,`+Num(y)+`H`+Num(-x+k)+`L`+Num(-x)+`,0Z`, a)
		},
	},
	"docs": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 8, th + 2*PAD_Y + 18, -1} },
		inset: func(_, _ float64, side int, _ float64) float64 {
			if side == 2 {
				return 10
			}
			return 0
		},
		draw: func(w, h float64, a, _ string) string {
			out := ""
			for i := 0; i < 3; i++ {
				out += `<g transform="translate(` + js.Int((1-i)*4) + `,` + js.Int((i-1)*4) + `)">` + wave(w-8, h-8, a) + `</g>`
			}
			return out
		},
	},
	"bow-rect": {
		size: func(tw, th float64) ShapeSize {
			h := th + 2*PAD_Y
			return ShapeSize{tw + 2*PAD_X + float64(h*0.4), h, 0}
		},
		inset: func(_, h float64, side int, _ float64) float64 {
			if side == 1 {
				return h * 0.2
			}
			return 0
		},
		draw: func(w, h float64, a, _ string) string {
			x := w / 2
			y := h / 2
			k := float64(h * 0.2)
			return path(`M`+Num(-x+k)+`,`+Num(-y)+`H`+Num(x)+`A`+Num(k)+`,`+Num(y)+` 0 0 0 `+Num(x)+`,`+Num(y)+`H`+Num(-x+k)+`A`+Num(k)+`,`+
				Num(y)+` 0 0 1 `+Num(-x+k)+`,`+Num(-y)+`Z`, a)
		},
	},
	"tag-rect": {
		size: box,
		span: flat,
		draw: func(w, h float64, a, line string) string {
			return rect(w, h, Rounded(a), "") +
				`<path d="M` + Num(w/2-12) + `,` + Num(h/2) + `L` + Num(w/2) + `,` + Num(h/2-12) + `" fill="none"` + line + `/>`
		},
	},
	"tag-doc": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X, th + 2*PAD_Y + 10, -5} },
		inset: func(_, _ float64, side int, _ float64) float64 {
			if side == 2 {
				return 10
			}
			return 0
		},
		draw: func(w, h float64, a, line string) string {
			return wave(w, h, a) + `<path d="M` + Num(w/2-12) + `,` + Num(h/2-14) + `L` + Num(w/2) + `,` + Num(h/2-26) + `" fill="none"` + line + `/>`
		},
	},
	"lin-doc": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 8, th + 2*PAD_Y + 10, -5} },
		inset: func(_, _ float64, side int, _ float64) float64 {
			if side == 2 {
				return 10
			}
			return 0
		},
		draw: func(w, h float64, a, line string) string {
			return wave(w, h, a) + `<path d="M` + Num(-w/2+8) + `,` + Num(-h/2) + `V` + Num(h/2-6) + `" fill="none"` + line + `/>`
		},
	},
	"collapsedGroup": {
		size: func(tw, th float64) ShapeSize { return ShapeSize{tw + 2*PAD_X + 8, th + 2*PAD_Y + 8, -2} },
		draw: func(w, h float64, a, _ string) string {
			return `<rect x="` + Num(-w/2+6) + `" y="` + Num(-h/2+8) + `" width="` + Num(w-12) + `" height="` + Num(h-8) + `"` + Rounded(a) + `/>` +
				`<rect x="` + Num(-w/2) + `" y="` + Num(-h/2) + `" width="` + Num(w) + `" height="` + Num(h-4) + `"` + Rounded(a) + `/>`
		},
	},
}

// Math.SQRT1_2 as JavaScript defines it.
const sqrt1_2 = 0.7071067811865476

func leanSize(tw, th float64) ShapeSize {
	h := th + 2*PAD_Y
	return ShapeSize{tw + 2*PAD_X + h/2, h, 0}
}

func brace(x, h, dir float64, line string) string {
	y := h / 2
	k := 6 * dir
	return `<path d="M` + Num(x) + `,` + Num(-y) + `q` + Num(k) + `,0 ` + Num(k) + `,6V-6q0,6 ` + Num(k) + `,6q` + Num(-k) + `,0 ` + Num(-k) + `,6V` + Num(y-6) +
		`q0,6 ` + Num(-k) + `,6" fill="none"` + line + `/>`
}

func wave(w, h float64, a string) string {
	x := w / 2
	top := -h / 2
	base := h/2 - 10
	q := w / 4
	return `<path d="M` + Num(-x) + `,` + Num(top) + `H` + Num(x) + `V` + Num(base) + `q` + Num(-q) + `,-12 ` + Num(-2*q) + `,0t` + Num(-2*q) + `,0Z"` + a + `/>`
}

func shapeOf(shape string) *shapeDef {
	if d, ok := shapes[shape]; ok {
		return d
	}
	return shapes["rect"]
}

func ShapeSizeOf(shape string, tw, th float64) ShapeSize {
	return shapeOf(shape).size(tw, th)
}

func DrawShape(shape string, w, h float64, a, line string) string {
	return shapeOf(shape).draw(w, h, a, line)
}

func ShapeInset(shape string, w, h float64, side int, t float64) float64 {
	d, ok := shapes[shape]
	if !ok || d.inset == nil {
		return 0
	}
	return d.inset(w, h, side, t)
}

// ShapeSpan is how much of a side of the shape edges may spread along, when the flow runs down or, if `sideways`, across.
func ShapeSpan(shape string, w, h float64, sideways bool) float64 {
	d, ok := shapes[shape]
	if !ok || d.span == nil {
		return 0
	}
	return math.Max(0, d.span(w, h, sideways))
}

func ShapeHasLabel(shape string) bool {
	d, ok := shapes[shape]
	return !ok || !d.noLabel
}

// ShapeFilled reports whether a shape takes a style's fill.
func ShapeFilled(shape string) bool {
	d, ok := shapes[shape]
	return !ok || !d.bare
}

// EndSide is the side of a node's layout box that a route end lies on: 0 top, 1 right, 2 bottom, 3 left, or -1 for none.
func EndSide(route []float64, at int, c *layout.CNode) int {
	dx := route[at] - c.X
	dy := route[at+1] - c.Y
	if math.Abs(math.Abs(dy)-c.H/2) < 0.5 {
		if dy < 0 {
			return 0
		}
		return 2
	}
	if math.Abs(math.Abs(dx)-c.W/2) < 0.5 {
		if dx < 0 {
			return 3
		}
		return 1
	}
	return -1
}

// ShapeView is the shape drawn in a node and its drawn size.
type ShapeView struct {
	Shape string
	W, H  float64
}

// InsetRoute moves a route end from a node's layout box onto the outline of the shape drawn in it.
func InsetRoute(route []float64, at int, view ShapeView, c *layout.CNode) {
	if c.IsGroup {
		return
	}
	side := EndSide(route, at, c)
	if side == 0 || side == 2 {
		amount := ShapeInset(view.Shape, view.W, view.H, side, route[at]-c.X) + (c.H-view.H)/2
		if side == 0 {
			route[at+1] += amount
		} else {
			route[at+1] -= amount
		}
	} else if side >= 0 {
		amount := ShapeInset(view.Shape, view.W, view.H, side, route[at+1]-c.Y) + (c.W-view.W)/2
		if side == 3 {
			route[at] += amount
		} else {
			route[at] -= amount
		}
	}
}
