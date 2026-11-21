package shot

import (
	"math"

	"tuios.dev/shot/mosaic"
)

type gop struct {
	op                       byte
	x, y, c1x, c1y, c2x, c2y float64
}

type gpath struct {
	ops     []gop
	opacity float64
	crisp   bool
}

func isProcedural(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x259F:
		return true
	case r >= 0x2800 && r <= 0x28FF:
		return true
	case r >= 0xE0B0 && r <= 0xE0B7:
		return true
	case r >= 0x1CD00 && r <= 0x1FBFF:
		_, _, _, ok := mosaic.Shape(r)
		return ok
	case r == 0x21B5:
		return true
	}
	return false
}

func proceduralPaths(r rune, x, y, w, h float64) ([]gpath, bool) {
	if !isProcedural(r) {
		return nil, false
	}
	x0, y0 := math.Round(x), math.Round(y)
	b := &glyphBuilder{w: math.Round(x+w) - x0, h: math.Round(y+h) - y0}
	b.t = max(math.Round(b.h/14), 1)
	b.o = max(math.Round(b.t*1.6), 2)
	switch {
	case r >= 0x2500 && r <= 0x257F:
		b.boxDrawing(r)
	case r >= 0x2580 && r <= 0x259F:
		b.blockElement(r)
	case r >= 0x2800 && r <= 0x28FF:
		b.braille(r)
	case r >= 0xE0B0 && r <= 0xE0B7:
		b.powerline(r)
	case r == 0x21B5:
		b.returnArrow()
	default:
		b.mosaicBlock(r)
	}
	for i := range b.paths {
		ops := b.paths[i].ops
		for j := range ops {
			ops[j].x += x0
			ops[j].y += y0
			ops[j].c1x += x0
			ops[j].c1y += y0
			ops[j].c2x += x0
			ops[j].c2y += y0
		}
	}
	return b.paths, true
}

type glyphBuilder struct {
	w, h  float64
	t     float64
	o     float64
	paths []gpath
}

func (b *glyphBuilder) clampX(x float64) float64 {
	return min(max(x, 0), b.w)
}

func (b *glyphBuilder) clampY(y float64) float64 {
	return min(max(y, 0), b.h)
}

func (b *glyphBuilder) rect(x0, y0, x1, y1 float64) {
	x0, x1 = math.Round(b.clampX(x0)), math.Round(b.clampX(x1))
	y0, y1 = math.Round(b.clampY(y0)), math.Round(b.clampY(y1))
	if x1 <= x0 || y1 <= y0 {
		return
	}
	b.paths = append(b.paths, gpath{crisp: true, ops: []gop{
		{op: 'M', x: x0, y: y0},
		{op: 'L', x: x1, y: y0},
		{op: 'L', x: x1, y: y1},
		{op: 'L', x: x0, y: y1},
		{op: 'Z'},
	}})
}

func (b *glyphBuilder) opacityRect(x0, y0, x1, y1, opacity float64) {
	b.rect(x0, y0, x1, y1)
	b.paths[len(b.paths)-1].opacity = opacity
}

func (b *glyphBuilder) hseg(y, x0, x1, t float64) {
	b.rect(x0-t/2, y-t/2, x1+t/2, y+t/2)
}

func (b *glyphBuilder) vseg(x, y0, y1, t float64) {
	b.rect(x-t/2, y0-t/2, x+t/2, y1+t/2)
}

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

const kappa = 0.5522847498

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

const (
	armNone  = 0
	armLight = 1
	armHeavy = 2
)

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

func (b *glyphBuilder) drawArms(arms [4]uint8) {
	cx, cy := b.w/2, b.h/2
	l, r, u, d := arms[0], arms[1], arms[2], arms[3]
	hMax := b.armThickness(max(l, r))
	vMax := b.armThickness(max(u, d))
	overH := 0.0
	if u != armNone || d != armNone {
		overH = vMax / 2
	}
	overV := 0.0
	if l != armNone || r != armNone {
		overV = hMax / 2
	}
	switch {
	case l != armNone && l == r:
		t := b.armThickness(l)
		b.rect(0, cy-t/2, b.w, cy+t/2)
	default:
		if l != armNone {
			t := b.armThickness(l)
			b.rect(0, cy-t/2, cx+overH, cy+t/2)
		}
		if r != armNone {
			t := b.armThickness(r)
			b.rect(cx-overH, cy-t/2, b.w, cy+t/2)
		}
	}
	switch {
	case u != armNone && u == d:
		t := b.armThickness(u)
		b.rect(cx-t/2, 0, cx+t/2, b.h)
	default:
		if u != armNone {
			t := b.armThickness(u)
			b.rect(cx-t/2, 0, cx+t/2, cy+overV)
		}
		if d != armNone {
			t := b.armThickness(d)
			b.rect(cx-t/2, cy-overV, cx+t/2, b.h)
		}
	}
}

func (b *glyphBuilder) dashes(n int, vertical bool, weight uint8) {
	t := b.armThickness(weight)
	span := b.w
	if vertical {
		span = b.h
	}
	seg := span / float64(n)
	gap := seg * 0.3
	for i := range n {
		a0 := float64(i)*seg + gap/2
		a1 := float64(i+1)*seg - gap/2
		if vertical {
			b.rect(b.w/2-t/2, a0, b.w/2+t/2, a1)
		} else {
			b.rect(a0, b.h/2-t/2, a1, b.h/2+t/2)
		}
	}
}

func (b *glyphBuilder) arcCorner(dx, dy float64) {
	t := b.t
	cx := math.Round(b.w/2-t/2) + t/2
	cy := math.Round(b.h/2-t/2) + t/2
	frac := cx - math.Floor(cx)
	rr := math.Round(min(b.w/2, b.h/2)*0.75+frac) - frac
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
	ro, ri := rr+t/2, rr-t/2
	ax, ay := cx+dx*rr, cy+dy*rr
	k := kappa
	p0x, p0y := ax, ay-dy*ro
	p1x, p1y := ax-dx*ro, ay
	q0x, q0y := ax, ay-dy*ri
	q1x, q1y := ax-dx*ri, ay
	b.paths = append(b.paths, gpath{ops: []gop{
		{op: 'M', x: p1x, y: p1y},
		{op: 'C', c1x: p1x, c1y: p1y - dy*k*ro, c2x: p0x - dx*k*ro, c2y: p0y, x: p0x, y: p0y},
		{op: 'L', x: q0x, y: q0y},
		{op: 'C', c1x: q1x - dx*k*ri, c1y: q0y, c2x: q1x, c2y: q1y - dy*k*ri, x: q1x, y: q1y},
		{op: 'Z'},
	}})
}

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
	case 0x2550:
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
	case 0x2551:
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
	case 0x2552:
		H(cy-o, cx, b.w)
		H(cy+o, cx, b.w)
		V(cx, cy-o, b.h)
	case 0x2553:
		V(cx-o, cy, b.h)
		V(cx+o, cy, b.h)
		H(cy, cx-o, b.w)
	case 0x2554:
		H(cy-o, cx-o, b.w)
		H(cy+o, cx+o, b.w)
		V(cx-o, cy-o, b.h)
		V(cx+o, cy+o, b.h)
	case 0x2555:
		H(cy-o, 0, cx)
		H(cy+o, 0, cx)
		V(cx, cy-o, b.h)
	case 0x2556:
		V(cx-o, cy, b.h)
		V(cx+o, cy, b.h)
		H(cy, 0, cx+o)
	case 0x2557:
		H(cy-o, 0, cx+o)
		H(cy+o, 0, cx-o)
		V(cx+o, cy-o, b.h)
		V(cx-o, cy+o, b.h)
	case 0x2558:
		H(cy-o, cx, b.w)
		H(cy+o, cx, b.w)
		V(cx, 0, cy+o)
	case 0x2559:
		V(cx-o, 0, cy)
		V(cx+o, 0, cy)
		H(cy, cx-o, b.w)
	case 0x255A:
		V(cx-o, 0, cy+o)
		V(cx+o, 0, cy-o)
		H(cy-o, cx+o, b.w)
		H(cy+o, cx-o, b.w)
	case 0x255B:
		H(cy-o, 0, cx)
		H(cy+o, 0, cx)
		V(cx, 0, cy+o)
	case 0x255C:
		V(cx-o, 0, cy)
		V(cx+o, 0, cy)
		H(cy, 0, cx+o)
	case 0x255D:
		V(cx+o, 0, cy+o)
		V(cx-o, 0, cy-o)
		H(cy-o, 0, cx-o)
		H(cy+o, 0, cx+o)
	case 0x255E:
		V(cx, 0, b.h)
		H(cy-o, cx, b.w)
		H(cy+o, cx, b.w)
	case 0x255F:
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
		H(cy, cx+o, b.w)
	case 0x2560:
		V(cx-o, 0, b.h)
		V(cx+o, 0, cy-o)
		V(cx+o, cy+o, b.h)
		H(cy-o, cx+o, b.w)
		H(cy+o, cx+o, b.w)
	case 0x2561:
		V(cx, 0, b.h)
		H(cy-o, 0, cx)
		H(cy+o, 0, cx)
	case 0x2562:
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
		H(cy, 0, cx-o)
	case 0x2563:
		V(cx+o, 0, b.h)
		V(cx-o, 0, cy-o)
		V(cx-o, cy+o, b.h)
		H(cy-o, 0, cx-o)
		H(cy+o, 0, cx-o)
	case 0x2564:
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
		V(cx, cy+o, b.h)
	case 0x2565:
		H(cy, 0, b.w)
		V(cx-o, cy, b.h)
		V(cx+o, cy, b.h)
	case 0x2566:
		H(cy-o, 0, b.w)
		H(cy+o, 0, cx-o)
		H(cy+o, cx+o, b.w)
		V(cx-o, cy+o, b.h)
		V(cx+o, cy+o, b.h)
	case 0x2567:
		V(cx, 0, cy-o)
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
	case 0x2568:
		H(cy, 0, b.w)
		V(cx-o, 0, cy)
		V(cx+o, 0, cy)
	case 0x2569:
		H(cy+o, 0, b.w)
		H(cy-o, 0, cx-o)
		H(cy-o, cx+o, b.w)
		V(cx-o, 0, cy-o)
		V(cx+o, 0, cy-o)
	case 0x256A:
		V(cx, 0, b.h)
		H(cy-o, 0, b.w)
		H(cy+o, 0, b.w)
	case 0x256B:
		H(cy, 0, b.w)
		V(cx-o, 0, b.h)
		V(cx+o, 0, b.h)
	case 0x256C:
		V(cx-o, 0, cy-o)
		V(cx-o, cy+o, b.h)
		V(cx+o, 0, cy-o)
		V(cx+o, cy+o, b.h)
		H(cy-o, 0, cx-o)
		H(cy-o, cx+o, b.w)
		H(cy+o, 0, cx-o)
		H(cy+o, cx+o, b.w)
	case 0x256D:
		b.arcCorner(1, 1)
	case 0x256E:
		b.arcCorner(-1, 1)
	case 0x256F:
		b.arcCorner(-1, -1)
	case 0x2570:
		b.arcCorner(1, -1)
	case 0x2571:
		b.diagonal(0, b.h, b.w, 0)
	case 0x2572:
		b.diagonal(0, 0, b.w, b.h)
	case 0x2573:
		b.diagonal(0, b.h, b.w, 0)
		b.diagonal(0, 0, b.w, b.h)
	}
}

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
	case r == 0x2580:
		b.rect(0, 0, w, h/2)
	case r >= 0x2581 && r <= 0x2588:
		k := float64(r - 0x2580)
		b.rect(0, h*(8-k)/8, w, h)
	case r >= 0x2589 && r <= 0x258F:
		k := float64(0x2590 - r)
		b.rect(0, 0, w*k/8, h)
	case r == 0x2590:
		b.rect(w/2, 0, w, h)
	case r == 0x2591:
		b.opacityRect(0, 0, w, h, 0.25)
	case r == 0x2592:
		b.opacityRect(0, 0, w, h, 0.5)
	case r == 0x2593:
		b.opacityRect(0, 0, w, h, 0.75)
	case r == 0x2594:
		b.rect(0, 0, w, h/8)
	case r == 0x2595:
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

func (b *glyphBuilder) mosaicBlock(r rune) {
	gx, gy, mask, ok := mosaic.Shape(r)
	if !ok {
		return
	}
	for i := range gx * gy {
		if mask&(1<<i) == 0 {
			continue
		}
		col, row := i%gx, i/gx
		b.rect(math.Round(b.w*float64(col)/float64(gx)), math.Round(b.h*float64(row)/float64(gy)),
			math.Round(b.w*float64(col+1)/float64(gx)), math.Round(b.h*float64(row+1)/float64(gy)))
	}
}

func (b *glyphBuilder) braille(r rune) {
	bits := uint8(r - 0x2800)
	radius := min(b.w/4.5, b.h/9)
	if radius < 0.75 {
		radius = 0.75
	}
	pos := func(col, row int) (float64, float64) {
		x := b.w * (0.25 + 0.5*float64(col))
		y := b.h * (1 + 2*float64(row)) / 8
		return x, y
	}
	for bit := range 8 {
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
	t := b.t * 1.4
	switch r {
	case 0xE0B0:
		b.poly([2]float64{0, 0}, [2]float64{w, h / 2}, [2]float64{0, h})
	case 0xE0B1:
		b.diagonalT(0, 0, w, h/2, t)
		b.diagonalT(w, h/2, 0, h, t)
	case 0xE0B2:
		b.poly([2]float64{w, 0}, [2]float64{0, h / 2}, [2]float64{w, h})
	case 0xE0B3:
		b.diagonalT(w, 0, 0, h/2, t)
		b.diagonalT(0, h/2, w, h, t)
	case 0xE0B4:
		b.halfEllipse(1, false)
	case 0xE0B5:
		b.halfEllipse(1, true)
	case 0xE0B6:
		b.halfEllipse(-1, false)
	case 0xE0B7:
		b.halfEllipse(-1, true)
	}
}

func (b *glyphBuilder) returnArrow() {
	w, h := b.w, b.h
	t := max(h/12, 1)
	x, top, y := w*0.74, h*0.3, h*0.62
	b.vseg(x, top, y, t)
	b.hseg(y, w*0.34, x, t)
	head := h * 0.16
	b.poly([2]float64{w * 0.1, y}, [2]float64{w * 0.4, y - head}, [2]float64{w * 0.4, y + head})
}

func (b *glyphBuilder) diagonalT(x0, y0, x1, y1, t float64) {
	saved := b.t
	b.t = t
	b.diagonal(x0, y0, x1, y1)
	b.t = saved
}

func (b *glyphBuilder) halfEllipse(dir float64, stroke bool) {
	h := b.h
	edge := 0.0
	if dir < 0 {
		edge = b.w
	}
	rx := b.w
	ry := h / 2
	arc := func(rxx, ryy float64, reverse bool) []gop {
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
	inner[0].op = 'L'
	ops := append(outer, inner...)
	ops = append(ops, gop{op: 'Z'})
	b.paths = append(b.paths, gpath{ops: ops})
}
