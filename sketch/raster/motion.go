package raster

import (
	"errors"
	"image"
	"math"
	"strings"

	"tuios.dev/sketch"
)

// A Scene is a parsed drawing, ready to draw many times and to animate to and
// from. Every group with a data-id is an object a Motion can move: a node, an
// edge, a cluster, a label. Objects keep their identity across two drawings of
// the same diagram when their ids match, which pele keeps stable: a node's id,
// and an edge's L_from_to_n.
type Scene struct {
	root          *node
	width, height float64
	vbX, vbY      float64
	objects       []*object
	byKey         map[string]*object
}

type box struct{ x0, y0, x1, y1 float64 }

func (b box) empty() bool          { return b.x1 < b.x0 }
func (b box) center() pt           { return pt{(b.x0 + b.x1) / 2, (b.y0 + b.y1) / 2} }
func (b box) size() (w, h float64) { return b.x1 - b.x0, b.y1 - b.y0 }

func (b *box) add(p pt) {
	b.x0, b.y0 = math.Min(b.x0, p.x), math.Min(b.y0, p.y)
	b.x1, b.y1 = math.Max(b.x1, p.x), math.Max(b.y1, p.y)
}

func emptyBox() box { return box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)} }

type object struct {
	key   string
	n     *node
	chain []*node // the ancestors, root first
	box   box     // the shapes, in root user space
	edge  bool
	main  *node // an edge's line
	line  []pt  // an edge's line, resampled, in root user space
	marks []mark
}

type mark struct {
	n   *node
	end bool // at the end of the line, not the start
}

// samples is the number of points an edge's line is resampled to, so that two
// lines of different shapes can be blended point by point.
const samples = 48

// ParseScene reads an SVG that sketch wrote.
func ParseScene(svg string) (*Scene, error) {
	if err := loadFaces(); err != nil {
		return nil, err
	}
	doc, err := parseXML(svg)
	if err != nil {
		return nil, err
	}
	root := doc
	s := &Scene{root: root, byKey: map[string]*object{}}
	s.width, s.height = lengthOf(attrOr(root, "width"), 300), lengthOf(attrOr(root, "height"), 150)
	if v := numbers(attrOr(root, "viewBox")); len(v) == 4 && v[2] > 0 && v[3] > 0 {
		s.vbX, s.vbY = v[0], v[1]
		s.width, s.height = v[2], v[3]
	}
	s.walk(root, identity, []*node{root}, 0)
	return s, nil
}

// Size is the drawing's size in SVG units.
func (s *Scene) Size() (w, h float64) { return s.width, s.height }

func (s *Scene) walk(n *node, m matrix, chain []*node, depth int) {
	if depth > maxDepth {
		return
	}
	for _, c := range n.children {
		if c.name == "" {
			continue
		}
		cm := m
		if t, ok := c.attr("transform"); ok {
			cm = m.mul(parseTransform(t))
		}
		if id, ok := c.attr("data-id"); ok && c.name == "g" {
			s.addObject(c, id, cm, chain)
		}
		next := make([]*node, len(chain), len(chain)+1)
		copy(next, chain)
		s.walk(c, cm, append(next, c), depth+1)
	}
}

func (s *Scene) addObject(n *node, id string, m matrix, chain []*node) {
	class := attrOr(n, "class")
	key := class + "\x00" + id
	for i := 2; s.byKey[key] != nil; i++ {
		key = class + "\x00" + id + "\x00" + string(rune('0'+i%10))
	}
	o := &object{key: key, n: n, chain: chain, box: emptyBox()}
	o.edge = strings.Contains(" "+class+" ", " pele-edge ")
	if o.edge {
		for _, c := range n.children {
			if c.name == "" {
				continue
			}
			if strings.Contains(attrOr(c, "class"), "pele-marker") {
				o.marks = append(o.marks, mark{n: c})
				continue
			}
			if o.main == nil {
				if p, ok := elementPath(c); ok {
					cm := m
					if t, ok := c.attr("transform"); ok {
						cm = m.mul(parseTransform(t))
					}
					var pts []pt
					for _, l := range flatten(p, cm, 0.25) {
						pts = append(pts, l.pts...)
					}
					if len(pts) >= 2 {
						o.main = c
						o.line = resample(pts, samples)
					}
				}
			}
		}
		for i := range o.marks {
			b := emptyBox()
			shapesBox(o.marks[i].n, m, &b, 0)
			if !b.empty() && o.line != nil {
				c := b.center()
				o.marks[i].end = dist(c, o.line[samples-1]) < dist(c, o.line[0])
			}
		}
	}
	shapesBox(n, m, &o.box, 0)
	if o.box.empty() {
		textBox(n, m, &o.box, 0)
	}
	if o.box.empty() {
		return
	}
	s.objects = append(s.objects, o)
	s.byKey[key] = o
}

// elementPath is the outline of a shape element.
func elementPath(n *node) (path, bool) {
	switch n.name {
	case "path":
		return parsePath(attrOr(n, "d")), true
	case "line":
		var p path
		p.moveTo(pt{number(n, "x1"), number(n, "y1")})
		p.lineTo(pt{number(n, "x2"), number(n, "y2")})
		return p, true
	case "polyline", "polygon":
		return polyPath(numbers(attrOr(n, "points")), n.name == "polygon"), true
	case "rect":
		return rectPath(number(n, "x"), number(n, "y"), number(n, "width"), number(n, "height"), 0, 0), true
	case "circle":
		r := number(n, "r")
		return ellipsePath(number(n, "cx"), number(n, "cy"), r, r), r > 0
	case "ellipse":
		rx, ry := number(n, "rx"), number(n, "ry")
		return ellipsePath(number(n, "cx"), number(n, "cy"), rx, ry), rx > 0 && ry > 0
	}
	return nil, false
}

// shapesBox adds the bounds of the shapes under n, text left out.
func shapesBox(n *node, m matrix, b *box, depth int) {
	if depth > maxDepth {
		return
	}
	if n.name == "text" {
		return
	}
	if p, ok := elementPath(n); ok {
		for _, l := range flatten(p, m, 1) {
			for _, q := range l.pts {
				b.add(q)
			}
		}
	}
	for _, c := range n.children {
		if c.name == "" {
			continue
		}
		cm := m
		if t, ok := c.attr("transform"); ok {
			cm = m.mul(parseTransform(t))
		}
		shapesBox(c, cm, b, depth+1)
	}
}

// textBox adds the anchors of the text under n, for a group with no shapes.
func textBox(n *node, m matrix, b *box, depth int) {
	if depth > maxDepth {
		return
	}
	if n.name == "text" {
		b.add(m.apply(pt{number(n, "x"), number(n, "y")}))
		return
	}
	for _, c := range n.children {
		if c.name == "" {
			continue
		}
		cm := m
		if t, ok := c.attr("transform"); ok {
			cm = m.mul(parseTransform(t))
		}
		textBox(c, cm, b, depth+1)
	}
}

func dist(a, b pt) float64 { return math.Hypot(a.x-b.x, a.y-b.y) }

// resample spaces n points evenly along a polyline.
func resample(pts []pt, n int) []pt {
	cum := make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		cum[i] = cum[i-1] + dist(pts[i-1], pts[i])
	}
	total := cum[len(cum)-1]
	out := make([]pt, n)
	j := 0
	for i := range n {
		at := total * float64(i) / float64(n-1)
		for j < len(pts)-2 && cum[j+1] < at {
			j++
		}
		seg := cum[j+1] - cum[j]
		t := 0.0
		if seg > 0 {
			t = (at - cum[j]) / seg
		}
		out[i] = lerpPt(pts[j], pts[j+1], math.Max(0, math.Min(1, t)))
	}
	return out
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }
func lerpPt(a, b pt, t float64) pt { return pt{lerp(a.x, b.x, t), lerp(a.y, b.y, t)} }

// pose is where an object is drawn in one frame.
type pose struct {
	box     box
	line    []pt
	reveal  float64 // the part of an edge's line drawn, 0 to 1
	opacity float64
}

func (o *object) target() pose { return pose{box: o.box, line: o.line, reveal: 1, opacity: 1} }

// enter is the pose an object comes in from: a little smaller, transparent,
// and for an edge, not drawn yet. kx and ky scale its place toward the origin
// by how much the drawing grows, so an object that comes in at the far edge
// starts inside the old drawing and moves out with the rest.
func (o *object) enter(ox, oy, kx, ky float64) pose {
	c := o.box.center()
	c = pt{ox + (c.x-ox)*kx, oy + (c.y-oy)*ky}
	w, h := o.box.size()
	k := 0.92
	p := pose{box: box{c.x - w*k/2, c.y - h*k/2, c.x + w*k/2, c.y + h*k/2}, line: o.line, opacity: 0, reveal: 1}
	if o.edge {
		p.box = o.box
		p.reveal = 0
		p.line = make([]pt, len(o.line))
		for i, q := range o.line {
			p.line[i] = pt{ox + (q.x-ox)*kx, oy + (q.y-oy)*ky}
		}
	}
	return p
}

func mix(a, b pose, t float64) pose {
	p := pose{
		box:     box{lerp(a.box.x0, b.box.x0, t), lerp(a.box.y0, b.box.y0, t), lerp(a.box.x1, b.box.x1, t), lerp(a.box.y1, b.box.y1, t)},
		reveal:  lerp(a.reveal, b.reveal, t),
		opacity: lerp(a.opacity, b.opacity, t),
		line:    b.line,
	}
	if len(a.line) == len(b.line) && len(a.line) > 0 && t < 1 {
		p.line = make([]pt, len(b.line))
		for i := range b.line {
			p.line[i] = lerpPt(a.line[i], b.line[i], t)
		}
	}
	return p
}

// Motion moves a drawing from what was on screen to a new Scene.
type Motion struct {
	to           *Scene
	from         map[*object]pose
	ghosts       []ghost
	fromW, fromH float64
}

// ghost is an object of an earlier scene that is not in the new one. It fades
// out where it was.
type ghost struct {
	obj      *object
	pose     pose
	opacity0 float64
}

// Ease is the easing of a motion: a cubic ease out, fast at first and settling
// gently, as pele's site animates its charts.
func Ease(p float64) float64 {
	p = math.Max(0, math.Min(1, p))
	return 1 - (1-p)*(1-p)*(1-p)
}

// NewMotion starts a motion to the scene to. prev is the motion on screen, or
// nil, and at is how far through it is, from 0 to 1. A motion that starts while
// another runs starts from where that one has got to, so a change that comes
// mid-motion bends the movement and does not jump.
func NewMotion(prev *Motion, at float64, to *Scene) *Motion {
	m := &Motion{to: to, from: map[*object]pose{}}
	m.fromW, m.fromH = to.width, to.height
	if prev == nil {
		for _, o := range to.objects {
			m.from[o] = o.target()
		}
		return m
	}
	e := Ease(at)
	m.fromW, m.fromH = lerp(prev.fromW, prev.to.width, e), lerp(prev.fromH, prev.to.height, e)
	cur := map[string]pose{}
	for _, o := range prev.to.objects {
		cur[o.key] = prev.poseOf(o, e)
	}
	// An object keeps its pose by key. One whose id changed while it was typed
	// (I, Id, Ide; L_I_B_0, L_Ide_B_0) takes the pose of an unmatched one of
	// the same class whose id it contains, or that contains it.
	matched := map[string]bool{}
	var loose []*object
	for _, o := range to.objects {
		if p, ok := cur[o.key]; ok && p.opacity > 0.01 {
			matched[o.key] = true
			m.from[o] = fit(p, o)
		} else {
			loose = append(loose, o)
		}
	}
	for _, o := range loose {
		class, id, _ := strings.Cut(o.key, "\x00")
		best, bestD := "", 1<<30
		for _, po := range prev.to.objects {
			key, p := po.key, cur[po.key]
			if matched[key] || to.byKey[key] != nil || p.opacity <= 0.01 {
				continue
			}
			c2, id2, _ := strings.Cut(key, "\x00")
			if c2 != class || !(subsequence(id2, id) || subsequence(id, id2)) {
				continue
			}
			if d := abs(len(id) - len(id2)); d < bestD {
				best, bestD = key, d
			}
		}
		if best != "" {
			matched[best] = true
			m.from[o] = fit(cur[best], o)
			continue
		}
		m.from[o] = o.enter(to.vbX, to.vbY, m.fromW/math.Max(to.width, 1), m.fromH/math.Max(to.height, 1))
	}
	for _, po := range prev.to.objects {
		if p := cur[po.key]; !matched[po.key] && p.opacity > 0.01 {
			m.ghosts = append(m.ghosts, ghost{obj: po, pose: p, opacity0: p.opacity})
		}
	}
	for _, g := range prev.ghosts {
		if op := g.opacity0 * (1 - e); op > 0.01 {
			m.ghosts = append(m.ghosts, ghost{obj: g.obj, pose: g.pose, opacity0: op})
		}
	}
	return m
}

// fit makes a pose from another object usable for o: an edge needs a line of
// the same length.
func fit(p pose, o *object) pose {
	if o.edge && len(p.line) != len(o.line) {
		p.line = o.line
	}
	return p
}

// subsequence reports whether every byte of a appears in b, in order.
func subsequence(a, b string) bool {
	if a == "" {
		return false
	}
	i := 0
	for j := 0; j < len(b) && i < len(a); j++ {
		if a[i] == b[j] {
			i++
		}
	}
	return i == len(a)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (m *Motion) poseOf(o *object, e float64) pose {
	from, ok := m.from[o]
	if !ok {
		return o.target()
	}
	return mix(from, o.target(), e)
}

// Still reports whether the motion moves nothing: every object starts where
// it ends and nothing fades out.
func (m *Motion) Still() bool {
	if len(m.ghosts) > 0 || m.fromW != m.to.width || m.fromH != m.to.height {
		return false
	}
	for _, o := range m.to.objects {
		if !samePose(m.from[o], o.target()) {
			return false
		}
	}
	return true
}

func samePose(a, b pose) bool {
	const eps = 0.01
	if math.Abs(a.opacity-b.opacity) > eps || math.Abs(a.reveal-b.reveal) > eps {
		return false
	}
	if math.Abs(a.box.x0-b.box.x0) > eps || math.Abs(a.box.y0-b.box.y0) > eps || math.Abs(a.box.x1-b.box.x1) > eps || math.Abs(a.box.y1-b.box.y1) > eps {
		return false
	}
	if len(a.line) != len(b.line) {
		return false
	}
	for i := range a.line {
		if dist(a.line[i], b.line[i]) > eps {
			return false
		}
	}
	return true
}

// FrameOptions say how to draw a frame of a motion.
type FrameOptions struct {
	// MaxScale is the most pixels per SVG unit. The drawing is scaled to fit
	// the image, centred, and never above this. 0 means 2.
	MaxScale float64
	// Theme overrides the --pele-* tokens of the SVG.
	Theme *sketch.Theme
	// Transparent leaves the background unpainted.
	Transparent bool
	// Readable is Options.Readable.
	Readable bool
}

// Scale is the pixels per SVG unit a frame at progress p uses in an image of
// w by h pixels.
func (m *Motion) Scale(w, h int, p float64, maxScale float64) float64 {
	if maxScale <= 0 {
		maxScale = 2
	}
	e := Ease(p)
	sw, sh := lerp(m.fromW, m.to.width, e), lerp(m.fromH, m.to.height, e)
	return math.Min(maxScale, math.Min(float64(w)/math.Max(sw, 1), float64(h)/math.Max(sh, 1)))
}

// Draw draws the frame at progress p, from 0 to 1, into dst. At 1 and later
// it is the target scene exactly as Draw would draw it.
func (m *Motion) Draw(dst *image.RGBA, p float64, opt FrameOptions) error {
	if dst == nil || dst.Bounds().Empty() {
		return errors.New("raster: no image to draw into")
	}
	b := dst.Bounds()
	W, H := float64(b.Dx()), float64(b.Dy())
	e := Ease(p)
	sw, sh := lerp(m.fromW, m.to.width, e), lerp(m.fromH, m.to.height, e)
	s := m.Scale(b.Dx(), b.Dy(), p, opt.MaxScale)
	base := translate(float64(b.Min.X)+(W-sw*s)/2, float64(b.Min.Y)+(H-sh*s)/2).mul(matrix{s, 0, 0, s, 0, 0}).mul(translate(-m.to.vbX, -m.to.vbY))
	r, st := newRenderer(m.to.root, dst, opt.Theme, base)
	r.readable = opt.Readable
	if opt.Transparent {
		clear(dst.Pix)
	} else {
		r.paintBackground()
	}
	if p < 1 {
		r.over = map[*node]*override{}
		for _, g := range m.ghosts {
			r.setPose(g.obj, g.pose, g.opacity0*(1-e), base)
		}
		for _, o := range m.to.objects {
			if cur := m.poseOf(o, e); !samePose(cur, o.target()) {
				r.setPose(o, cur, cur.opacity, base)
			}
		}
		// Ghosts are drawn first, under the scene. Their state is the one their
		// ancestors in the earlier scene gave them.
		for _, g := range m.ghosts {
			gst := st
			for _, a := range g.obj.chain {
				gst = r.apply(a, gst)
				if t, ok := a.attr("transform"); ok {
					tm := parseTransform(t)
					gst.m, gst.plain = gst.m.mul(tm), gst.plain.mul(tm)
				}
			}
			r.element(g.obj.n, gst)
		}
	}
	st = r.apply(m.to.root, st)
	r.children(m.to.root, st)
	return nil
}

// setPose records the overrides that draw o at pose p.
func (r *renderer) setPose(o *object, p pose, opacity float64, base matrix) {
	inv := base.inverse()
	dev := func(a matrix) matrix { return base.mul(a).mul(inv) }
	ov := &override{opacity: opacity}
	r.over[o.n] = ov
	if o.edge && o.main != nil && len(p.line) >= 2 {
		line := p.line
		if p.reveal < 1 {
			line = prefix(line, p.reveal)
		}
		var path path
		if len(line) >= 2 {
			path.moveTo(line[0])
			for _, q := range line[1:] {
				path.lineTo(q)
			}
		}
		r.over[o.main] = &override{opacity: 1, replace: true, path: path}
		for _, mk := range o.marks {
			if len(line) < 2 {
				r.over[mk.n] = &override{opacity: 0}
				continue
			}
			at, dir := lineEnd(line, mk.end)
			toAt, toDir := lineEnd(o.line, mk.end)
			rot := math.Atan2(dir.y, dir.x) - math.Atan2(toDir.y, toDir.x)
			c, s := math.Cos(rot), math.Sin(rot)
			a := translate(at.x, at.y).mul(matrix{c, s, -s, c, 0, 0}).mul(translate(-toAt.x, -toAt.y))
			mo := 1.0
			if p.reveal < 1 {
				mo = math.Max(0, (p.reveal-0.85)/0.15)
			}
			r.over[mk.n] = &override{opacity: mo, hasM: true, m: dev(a)}
		}
		return
	}
	tw, th := o.box.size()
	cw, ch := p.box.size()
	sx, sy := 1.0, 1.0
	if tw > 0.5 {
		sx = cw / tw
	}
	if th > 0.5 {
		sy = ch / th
	}
	tc, cc := o.box.center(), p.box.center()
	a := translate(cc.x, cc.y).mul(matrix{sx, 0, 0, sy, 0, 0}).mul(translate(-tc.x, -tc.y))
	ov.hasM = true
	ov.m = dev(a)
}

// lineEnd is the point at one end of a line and the direction the line runs
// there, outward.
func lineEnd(line []pt, end bool) (pt, pt) {
	n := len(line)
	k := min(3, n-1)
	if end {
		a, b := line[n-1-k], line[n-1]
		return b, pt{b.x - a.x, b.y - a.y}
	}
	a, b := line[k], line[0]
	return b, pt{b.x - a.x, b.y - a.y}
}

// prefix is the first part f of a line, by length.
func prefix(line []pt, f float64) []pt {
	if f <= 0 {
		return nil
	}
	total := 0.0
	for i := 1; i < len(line); i++ {
		total += dist(line[i-1], line[i])
	}
	want := total * f
	out := []pt{line[0]}
	for i := 1; i < len(line); i++ {
		d := dist(line[i-1], line[i])
		if d >= want {
			if d > 0 {
				out = append(out, lerpPt(line[i-1], line[i], want/d))
			}
			return out
		}
		want -= d
		out = append(out, line[i])
	}
	return out
}

// override is what a motion frame changes about one element.
type override struct {
	opacity float64
	// m, when hasM is set, moves the element: it maps where the element is in
	// the target scene to where it is in this frame, in device space.
	hasM bool
	m    matrix
	// replace draws path in place of the element, in root user space.
	replace bool
	path    path
}

func (m matrix) inverse() matrix {
	det := m.a*m.d - m.b*m.c
	if det == 0 {
		return identity
	}
	return matrix{
		a: m.d / det, b: -m.b / det, c: -m.c / det, d: m.a / det,
		e: (m.c*m.f - m.d*m.e) / det, f: (m.b*m.e - m.a*m.f) / det,
	}
}
