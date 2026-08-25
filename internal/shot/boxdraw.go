package shot

import "math"

// Procedural geometry for the glyph ranges a font cannot be trusted with.
// Terminals stretch box drawing to fill the whole cell; fonts do not, which
// is why every serious terminal (kitty, alacritty, ghostty) draws these
// ranges itself. The measured Go Mono coverage (box drawing 40/128, braille
// 0/256, powerline 0/8) settles it for the raster backend, and the SVG
// prototype showed the same seams from the other side. So both image
// backends share this one geometry source: a glyph becomes a list of filled
// paths in cell-local pixel coordinates, SVG serializes them, PNG feeds them
// to the rasterizer, and the seams are unconstructible.
//
// Covered: box drawing U+2500-257F, block elements U+2580-259F, braille
// U+2800-28FF, powerline U+E0B0-E0B7.

// A gop is one path operation. x1,y1 is the target point; quads use c1 as
// control; cubes use c1 and c2.
type gop struct {
	op                       byte // 'M', 'L', 'Q', 'C', 'Z'
	x, y, c1x, c1y, c2x, c2y float64
}

// A gpath is one filled subpath group with an optional opacity (0 means 1).
type gpath struct {
	ops     []gop
	opacity float64
}

// IsProcedural reports whether r is drawn as geometry instead of font text.
func IsProcedural(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x259F:
		return true
	case r >= 0x2800 && r <= 0x28FF:
		return true
	case r >= 0xE0B0 && r <= 0xE0B7:
		return true
	}
	return false
}

// proceduralPaths returns the filled paths for r in a cell of w by h pixels,
// with (0,0) the cell's top-left corner. ok is false when r is not in a
// covered range.
func proceduralPaths(r rune, w, h float64) ([]gpath, bool) {
	if !IsProcedural(r) {
		return nil, false
	}
	b := &glyphBuilder{w: w, h: h}
	// Light stroke: about 1/14 of the cell height, never under 1px, so the
	// weight tracks the font size the way a terminal's own box glyphs do.
	b.t = h / 14
	if b.t < 1 {
		b.t = 1
	}
	// Double-line gap offset from the centerline.
	b.o = b.t * 1.6
	if b.o < 2 {
		b.o = 2
	}
	switch {
	case r >= 0x2500 && r <= 0x257F:
		b.boxDrawing(r)
	case r >= 0x2580 && r <= 0x259F:
		b.blockElement(r)
	case r >= 0x2800 && r <= 0x28FF:
		b.braille(r)
	case r >= 0xE0B0 && r <= 0xE0B7:
		b.powerline(r)
	}
	return b.paths, true
}

type glyphBuilder struct {
	w, h  float64 // cell size in px
	t     float64 // light stroke thickness
	o     float64 // double-line offset from center
	paths []gpath
}

func (b *glyphBuilder) clampX(x float64) float64 {
	return min(max(x, 0), b.w)
}

func (b *glyphBuilder) clampY(y float64) float64 {
	return min(max(y, 0), b.h)
}

// edgeBleed is how far a fill that touches a cell edge overdraws into the
// neighbor. Cells land on fractional pixel positions, so two abutting fills
// each cover the shared pixel at partial alpha and a run of box glyphs reads
// as a dashed line. Terminals avoid it by snapping to the pixel grid; the
// backends here cannot (SVG has no grid), so a slight overdraw closes the
// seam instead, in both backends at once.
const edgeBleed = 0.5

// rect appends an axis-aligned filled rectangle, clamped to the cell, with
// edges that touch the cell boundary bled past it.
func (b *glyphBuilder) rect(x0, y0, x1, y1 float64) {
	x0, x1 = b.clampX(x0), b.clampX(x1)
	y0, y1 = b.clampY(y0), b.clampY(y1)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	if x0 <= 0 {
		x0 = -edgeBleed
	}
	if x1 >= b.w {
		x1 = b.w + edgeBleed
	}
	if y0 <= 0 {
		y0 = -edgeBleed
	}
	if y1 >= b.h {
		y1 = b.h + edgeBleed
	}
	b.paths = append(b.paths, gpath{ops: []gop{
		{op: 'M', x: x0, y: y0},
		{op: 'L', x: x1, y: y0},
		{op: 'L', x: x1, y: y1},
		{op: 'L', x: x0, y: y1},
		{op: 'Z'},
	}})
}

// opacityRect is rect with a fill opacity, for the shade blocks.
func (b *glyphBuilder) opacityRect(x0, y0, x1, y1, opacity float64) {
	b.rect(x0, y0, x1, y1)
	b.paths[len(b.paths)-1].opacity = opacity
}

// hseg draws a horizontal stroke segment centered on y, spanning x0..x1
// between stroke centerlines, extended by half a thickness at each end so
// meeting strokes form clean corners.
func (b *glyphBuilder) hseg(y, x0, x1, t float64) {
	b.rect(x0-t/2, y-t/2, x1+t/2, y+t/2)
}

// vseg is hseg turned vertical.
func (b *glyphBuilder) vseg(x, y0, y1, t float64) {
	b.rect(x-t/2, y0-t/2, x+t/2, y1+t/2)
}

// poly appends a filled polygon.
func (b *glyphBuilder) poly(pts ...[2]float64) {
	if len(pts) < 3 {
		return
	}
	ops := []gop{{op: 'M', x: pts[0][0], y: pts[0][1]}}
	for _, p := range pts[1:] {
		ops = append(ops, gop{op: 'L', x: p[0], y: p[1]})
	}
	ops = append(ops, gop{op: 'Z'})
	b.paths = append(b.paths, gpath{ops: ops})
}

// kappa approximates a quarter circle with one cubic.
const kappa = 0.5522847498

// dot appends a filled circle from four cubics.
func (b *glyphBuilder) dot(cx, cy, r float64) {
	k := r * kappa
	b.paths = append(b.paths, gpath{ops: []gop{
		{op: 'M', x: cx + r, y: cy},
		{op: 'C', c1x: cx + r, c1y: cy + k, c2x: cx + k, c2y: cy + r, x: cx, y: cy + r},
		{op: 'C', c1x: cx - k, c1y: cy + r, c2x: cx - r, c2y: cy + k, x: cx - r, y: cy},
		{op: 'C', c1x: cx - r, c1y: cy - k, c2x: cx - k, c2y: cy - r, x: cx, y: cy - r},
		{op: 'C', c1x: cx + k, c1y: cy - r, c2x: cx + r, c2y: cy - k, x: cx + r, y: cy},
		{op: 'Z'},
	}})
}

// Arm weights for the single-weight box range.
const (
	armNone  = 0
	armLight = 1
	armHeavy = 2
)

// boxArms maps U+2500..U+254B and U+2574..U+257F to left,right,up,down arm
// weights. Dashed and double variants are handled separately.
var boxArms = map[rune][4]uint8{
	0x2500: {1, 1, 0, 0}, 0x2501: {2, 2, 0, 0}, 0x2502: {0, 0, 1, 1}, 0x2503: {0, 0, 2, 2},
	0x250C: {0, 1, 0, 1}, 0x250D: {0, 2, 0, 1}, 0x250E: {0, 1, 0, 2}, 0x250F: {0, 2, 0, 2},
	0x2510: {1, 0, 0, 1}, 0x2511: {2, 0, 0, 1}, 0x2512: {1, 0, 0, 2}, 0x2513: {2, 0, 0, 2},
	0x2514: {0, 1, 1, 0}, 0x2515: {0, 2, 1, 0}, 0x2516: {0, 1, 2, 0}, 0x2517: {0, 2, 2, 0},
	0x2518: {1, 0, 1, 0}, 0x2519: {2, 0, 1, 0}, 0x251A: {1, 0, 2, 0}, 0x251B: {2, 0, 2, 0},
	0x251C: {0, 1, 1, 1}, 0x251D: {0, 2, 1, 1}, 0x251E: {0, 1, 2, 1}, 0x251F: {0, 1, 1, 2},
	0x2520: {0, 1, 2, 2}, 0x2521: {0, 2, 2, 1}, 0x2522: {0, 2, 1, 2}, 0x2523: {0, 2, 2, 2},
	0x2524: {1, 0, 1, 1}, 0x2525: {2, 0, 1, 1}, 0x2526: {1, 0, 2, 1}, 0x2527: {1, 0, 1, 2},
	0x2528: {1, 0, 2, 2}, 0x2529: {2, 0, 2, 1}, 0x252A: {2, 0, 1, 2}, 0x252B: {2, 0, 2, 2},
	0x252C: {1, 1, 0, 1}, 0x252D: {2, 1, 0, 1}, 0x252E: {1, 2, 0, 1}, 0x252F: {2, 2, 0, 1},
	0x2530: {1, 1, 0, 2}, 0x2531: {2, 1, 0, 2}, 0x2532: {1, 2, 0, 2}, 0x2533: {2, 2, 0, 2},
	0x2534: {1, 1, 1, 0}, 0x2535: {2, 1, 1, 0}, 0x2536: {1, 2, 1, 0}, 0x2537: {2, 2, 1, 0},
	0x2538: {1, 1, 2, 0}, 0x2539: {2, 1, 2, 0}, 0x253A: {1, 2, 2, 0}, 0x253B: {2, 2, 2, 0},
	0x253C: {1, 1, 1, 1}, 0x253D: {2, 1, 1, 1}, 0x253E: {1, 2, 1, 1}, 0x253F: {2, 2, 1, 1},
	0x2540: {1, 1, 2, 1}, 0x2541: {1, 1, 1, 2}, 0x2542: {1, 1, 2, 2}, 0x2543: {2, 1, 2, 1},
	0x2544: {1, 2, 2, 1}, 0x2545: {2, 1, 1, 2}, 0x2546: {1, 2, 1, 2}, 0x2547: {2, 2, 2, 1},
	0x2548: {2, 2, 1, 2}, 0x2549: {2, 1, 2, 2}, 0x254A: {1, 2, 2, 2}, 0x254B: {2, 2, 2, 2},
	0x2574: {1, 0, 0, 0}, 0x2575: {0, 0, 1, 0}, 0x2576: {0, 1, 0, 0}, 0x2577: {0, 0, 0, 1},
	0x2578: {2, 0, 0, 0}, 0x2579: {0, 0, 2, 0}, 0x257A: {0, 2, 0, 0}, 0x257B: {0, 0, 0, 2},
	0x257C: {1, 2, 0, 0}, 0x257D: {0, 0, 1, 2}, 0x257E: {2, 1, 0, 0}, 0x257F: {0, 0, 2, 1},
}

func (b *glyphBuilder) armThickness(weight uint8) float64 {
	if weight == armHeavy {
		return b.t * 2
	}
	return b.t
}

// drawArms renders a left/right/up/down arm combination in light and heavy
// weights. Each arm runs from its cell edge to the center, overreaching by
// half the thickest perpendicular stroke so junctions have no notch.
func (b *glyphBuilder) drawArms(arms [4]uint8) {
	cx, cy := b.w/2, b.h/2
	l, r, u, d := arms[0], arms[1], arms[2], arms[3]
	hMax := b.armThickness(max(l, r))
	vMax := b.armThickness(max(u, d))
	overH := 0.0 // how far horizontal arms reach past center
	if u != armNone || d != armNone {
		overH = vMax / 2
	}
	overV := 0.0
	if l != armNone || r != armNone {
		overV = hMax / 2
	}
	if l != armNone {
		t := b.armThickness(l)
		b.rect(0, cy-t/2, cx+overH, cy+t/2)
	}
	if r != armNone {
		t := b.armThickness(r)
		b.rect(cx-overH, cy-t/2, b.w, cy+t/2)
	}
	if u != armNone {
		t := b.armThickness(u)
		b.rect(cx-t/2, 0, cx+t/2, cy+overV)
	}
	if d != armNone {
		t := b.armThickness(d)
		b.rect(cx-t/2, cy-overV, cx+t/2, b.h)
	}
}

// dashes renders an n-dash horizontal or vertical line in weight w.
func (b *glyphBuilder) dashes(n int, vertical bool, weight uint8) {
	t := b.armThickness(weight)
	span := b.w
	if vertical {
		span = b.h
	}
	seg := span / float64(n)
	gap := seg * 0.3
	for i := 0; i < n; i++ {
		a0 := float64(i)*seg + gap/2
		a1 := float64(i+1)*seg - gap/2
		if vertical {
			b.rect(b.w/2-t/2, a0, b.w/2+t/2, a1)
		} else {
			b.rect(a0, b.h/2-t/2, a1, b.h/2+t/2)
		}
	}
}

// arcCorner renders one of the light rounded corners U+256D..U+2570.
// dx,dy say which edges the arms leave through: dx=+1 right, -1 left;
// dy=+1 down, -1 up.
func (b *glyphBuilder) arcCorner(dx, dy float64) {
	cx, cy := b.w/2, b.h/2
	t := b.t
	rr := min(cx, cy) * 0.75
	// Straight legs from the edges up to where the arc begins.
	if dx > 0 {
		b.rect(cx+rr, cy-t/2, b.w, cy+t/2)
	} else {
		b.rect(0, cy-t/2, cx-rr, cy+t/2)
	}
	if dy > 0 {
		b.rect(cx-t/2, cy+rr, cx+t/2, b.h)
	} else {
		b.rect(cx-t/2, 0, cx+t/2, cy-rr)
	}
	// The quarter-circle elbow as an outline: the outer arc out, the inner
	// arc back, each one cubic about the center of curvature at
	// (cx+dx*rr, cy+dy*rr).
	ro, ri := rr+t/2, rr-t/2
	ax, ay := cx+dx*rr, cy+dy*rr
	k := kappa
	p0x, p0y := ax, ay-dy*ro // outer edge, vertical-arm side
	p1x, p1y := ax-dx*ro, ay // outer edge, horizontal-arm side
	q0x, q0y := ax, ay-dy*ri // inner edge, vertical-arm side
	q1x, q1y := ax-dx*ri, ay // inner edge, horizontal-arm side
	b.paths = append(b.paths, gpath{ops: []gop{
		{op: 'M', x: p1x, y: p1y},
		{op: 'C', c1x: p1x, c1y: p1y - dy*k*ro, c2x: p0x - dx*k*ro, c2y: p0y, x: p0x, y: p0y},
		{op: 'L', x: q0x, y: q0y},
		{op: 'C', c1x: q1x - dx*k*ri, c1y: q0y, c2x: q1x, c2y: q1y - dy*k*ri, x: q1x, y: q1y},
		{op: 'Z'},
	}})
}

// diagonal renders a stroked diagonal from (x0,y0) to (x1,y1).
func (b *glyphBuilder) diagonal(x0, y0, x1, y1 float64) {
	dx, dy := x1-x0, y1-y0
	l := math.Hypot(dx, dy)
	if l == 0 {
		return
	}
	nx, ny := -dy/l*(b.t/2), dx/l*(b.t/2)
	b.poly([2]float64{x0 + nx, y0 + ny}, [2]float64{x1 + nx, y1 + ny},
		[2]float64{x1 - nx, y1 - ny}, [2]float64{x0 - nx, y0 - ny})
}

func (b *glyphBuilder) boxDrawing(r rune) {
	if arms, ok := boxArms[r]; ok {
		b.drawArms(arms)
		return
	}
	cx, cy := b.w/2, b.h/2
	o, t := b.o, b.t
	H := func(y, x0, x1 float64) { b.hseg(y, x0, x1, t) }
	V := func(x, y0, y1 float64) { b.vseg(x, y0, y1, t) }
	switch r {
	case 0x2504:
		b.dashes(3, false, armLight)
	case 0x2505:
		b.dashes(3, false, armHeavy)
	case 0x2506:
		b.dashes(3, true, armLight)
	case 0x2507:
		b.dashes(3, true, armHeavy)
	case 0x2508:
		b.dashes(4, false, armLight)
	case 0x2509:
		b.dashes(4, false, armHeavy)
	case 0x250A:
		b.dashes(4, true, armLight)
	case 0x250B:
		b.dashes(4, true, armHeavy)
	case 0x254C:
		b.dashes(2, false, armLight)
	case 0x254D:
		b.dashes(2, false, armHeavy)
	case 0x254E:
		b.dashes(2, true, armLight)
	case 0x254F:
		b.dashes(2, true, armHeavy)
	case 0x2550: // ═
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
	case 0x2551: // ║
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
	case 0x2552: // ╒
		H(cy-o, cx, b.w)
		H(cy+o, cx, b.w)
		V(cx, cy-o, b.h)
	case 0x2553: // ╓
		V(cx-o, cy, b.h)
		V(cx+o, cy, b.h)
		H(cy, cx-o, b.w)
	case 0x2554: // ╔
		H(cy-o, cx-o, b.w)
		H(cy+o, cx+o, b.w)
		V(cx-o, cy-o, b.h)
		V(cx+o, cy+o, b.h)
	case 0x2555: // ╕
		H(cy-o, 0, cx)
		H(cy+o, 0, cx)
		V(cx, cy-o, b.h)
	case 0x2556: // ╖
		V(cx-o, cy, b.h)
		V(cx+o, cy, b.h)
		H(cy, 0, cx+o)
	case 0x2557: // ╗
		H(cy-o, 0, cx+o)
		H(cy+o, 0, cx-o)
		V(cx+o, cy-o, b.h)
		V(cx-o, cy+o, b.h)
	case 0x2558: // ╘
		H(cy-o, cx, b.w)
		H(cy+o, cx, b.w)
		V(cx, 0, cy+o)
	case 0x2559: // ╙
		V(cx-o, 0, cy)
		V(cx+o, 0, cy)
		H(cy, cx-o, b.w)
	case 0x255A: // ╚
		V(cx-o, 0, cy+o)
		V(cx+o, 0, cy-o)
		H(cy-o, cx+o, b.w)
		H(cy+o, cx-o, b.w)
	case 0x255B: // ╛
		H(cy-o, 0, cx)
		H(cy+o, 0, cx)
		V(cx, 0, cy+o)
	case 0x255C: // ╜
		V(cx-o, 0, cy)
		V(cx+o, 0, cy)
		H(cy, 0, cx+o)
	case 0x255D: // ╝
		V(cx+o, 0, cy+o)
		V(cx-o, 0, cy-o)
		H(cy-o, 0, cx-o)
		H(cy+o, 0, cx+o)
	case 0x255E: // ╞
		V(cx, 0, b.h)
		H(cy-o, cx, b.w)
		H(cy+o, cx, b.w)
	case 0x255F: // ╟
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
		H(cy, cx+o, b.w)
	case 0x2560: // ╠
		V(cx-o, 0, b.h)
		V(cx+o, 0, cy-o)
		V(cx+o, cy+o, b.h)
		H(cy-o, cx+o, b.w)
		H(cy+o, cx+o, b.w)
	case 0x2561: // ╡
		V(cx, 0, b.h)
		H(cy-o, 0, cx)
		H(cy+o, 0, cx)
	case 0x2562: // ╢
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
		H(cy, 0, cx-o)
	case 0x2563: // ╣
		V(cx+o, 0, b.h)
		V(cx-o, 0, cy-o)
		V(cx-o, cy+o, b.h)
		H(cy-o, 0, cx-o)
		H(cy+o, 0, cx-o)
	case 0x2564: // ╤
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
		V(cx, cy+o, b.h)
	case 0x2565: // ╥
		H(cy, 0, b.w)
		V(cx-o, cy, b.h)
		V(cx+o, cy, b.h)
	case 0x2566: // ╦
		H(cy-o, 0, b.w)
		H(cy+o, 0, cx-o)
		H(cy+o, cx+o, b.w)
		V(cx-o, cy+o, b.h)
		V(cx+o, cy+o, b.h)
	case 0x2567: // ╧
		V(cx, 0, cy-o)
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
	case 0x2568: // ╨
		H(cy, 0, b.w)
		V(cx-o, 0, cy)
		V(cx+o, 0, cy)
	case 0x2569: // ╩
		H(cy+o, 0, b.w)
		H(cy-o, 0, cx-o)
		H(cy-o, cx+o, b.w)
		V(cx-o, 0, cy-o)
		V(cx+o, 0, cy-o)
	case 0x256A: // ╪
		V(cx, 0, b.h)
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
	case 0x256B: // ╫
		H(cy, 0, b.w)
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
	case 0x256C: // ╬
		V(cx-o, 0, cy-o)
		V(cx-o, cy+o, b.h)
		V(cx+o, 0, cy-o)
		V(cx+o, cy+o, b.h)
		H(cy-o, 0, cx-o)
		H(cy-o, cx+o, b.w)
		H(cy+o, 0, cx-o)
		H(cy+o, cx+o, b.w)
	case 0x256D: // ╭ arcs down+right
		b.arcCorner(1, 1)
	case 0x256E: // ╮ down+left
		b.arcCorner(-1, 1)
	case 0x256F: // ╯ up+left
		b.arcCorner(-1, -1)
	case 0x2570: // ╰ up+right
		b.arcCorner(1, -1)
	case 0x2571: // ╱
		b.diagonal(0, b.h, b.w, 0)
	case 0x2572: // ╲
		b.diagonal(0, 0, b.w, b.h)
	case 0x2573: // ╳
		b.diagonal(0, b.h, b.w, 0)
		b.diagonal(0, 0, b.w, b.h)
	}
}

// quadrant bits for U+2596..U+259F.
const (
	qUL = 1 << iota
	qUR
	qLL
	qLR
)

var quadrants = map[rune]uint8{
	0x2596: qLL, 0x2597: qLR, 0x2598: qUL,
	0x2599: qUL | qLL | qLR, 0x259A: qUL | qLR, 0x259B: qUL | qUR | qLL,
	0x259C: qUL | qUR | qLR, 0x259D: qUR, 0x259E: qUR | qLL,
	0x259F: qUR | qLL | qLR,
}

func (b *glyphBuilder) blockElement(r rune) {
	w, h := b.w, b.h
	switch {
	case r == 0x2580: // ▀ upper half
		b.rect(0, 0, w, h/2)
	case r >= 0x2581 && r <= 0x2588: // ▁..█ lower eighths
		k := float64(r - 0x2580)
		b.rect(0, h*(8-k)/8, w, h)
	case r >= 0x2589 && r <= 0x258F: // ▉..▏ left eighths, 7/8 down to 1/8
		k := float64(0x2590 - r)
		b.rect(0, 0, w*k/8, h)
	case r == 0x2590: // ▐ right half
		b.rect(w/2, 0, w, h)
	case r == 0x2591:
		b.opacityRect(0, 0, w, h, 0.25)
	case r == 0x2592:
		b.opacityRect(0, 0, w, h, 0.5)
	case r == 0x2593:
		b.opacityRect(0, 0, w, h, 0.75)
	case r == 0x2594: // ▔ upper eighth
		b.rect(0, 0, w, h/8)
	case r == 0x2595: // ▕ right eighth
		b.rect(w*7/8, 0, w, h)
	default:
		q := quadrants[r]
		if q&qUL != 0 {
			b.rect(0, 0, w/2, h/2)
		}
		if q&qUR != 0 {
			b.rect(w/2, 0, w, h/2)
		}
		if q&qLL != 0 {
			b.rect(0, h/2, w/2, h)
		}
		if q&qLR != 0 {
			b.rect(w/2, h/2, w, h)
		}
	}
}

func (b *glyphBuilder) braille(r rune) {
	bits := uint8(r - 0x2800)
	// Dot layout: bits 0..2 left column rows 0..2, bits 3..5 right column
	// rows 0..2, bit 6 left row 3, bit 7 right row 3.
	radius := min(b.w/4.5, b.h/9)
	if radius < 0.75 {
		radius = 0.75
	}
	pos := func(col, row int) (float64, float64) {
		x := b.w * (0.25 + 0.5*float64(col))
		y := b.h * (1 + 2*float64(row)) / 8
		return x, y
	}
	for bit := 0; bit < 8; bit++ {
		if bits&(1<<bit) == 0 {
			continue
		}
		var col, row int
		switch {
		case bit < 3:
			col, row = 0, bit
		case bit < 6:
			col, row = 1, bit-3
		case bit == 6:
			col, row = 0, 3
		default:
			col, row = 1, 3
		}
		x, y := pos(col, row)
		b.dot(x, y, radius)
	}
}

func (b *glyphBuilder) powerline(r rune) {
	w, h := b.w, b.h
	t := b.t * 1.4 // powerline chevrons read better slightly heavier
	switch r {
	case 0xE0B0: // solid right-pointing triangle
		b.poly([2]float64{0, 0}, [2]float64{w, h / 2}, [2]float64{0, h})
	case 0xE0B1: // right chevron stroke
		b.diagonalT(0, 0, w, h/2, t)
		b.diagonalT(w, h/2, 0, h, t)
	case 0xE0B2: // solid left-pointing triangle
		b.poly([2]float64{w, 0}, [2]float64{0, h / 2}, [2]float64{w, h})
	case 0xE0B3: // left chevron stroke
		b.diagonalT(w, 0, 0, h/2, t)
		b.diagonalT(0, h/2, w, h, t)
	case 0xE0B4: // solid right half-ellipse
		b.halfEllipse(1, false)
	case 0xE0B5: // right half-ellipse stroke
		b.halfEllipse(1, true)
	case 0xE0B6: // solid left half-ellipse
		b.halfEllipse(-1, false)
	case 0xE0B7: // left half-ellipse stroke
		b.halfEllipse(-1, true)
	}
}

// diagonalT is diagonal with an explicit thickness.
func (b *glyphBuilder) diagonalT(x0, y0, x1, y1, t float64) {
	saved := b.t
	b.t = t
	b.diagonal(x0, y0, x1, y1)
	b.t = saved
}

// halfEllipse draws the powerline round cap: dir=+1 bulges right from the
// left edge, dir=-1 bulges left from the right edge.
func (b *glyphBuilder) halfEllipse(dir float64, stroke bool) {
	h := b.h
	edge := 0.0
	if dir < 0 {
		edge = b.w
	}
	rx := b.w
	ry := h / 2
	arc := func(rxx, ryy float64, reverse bool) []gop {
		// Half ellipse from (edge, cy-ryy) around apex (edge+dir*rxx, cy)
		// to (edge, cy+ryy), as two cubics.
		cy := h / 2
		k := kappa
		p0 := [2]float64{edge, cy - ryy}
		p1 := [2]float64{edge + dir*rxx, cy}
		p2 := [2]float64{edge, cy + ryy}
		fwd := []gop{
			{op: 'M', x: p0[0], y: p0[1]},
			{op: 'C', c1x: p0[0] + dir*k*rxx, c1y: p0[1], c2x: p1[0], c2y: p1[1] - k*ryy, x: p1[0], y: p1[1]},
			{op: 'C', c1x: p1[0], c1y: p1[1] + k*ryy, c2x: p2[0] + dir*k*rxx, c2y: p2[1], x: p2[0], y: p2[1]},
		}
		if !reverse {
			return fwd
		}
		return []gop{
			{op: 'M', x: p2[0], y: p2[1]},
			{op: 'C', c1x: p2[0] + dir*k*rxx, c1y: p2[1], c2x: p1[0], c2y: p1[1] + k*ryy, x: p1[0], y: p1[1]},
			{op: 'C', c1x: p1[0], c1y: p1[1] - k*ryy, c2x: p0[0] + dir*k*rxx, c2y: p0[1], x: p0[0], y: p0[1]},
		}
	}
	if !stroke {
		ops := arc(rx, ry, false)
		ops = append(ops, gop{op: 'Z'})
		b.paths = append(b.paths, gpath{ops: ops})
		return
	}
	t := b.t * 1.4
	outer := arc(rx, ry, false)
	inner := arc(rx-t, ry-t, true)
	// Join outer forward and inner backward into one ring; nonzero winding
	// with opposite directions leaves the stroke.
	inner[0].op = 'L'
	ops := append(outer, inner...)
	ops = append(ops, gop{op: 'Z'})
	b.paths = append(b.paths, gpath{ops: ops})
}
