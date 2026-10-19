// Package raster draws the SVG that tuios.dev/sketch writes into an image. It
// reads only the subset of SVG that pele emits: g, a, svg, rect, circle,
// ellipse, polygon, polyline, line, path, text and tspan, with translate,
// rotate, scale and matrix transforms, CSS variables and color-mix. It is not
// a general SVG renderer: images, gradients, filters and clip paths are not
// drawn.
package raster

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/vector"

	"tuios.dev/sketch"
)

// Options say how to draw.
type Options struct {
	// Scale multiplies the SVG's size in pixels. 0 means 1.
	Scale float64
	// Theme overrides the --pele-* tokens of the SVG. nil keeps what the SVG says.
	Theme *sketch.Theme
	// Background paints the image in the diagram's background colour first.
	// Without it the image is transparent where nothing is drawn.
	Background bool
	// Readable lets text in a theme colour change to the theme's text or
	// background colour where a fill the diagram sets itself leaves it below
	// TextFloor. pele leaves such text as it is.
	Readable bool
	// MaxPixels bounds the image. 0 means 40 million pixels.
	MaxPixels int
}

// ErrTooLarge is returned for an image past Options.MaxPixels.
var ErrTooLarge = errors.New("raster: the image would be too large")

type state struct {
	fill, stroke               paint
	fillOpacity, strokeOpacity float64
	opacity                    float64
	strokeWidth                float64
	dashes                     []float64
	dashOffset                 float64
	cap, join                  string
	miterLimit                 float64
	color                      color.NRGBA
	fontSize                   float64
	bold, italic, mono         bool
	anchor                     string
	preserve                   bool
	m                          matrix
	// plain is m without the overrides of a motion frame. moved says they differ,
	// so text is shifted to where its anchor went instead of being scaled.
	plain matrix
	moved bool
	// authored says a fill or text colour came from the diagram's own style,
	// not from the theme.
	authored bool
}

type renderer struct {
	env  map[string]string
	dst  *image.RGBA
	z    vector.Rasterizer
	mask *image.Alpha
	// budget bounds the drawing work, so that hostile markup cannot run for long:
	// each fill costs the pixels of its mask and the rows its edges cross.
	budget int
	// over holds the overrides of a motion frame, by element. nil for a still drawing.
	over map[*node]*override
	// base is the device matrix of the root's user space.
	base matrix
	// readable is Options.Readable.
	readable bool
	// layered is set while a group layer is drawn. A group with an opacity
	// inside it draws its shapes with the opacity folded in, so there is at
	// most one layer at a time.
	layered bool
	// probe, when set, sees each run of text before it is drawn. Tests use it.
	probe func(r *renderer, p path, st state)
}

// Draw rasterises an SVG that sketch wrote. Its budgets bound the work a
// diagram's styles can ask for, but it is not a general SVG renderer: do not
// give it SVG from other sources.
func Draw(svg string, opt Options) (*image.RGBA, error) {
	if err := loadFaces(); err != nil {
		return nil, err
	}
	root, err := parseXML(svg)
	if err != nil {
		return nil, err
	}
	scale := opt.Scale
	if scale <= 0 {
		scale = 1
	}
	w, _ := root.attr("width")
	h, _ := root.attr("height")
	width, height := lengthOf(w, 300), lengthOf(h, 150)
	pw, ph := int(math.Ceil(width*scale)), int(math.Ceil(height*scale))
	limit := opt.MaxPixels
	if limit <= 0 {
		limit = 40_000_000
	}
	if pw <= 0 || ph <= 0 || pw > 32768 || ph > 32768 || pw*ph > limit {
		return nil, ErrTooLarge
	}
	m := matrix{scale, 0, 0, scale, 0, 0}
	if vb, ok := root.attr("viewBox"); ok {
		if n := numbers(vb); len(n) == 4 && n[2] > 0 && n[3] > 0 {
			m = m.mul(matrix{width / n[2], 0, 0, height / n[3], 0, 0}).mul(translate(-n[0], -n[1]))
		}
	}
	r, st := newRenderer(root, image.NewRGBA(image.Rect(0, 0, pw, ph)), opt.Theme, m)
	r.readable = opt.Readable
	if opt.Background {
		r.paintBackground()
	}
	st = r.apply(root, st)
	r.children(root, st)
	return r.dst, nil
}

// newRenderer sets up drawing root into dst with m as the root's device matrix.
func newRenderer(root *node, dst *image.RGBA, theme *sketch.Theme, m matrix) (*renderer, state) {
	r := &renderer{env: map[string]string{}, dst: dst, budget: 300_000_000, base: m}
	style, _ := root.attr("style")
	for _, d := range declarations(style) {
		if strings.HasPrefix(d[0], "--") {
			r.env[d[0]] = d[1]
		}
	}
	for _, v := range theme.Vars() {
		r.env[v[0]] = v[1]
	}
	st := state{
		fill:          paint{c: color.NRGBA{0, 0, 0, 255}},
		stroke:        paint{none: true},
		fillOpacity:   1,
		strokeOpacity: 1,
		opacity:       1,
		strokeWidth:   1,
		cap:           "butt",
		join:          "miter",
		miterLimit:    4,
		color:         color.NRGBA{0, 0, 0, 255},
		fontSize:      16,
		anchor:        "start",
		m:             m,
		plain:         m,
	}
	return r, st
}

// paintBackground fills the image with the diagram's background colour.
func (r *renderer) paintBackground() {
	if c, ok := parseColor(r.resolve("var(--_bg)")); ok {
		draw.Draw(r.dst, r.dst.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	}
}

func (r *renderer) resolve(v string) string {
	if strings.Contains(v, "var(") {
		return resolveVars(v, r.env)
	}
	return v
}

func lengthOf(s string, def float64) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "px"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return v
}

func attrOr(n *node, name string) string {
	v, _ := n.attr(name)
	return v
}

func number(n *node, name string) float64 {
	v, _ := n.attr(name)
	return lengthOf(v, 0)
}

// apply sets the properties an element gives, from its attributes and then its style.
func (r *renderer) apply(n *node, st state) state {
	props := make([][2]string, 0, len(n.attrs))
	props = append(props, n.attrs...)
	if style, ok := n.attr("style"); ok {
		props = append(props, declarations(style)...)
	}
	for _, p := range props {
		name, value := p[0], r.resolve(p[1])
		switch name {
		case "fill", "color", "stroke":
			if raw := strings.ToLower(p[1]); !strings.Contains(raw, "var(") && raw != "none" && raw != "currentcolor" {
				st.authored = true
			}
		}
		switch name {
		case "fill":
			if pt, ok := parsePaint(value); ok {
				st.fill = pt
			}
		case "stroke":
			if pt, ok := parsePaint(value); ok {
				st.stroke = pt
			}
		case "color":
			if c, ok := parseColor(value); ok {
				st.color = c
			}
		case "fill-opacity":
			st.fillOpacity = unit(value, st.fillOpacity)
		case "stroke-opacity":
			st.strokeOpacity = unit(value, st.strokeOpacity)
		case "opacity":
			st.opacity *= unit(value, 1)
		case "stroke-width":
			st.strokeWidth = math.Max(0, lengthOf(value, st.strokeWidth))
		case "stroke-dasharray":
			if value == "none" {
				st.dashes = nil
			} else {
				st.dashes = numbers(strings.ReplaceAll(value, "px", ""))
			}
		case "stroke-dashoffset":
			st.dashOffset = lengthOf(value, 0)
		case "stroke-linecap":
			st.cap = value
		case "stroke-linejoin":
			st.join = value
		case "stroke-miterlimit":
			st.miterLimit = lengthOf(value, 4)
		case "font-size":
			if v := lengthOf(value, -1); v > 0 {
				st.fontSize = math.Min(v, 2000)
			}
		case "font":
			for _, f := range strings.Fields(value) {
				if strings.HasSuffix(f, "px") {
					if v := lengthOf(f, -1); v > 0 {
						st.fontSize = math.Min(v, 2000)
					}
				}
			}
		case "font-family":
			st.mono = strings.Contains(strings.ToLower(value), "mono")
		case "font-weight":
			v := strings.ToLower(value)
			if v == "bold" || v == "bolder" {
				st.bold = true
			} else if n, err := strconv.ParseFloat(v, 64); err == nil {
				st.bold = n >= 600
			} else if v == "normal" || v == "lighter" {
				st.bold = false
			}
		case "font-style":
			st.italic = value == "italic" || value == "oblique"
		case "text-anchor":
			st.anchor = value
		case "xml:space":
			st.preserve = value == "preserve"
		}
	}
	return st
}

func unit(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil {
			return def
		}
		return math.Max(0, math.Min(1, v/100))
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return math.Max(0, math.Min(1, v))
}

func (r *renderer) children(n *node, st state) {
	for _, c := range n.children {
		if c.name != "" {
			r.element(c, st)
		}
	}
}

func (r *renderer) element(n *node, parent state) {
	if r.budget <= 0 {
		return
	}
	r.budget--
	switch n.name {
	case "title", "desc", "defs", "style", "script", "image", "metadata", "foreignObject", "clipPath", "mask", "pattern", "linearGradient", "radialGradient":
		return
	}
	st := r.apply(n, parent)
	if t, ok := n.attr("transform"); ok {
		tm := parseTransform(t)
		st.m = st.m.mul(tm)
		st.plain = st.plain.mul(tm)
	}
	if o := r.over[n]; o != nil {
		st.opacity *= o.opacity
		if st.opacity <= 0.002 {
			return
		}
		if o.hasM {
			st.m = o.m.mul(st.plain)
			st.moved = true
		}
		if o.replace {
			ps := st
			ps.m = r.base
			r.shape(o.path, ps, true)
			return
		}
	}
	switch n.name {
	case "g", "a":
		if op, ok := n.attr("opacity"); ok && len(n.children) > 1 {
			if a := unit(r.resolve(op), 1); a < 1 {
				r.groupLayer(n, parent, st, a)
				return
			}
		}
		r.children(n, st)
	case "svg":
		x, y := number(n, "x"), number(n, "y")
		st.m = st.m.mul(translate(x, y))
		if vb, ok := n.attr("viewBox"); ok {
			if v := numbers(vb); len(v) == 4 && v[2] > 0 && v[3] > 0 {
				w, h := number(n, "width"), number(n, "height")
				s := math.Min(w/v[2], h/v[3])
				st.m = st.m.mul(translate((w-v[2]*s)/2, (h-v[3]*s)/2)).mul(matrix{s, 0, 0, s, -v[0] * s, -v[1] * s})
			}
		}
		r.children(n, st)
	case "rect":
		rx, okx := n.attr("rx")
		ry, oky := n.attr("ry")
		if style, ok := n.attr("style"); ok {
			for _, d := range declarations(style) {
				switch d[0] {
				case "rx":
					rx, okx = d[1], true
				case "ry":
					ry, oky = d[1], true
				}
			}
		}
		rxv, ryv := lengthOf(r.resolve(rx), 0), lengthOf(r.resolve(ry), 0)
		if okx && !oky {
			ryv = rxv
		} else if oky && !okx {
			rxv = ryv
		}
		r.shape(rectPath(number(n, "x"), number(n, "y"), number(n, "width"), number(n, "height"), rxv, ryv), st, true)
	case "circle":
		rad := number(n, "r")
		if rad > 0 {
			r.shape(ellipsePath(number(n, "cx"), number(n, "cy"), rad, rad), st, true)
		}
	case "ellipse":
		rx, ry := number(n, "rx"), number(n, "ry")
		if rx > 0 && ry > 0 {
			r.shape(ellipsePath(number(n, "cx"), number(n, "cy"), rx, ry), st, true)
		}
	case "line":
		var p path
		p.moveTo(pt{number(n, "x1"), number(n, "y1")})
		p.lineTo(pt{number(n, "x2"), number(n, "y2")})
		r.shape(p, st, false)
	case "polygon", "polyline":
		pts, _ := n.attr("points")
		r.shape(polyPath(numbers(pts), n.name == "polygon"), st, n.name == "polygon")
	case "path":
		d, _ := n.attr("d")
		r.shape(parsePath(d), st, true)
	case "text":
		if st.moved {
			a := pt{lengthOf(firstNumber(attrOr(n, "x")), 0), lengthOf(firstNumber(attrOr(n, "y")), 0)}
			to, from := st.m.apply(a), st.plain.apply(a)
			st.m = translate(to.x-from.x, to.y-from.y).mul(st.plain)
		}
		r.text(n, st)
	}
}

// groupLayer draws a group with an opacity of its own the way SVG does: its
// children are drawn together on a layer, and the layer is laid over the
// image once at that opacity, so shapes that overlap inside the group do not
// add up.
func (r *renderer) groupLayer(n *node, parent, st state, a float64) {
	// The layer covers the group's shapes, with room for stroke widths. Text
	// is not measured here, so a group with text takes the whole image.
	rect := r.dst.Bounds()
	if !hasText(n, 0) {
		b := emptyBox()
		shapesBox(n, st.m, &b, 0)
		if b.empty() {
			return
		}
		pad := 2 + st.strokeWidth*st.m.scale()*4
		rect = image.Rect(int(math.Floor(b.x0-pad)), int(math.Floor(b.y0-pad)), int(math.Ceil(b.x1+pad)), int(math.Ceil(b.y1+pad))).Intersect(rect)
	}
	// One layer at a time. Nested groups would each take up to 64 MB, and
	// the XML allows 256 levels.
	if r.layered || rect.Empty() || rect.Dx()*rect.Dy() > 16_000_000 {
		r.children(n, st)
		return
	}
	// The layer is work too: its pixels are allocated, cleared and composed.
	cost := float64(rect.Dx()) * float64(rect.Dy())
	if cost > float64(r.budget) {
		r.budget = -1
		return
	}
	r.budget -= int(cost)
	r.layered = true
	defer func() { r.layered = false }()
	layer := image.NewRGBA(rect)
	dst := r.dst
	r.dst = layer
	inner := st
	inner.opacity = st.opacity / a
	r.children(n, inner)
	r.dst = dst
	draw.DrawMask(r.dst, rect, layer, rect.Min, image.NewUniform(color.Alpha{uint8(math.Round(a * 255))}), image.Point{}, draw.Over)
}

func hasText(n *node, depth int) bool {
	if n.name == "text" {
		return true
	}
	if depth > maxDepth {
		return false
	}
	for _, c := range n.children {
		if c.name != "" && hasText(c, depth+1) {
			return true
		}
	}
	return false
}

// paintColor is the colour a paint gives with an opacity, or false for none.
func (st state) paintColor(p paint, opacity float64) (color.NRGBA, bool) {
	if p.none {
		return color.NRGBA{}, false
	}
	c := p.c
	if p.current {
		c = st.color
	}
	a := float64(c.A) * opacity * st.opacity
	if a <= 0 {
		return color.NRGBA{}, false
	}
	c.A = uint8(math.Round(math.Min(255, a)))
	return c, true
}

// shape fills and strokes a path.
func (r *renderer) shape(p path, st state, fillable bool) {
	if len(p) == 0 {
		return
	}
	scale := st.m.scale()
	lines := flatten(p, st.m, 0.1)
	if fillable {
		if c, ok := st.paintColor(st.fill, st.fillOpacity); ok {
			polys := make([]polygon, 0, len(lines))
			for _, l := range lines {
				polys = append(polys, polygon(l.pts))
			}
			r.fill(polys, c)
		}
	}
	if c, ok := st.paintColor(st.stroke, st.strokeOpacity); ok && st.strokeWidth > 0 {
		// A stroke wider than the image covers all of it, so widths and dash
		// lengths are held to four diagonals. Without the bound a width such
		// as 1e20 puts the stroke's vertices ~1e19 pixels away.
		b := r.dst.Bounds()
		bound := 4 * math.Hypot(float64(b.Dx()), float64(b.Dy()))
		clamp := func(v float64) float64 { return math.Max(-bound, math.Min(bound, v)) }
		ss := strokeStyle{width: clamp(st.strokeWidth * scale), cap: st.cap, join: st.join, miterLimit: st.miterLimit, dashOffset: clamp(st.dashOffset * scale)}
		if len(st.dashes) > 0 {
			ss.dashes = make([]float64, len(st.dashes))
			for i, d := range st.dashes {
				ss.dashes[i] = clamp(d * scale)
			}
		}
		r.fill(stroke(lines, ss), c)
	}
}

// clipRect keeps the part of a polygon between x0 and x1 and between y0 and
// y1. Clipping each polygon to a convex window keeps its winding inside the
// window, so the nonzero union is the same there.
func clipRect(p polygon, x0, x1, y0, y1 float64) polygon {
	clip := func(in polygon, keep func(pt) bool, cross func(a, b pt) pt) polygon {
		if len(in) == 0 {
			return in
		}
		var out polygon
		prev := in[len(in)-1]
		for _, cur := range in {
			if keep(cur) {
				if !keep(prev) {
					out = append(out, cross(prev, cur))
				}
				out = append(out, cur)
			} else if keep(prev) {
				out = append(out, cross(prev, cur))
			}
			prev = cur
		}
		return out
	}
	at := func(x float64) func(a, b pt) pt {
		return func(a, b pt) pt {
			t := (x - a.x) / (b.x - a.x)
			return pt{x, a.y + (b.y-a.y)*t}
		}
	}
	atY := func(y float64) func(a, b pt) pt {
		return func(a, b pt) pt {
			t := (y - a.y) / (b.y - a.y)
			return pt{a.x + (b.x-a.x)*t, y}
		}
	}
	p = clip(p, func(q pt) bool { return q.x >= x0 }, at(x0))
	p = clip(p, func(q pt) bool { return q.x <= x1 }, at(x1))
	p = clip(p, func(q pt) bool { return q.y >= y0 }, atY(y0))
	return clip(p, func(q pt) bool { return q.y <= y1 }, atY(y1))
}

// fill paints the union of polygons, nonzero, in one colour.
func (r *renderer) fill(polys []polygon, c color.NRGBA) {
	if len(polys) == 0 {
		return
	}
	b := r.dst.Bounds()
	X0, Y0, X1, Y1 := float64(b.Min.X), float64(b.Min.Y), float64(b.Max.X), float64(b.Max.Y)
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	clipped := make([]polygon, 0, len(polys))
	for _, p := range polys {
		bad := false
		for _, q := range p {
			if math.IsNaN(q.x) || math.IsNaN(q.y) || math.IsInf(q.x, 0) || math.IsInf(q.y, 0) {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		// y is clipped a pixel outside the image, so every coordinate that
		// reaches the rasterizer is in range and the rows at the edges keep
		// their coverage.
		q := clipRect(p, X0, X1, Y0-1, Y1+1)
		if len(q) < 3 {
			continue
		}
		for _, v := range q {
			minX, maxX = math.Min(minX, v.x), math.Max(maxX, v.x)
			minY, maxY = math.Min(minY, v.y), math.Max(maxY, v.y)
		}
		clipped = append(clipped, q)
	}
	if len(clipped) == 0 {
		return
	}
	x0, x1 := int(math.Floor(minX)), int(math.Ceil(maxX))
	y0, y1 := int(math.Floor(math.Max(Y0, minY))), int(math.Ceil(math.Min(Y1, maxY)))
	if x1 <= x0 || y1 <= y0 {
		return
	}
	w, h := x1-x0, y1-y0
	// The cost is summed in float64 and checked before any conversion, so no
	// value can wrap around an int.
	cost := float64(w) * float64(h)
	for _, p := range clipped {
		for i := range p {
			j := (i + 1) % len(p)
			cost += math.Abs(p[j].y-p[i].y) + 1
		}
	}
	if cost > float64(r.budget) {
		r.budget = -1
		return
	}
	r.budget -= int(cost)
	r.z.Reset(w, h)
	r.z.DrawOp = draw.Src
	ox, oy := float32(x0), float32(y0)
	for _, p := range clipped {
		r.z.MoveTo(float32(p[0].x)-ox, float32(p[0].y)-oy)
		for _, v := range p[1:] {
			r.z.LineTo(float32(v.x)-ox, float32(v.y)-oy)
		}
		r.z.ClosePath()
	}
	if r.mask == nil || cap(r.mask.Pix) < w*h {
		r.mask = image.NewAlpha(image.Rect(0, 0, w, h))
	} else {
		r.mask.Pix = r.mask.Pix[:w*h]
		r.mask.Stride = w
		r.mask.Rect = image.Rect(0, 0, w, h)
	}
	r.z.Draw(r.mask, r.mask.Bounds(), image.Opaque, image.Point{})
	draw.DrawMask(r.dst, image.Rect(x0, y0, x1, y1), &image.Uniform{c}, image.Point{}, r.mask, image.Point{}, draw.Over)
}
