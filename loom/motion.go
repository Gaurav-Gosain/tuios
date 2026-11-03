package loom

import (
	"image"
	"image/color"
	"math"
	"slices"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/tml"
)

// Motion: the enter and exit effects, the skeleton placeholder, stagger and
// the toast's ttl.
//
// The effects are small versions, written for loom, of ideas from bloom
// (github.com/lrstanley/bloom, MIT, by Liam Stanley): its Fade, Slide, Iris,
// Decode and Shimmer cell effects, its Skeleton placeholder, and stagger.
// loom takes no code from it. An effect here is a pure function of the
// box's own cells, what is under them and the progress, so it draws the same
// at every depth: colours blend at truecolor and 256 colours, and step
// through faint at 16 colours and in ASCII.
//
// Nothing here runs a timer. NextFrame tells the host when the surface
// wants Render again: a frame gap while an effect plays or a skeleton
// shimmers, the end of a toast's ttl, or never. With Options.Motion off an
// effect draws its last frame at once, a skeleton stands still and no exit
// plays; a toast still goes away at the end of its ttl.

// effect is one enter or exit effect.
type effect uint8

const (
	fxNone effect = iota
	fxFade
	fxSlideUp
	fxSlideDown
	fxSlideLeft
	fxSlideRight
	fxIris
	fxDecode
)

// effectOf returns the effect a TML enter or exit value names.
func effectOf(name string) effect {
	switch name {
	case "fade":
		return fxFade
	case "slide-up":
		return fxSlideUp
	case "slide-down":
		return fxSlideDown
	case "slide-left":
		return fxSlideLeft
	case "slide-right":
		return fxSlideRight
	case "iris":
		return fxIris
	case "decode":
		return fxDecode
	}
	return fxNone
}

// enterTime is how long each effect plays when a node shows. An exit
// plays in three quarters of it: leaving is quicker than arriving.
var enterTime = [...]time.Duration{
	fxFade: 200 * time.Millisecond, fxSlideUp: 240 * time.Millisecond, fxSlideDown: 240 * time.Millisecond,
	fxSlideLeft: 240 * time.Millisecond, fxSlideRight: 240 * time.Millisecond,
	fxIris: 320 * time.Millisecond, fxDecode: 560 * time.Millisecond,
}

func exitTime(e effect) time.Duration { return enterTime[e] * 3 / 4 }

// The frame gaps: 30 frames a second while an effect plays, 20 while a
// skeleton shimmers, and the time the shimmer's band takes to cross.
const (
	FrameGap      = time.Second / 30
	shimmerGap    = time.Second / 20
	shimmerPeriod = 1600 * time.Millisecond
	maxStagger    = 12 // children past this one start with the twelfth
	maxGhosts     = 16
)

// motion is what one box key carries across builds.
type motion struct {
	b           *box // the box that carries it now; nil once it left
	enter, exit effect
	start       time.Time // when the enter starts, after its stagger
	playing     bool      // the enter runs, or its last frame is due
	skeleton    bool
	ttl         time.Duration
	until       time.Time // when a toast's ttl runs out
	expired     bool      // the ttl ran out: the toast stays away
	label       string    // the toast's text when it ran out
	fresh       bool      // made by this Render's build
	parentPtr   string
	parentKey   string
}

// ghost is a box that left with an exit effect: its last cells, drawn
// over what is now under them until the effect ends.
type ghost struct {
	r     Rect
	cells []uv.Cell
	eff   effect // drawn as an enter played backwards
	start time.Time
	dur   time.Duration
}

// SetMotion turns the effects on or off, as appearance.motion and calm mode
// do in tuios. Off stops what plays and draws every box as it is.
func (s *Surface) SetMotion(on bool) {
	if on == s.opts.Motion {
		return
	}
	s.opts.Motion = on
	if !on {
		s.ghosts = nil
		for _, m := range s.motions {
			m.playing = false
		}
	}
	s.repaint = true
}

// NextFrame reports how long until the surface wants Render again on its
// own: the next frame of an effect or a skeleton's shimmer, or the end of a
// toast's ttl. False means nothing waits, and the host arms no timer.
func (s *Surface) NextFrame() (time.Duration, bool) {
	var wait time.Duration
	ok := false
	at := func(d time.Duration) {
		if !ok || d < wait {
			wait, ok = max(d, 0), true
		}
	}
	if len(s.ghosts) > 0 {
		at(FrameGap)
	}
	if len(s.motions) == 0 {
		return wait, ok
	}
	now := s.timeNow()
	for _, m := range s.motions {
		if m.b == nil {
			continue
		}
		if m.playing {
			at(FrameGap)
		}
		if m.skeleton && s.opts.Motion {
			at(shimmerGap)
		}
		if m.ttl > 0 && !m.expired {
			at(m.until.Sub(now))
		}
	}
	return wait, ok
}

// timeNow is the time of ago() and of the effects.
func (s *Surface) timeNow() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// track records the motion attributes of a box just built. A box with none
// costs the attribute lookups and nothing else.
func (s *Surface) track(b *box, n *tml.Node, vars map[string]any) {
	enter, exit := effectOf(n.AttrString("enter")), effectOf(n.AttrString("exit"))
	_, skel := n.Attr("skeleton")
	var ttl time.Duration
	if n.Type == "toast" {
		// A toast fades in and out unless it asks for another effect.
		if _, set := n.Attr("enter"); !set {
			enter = fxFade
		}
		if _, set := n.Attr("exit"); !set {
			exit = fxFade
		}
		ttl, _ = time.ParseDuration(n.AttrString("ttl"))
	}
	if enter == fxNone && exit == fxNone && !skel && ttl <= 0 {
		return
	}
	if s.motions == nil {
		s.motions = map[string]*motion{}
	}
	now := s.now
	if now.IsZero() {
		now = s.timeNow()
	}
	m := s.motions[b.key]
	if m == nil {
		m = &motion{start: now, playing: enter != fxNone && s.opts.Motion, fresh: true, ttl: ttl}
		if ttl > 0 {
			m.until = now.Add(ttl)
		}
		s.motions[b.key] = m
	}
	was, old := m.skeleton, m.b
	m.b, m.enter, m.exit = b, enter, exit
	if b.parent != nil {
		m.parentPtr, m.parentKey = b.parent.ptr, b.parent.key
	}
	m.skeleton = skel && s.truthy(n, "skeleton", vars)
	if was && !m.skeleton && old != nil && s.opts.Motion && !s.resized && s.buf != nil {
		// The content arrived: the skeleton fades out over it. The
		// buffer still holds the skeleton, as this Render paints later.
		s.addGhost(old, fxFade)
	}
	if bw, ok := b.w.(*badgeWidget); ok {
		m.label = bw.label
	}
	b.mo = m
}

// expiredToast reports whether a toast's ttl ran out and its text is the
// same: it stays away. A new text forgets the old toast, so it shows again.
func (s *Surface) expiredToast(n *tml.Node, key string, vars map[string]any) bool {
	m := s.motions[key]
	if m == nil || !m.expired {
		return false
	}
	if n.Type == "toast" && s.plainText(n, vars) == m.label {
		return true
	}
	delete(s.motions, key)
	return false
}

// staggerChildren starts the enter of each child built in this Render the
// stagger of their parent after the one before it.
func (s *Surface) staggerChildren(n *tml.Node, kids []*box) {
	st := n.AttrString("stagger")
	if st == "" || len(kids) < 2 {
		return
	}
	d, err := time.ParseDuration(st)
	if err != nil || d <= 0 {
		return
	}
	d = min(d, time.Second)
	for i, k := range kids {
		if k.mo != nil && k.mo.fresh {
			k.mo.start = k.mo.start.Add(time.Duration(min(i, maxStagger)) * d)
		}
	}
}

// beginMotion runs at the start of Render: it reads the clock, ends the
// ttl of toasts that ran out, and marks the boxes that play for a paint.
func (s *Surface) beginMotion() {
	s.now = s.timeNow()
	s.animate = false
	if len(s.ghosts) > 0 {
		// The ghosts are drawn over the tree, so the tree under them is
		// painted again first, and once more when the last one ends.
		s.repaint = true
		s.ghosts = slices.DeleteFunc(s.ghosts, func(g ghost) bool { return s.now.Sub(g.start) >= g.dur })
	}
	for _, m := range s.motions {
		if m.ttl > 0 && !m.expired && !s.now.Before(m.until) {
			m.expired = true
			// The toast leaves the tree: build it again without it.
			s.full = true
		}
		if m.b == nil {
			continue
		}
		if m.playing {
			m.b.dirty, s.animate = true, true
			if s.enterProgress(m) >= 1 {
				// This frame draws the box as it is, and the last.
				m.playing = false
			}
		}
		if m.skeleton && s.opts.Motion {
			m.b.dirty, s.animate = true, true
		}
	}
}

// departures finds the boxes with motion that the build of this Render
// dropped. One whose parent is still there plays its exit as a ghost; one
// that left with its parent, such as the content of a tab closed, does not.
func (s *Surface) departures() {
	for k, m := range s.motions {
		if m.b == nil || s.inTree(m.b) {
			continue
		}
		gone := m.b
		m.b = nil
		if s.opts.Motion && m.exit != fxNone && !s.resized && s.parentAlive(m) {
			s.addGhost(gone, m.exit)
		}
		if !m.expired {
			delete(s.motions, k)
		}
	}
	if len(s.motions) > 1024 {
		// Toasts that ran out and whose node never came back.
		for k, m := range s.motions {
			if m.b == nil {
				delete(s.motions, k)
			}
		}
	}
}

// endMotion clears what was new in this Render.
func (s *Surface) endMotion() {
	for _, m := range s.motions {
		m.fresh = false
	}
}

func (s *Surface) inTree(b *box) bool {
	for ; b.parent != nil; b = b.parent {
		if !slices.Contains(b.parent.kids, b) {
			return false
		}
	}
	return b == s.root
}

func (s *Surface) parentAlive(m *motion) bool {
	if m.parentPtr == "" {
		return true
	}
	for _, p := range s.byPtr[m.parentPtr] {
		if p.key == m.parentKey && s.inTree(p) {
			return true
		}
	}
	return false
}

// addGhost keeps the cells a leaving box last drew, from the buffer before
// this Render paints over them.
func (s *Surface) addGhost(b *box, e effect) {
	r := b.rect.Intersect(s.buf.Bounds())
	if r.Empty() || len(s.ghosts) >= maxGhosts {
		return
	}
	cells := make([]uv.Cell, 0, r.Dx()*r.Dy())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		cells = append(cells, s.buf.Line(y)[r.Min.X:r.Max.X]...)
	}
	// An exit plays an enter backwards, so a slide leaves the way it is
	// named: slide-down leaves as slide-up arrives.
	switch e {
	case fxSlideUp:
		e = fxSlideDown
	case fxSlideDown:
		e = fxSlideUp
	case fxSlideLeft:
		e = fxSlideRight
	case fxSlideRight:
		e = fxSlideLeft
	}
	s.ghosts = append(s.ghosts, ghost{r: r, cells: cells, eff: e, start: s.now, dur: exitTime(e)})
	s.repaint = true
}

// enterProgress is how far an enter has come, eased out: 0 hidden, 1 shown.
func (s *Surface) enterProgress(m *motion) float64 {
	d := enterTime[m.enter]
	if d <= 0 {
		return 1
	}
	u := float64(s.now.Sub(m.start)) / float64(d)
	if u <= 0 {
		return 0
	}
	if u >= 1 {
		return 1
	}
	return 1 - (1-u)*(1-u)*(1-u)
}

// playEnter draws the enter of b over the cells paintBox just drew.
func (s *Surface) playEnter(b *box, c *canvas) {
	p := s.enterProgress(b.mo)
	if p >= 1 {
		return
	}
	vis := b.rect.Intersect(c.clip).Intersect(s.buf.Bounds())
	if vis.Empty() {
		return
	}
	n := vis.Dx() * vis.Dy()
	if cap(s.fxCells) < n {
		s.fxCells = make([]uv.Cell, n)
	}
	own := s.fxCells[:0]
	for y := vis.Min.Y; y < vis.Max.Y; y++ {
		own = append(own, s.buf.Line(y)[vis.Min.X:vis.Max.X]...)
	}
	ground := "panel"
	if b.parent != nil && b.parent.ground != "" {
		ground = b.parent.ground
	}
	under := uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: s.pal.Color(ground)}}
	s.fxDraw(b.mo.enter, p, vis, own, &under)
}

// drawGhosts draws each leaving box over the tree.
func (s *Surface) drawGhosts() {
	bounds := s.buf.Bounds()
	for _, g := range s.ghosts {
		if !g.r.In(bounds) {
			continue
		}
		u := float64(s.now.Sub(g.start)) / float64(g.dur)
		u = min(max(u, 0), 1)
		s.fxDraw(g.eff, 1-u*u, g.r, g.cells, nil)
		s.addDamage(g.r)
	}
}

// fxDraw draws effect e at progress p (0 hidden, 1 shown) over r. own
// holds the box's cells over r, row by row. under is what shows where the
// box does not: a ground cell for an enter, or nil for a ghost, whose
// buffer already holds the tree under it.
func (s *Surface) fxDraw(e effect, p float64, r Rect, own []uv.Cell, under *uv.Cell) {
	w, h := r.Dx(), r.Dy()
	at := func(lx, ly int) *uv.Cell {
		if lx < 0 || ly < 0 || lx >= w || ly >= h {
			return nil
		}
		return &own[ly*w+lx]
	}
	// The slide's offset, in cells, and the way the box comes from.
	var sx, sy, off int
	switch e {
	case fxSlideUp, fxSlideDown:
		sy = 1
		if e == fxSlideDown {
			sy = -1
		}
		off = int(math.Round((1 - p) * float64(clampInt(h, 1, 4))))
	case fxSlideLeft, fxSlideRight:
		sx = 1
		if e == fxSlideRight {
			sx = -1
		}
		off = int(math.Round((1 - p) * float64(clampInt(w/4, 2, 8))))
	}
	// The iris: rows count double, so the circle looks round.
	cx, cy := float64(w)/2, float64(h)/2
	r2 := (cx*cx + 4*cy*cy) * p * p
	s.fxBase, s.fxFg = s.fadeGround(), s.pal.Color("fg")
	step := uint32(p * 48) // the decode glyphs change 48 times
	accent := s.pal.Color("accent-bright")
	for ly := range h {
		line := s.buf.Line(r.Min.Y + ly)
		shownPrev := true
		for lx := range w {
			x := r.Min.X + lx
			u := line[x]
			if under != nil {
				u = *under
			}
			var out uv.Cell
			o := at(lx, ly)
			shown := true
			switch e {
			case fxFade:
				out = s.fadeCell(*o, u, p)
			case fxSlideUp, fxSlideDown, fxSlideLeft, fxSlideRight:
				src := at(lx-sx*off, ly-sy*off)
				if src == nil {
					out, shown = u, false
				} else {
					out = s.fadeCell(*src, u, p)
				}
			case fxIris:
				dx, dy := float64(lx)+0.5-cx, 2*(float64(ly)+0.5-cy)
				if dx*dx+dy*dy < r2 {
					out = *o
				} else {
					out, shown = u, false
				}
			case fxDecode:
				// Each column starts after a random delay and falls to the
				// bottom by the end; its front shows a random glyph.
				delay := hash01(lx, -1, 7) * 0.5
				front := (p - delay) / (1 - delay) * float64(h+1)
				switch {
				case float64(ly)+1 <= front:
					out = *o
				case float64(ly) < front && o.Width == 1 && o.Content != " " && o.Content != "":
					out = *o
					i := int(hash01(lx, ly, step) * float64(len(decodeGlyphs)))
					out.Content = decodeGlyphs[i : i+1]
					out.Style.Fg, out.Style.Attrs = accent, attrBold
				default:
					out, shown = u, false
				}
			default:
				out = *o
			}
			// The tail of a wide glyph goes with its lead.
			if out.Width == 0 && !shownPrev {
				out = u
			}
			shownPrev = shown || out.Width != 0
			line[x] = out
		}
		mendRow(line, r.Min.X, r.Max.X)
	}
}

// decodeGlyphs are what the front of a decode column shows. All ASCII, so
// they draw at every depth.
const decodeGlyphs = `0123456789ABCDEF:=+*<>|/\`

// mendRow blanks a wide lead whose tail was replaced and a tail whose lead
// was, so an effect that mixes two sources never leaves a broken row.
func mendRow(line uv.Line, x0, x1 int) {
	for x := max(x0-1, 0); x < min(x1+1, len(line)); x++ {
		c := &line[x]
		switch {
		case c.Width > 1 && (x+1 >= len(line) || line[x+1].Width != 0):
			c.Content, c.Width = " ", 1
		case c.Width == 0 && (x == 0 || line[x-1].Width < 2):
			c.Content, c.Width = " ", 1
		}
	}
}

// fadeCell blends from u, what is under the box, to o, the box's own cell,
// by p. At 16 colours and in ASCII there is nothing to blend: the cell
// steps from u through o in faint to o.
func (s *Surface) fadeCell(o, u uv.Cell, p float64) uv.Cell {
	if p >= 1 {
		return o
	}
	base := s.fxBase
	if base == nil {
		switch {
		case p < 1.0/3:
			return u
		case p < 2.0/3:
			o.Style.Attrs |= attrFaint
		}
		return o
	}
	ub := cmpColor(u.Style.Bg, base)
	ob := cmpColor(o.Style.Bg, ub)
	bg := s.mix(ub, ob, p)
	out := o
	blankUnder := u.Content == " " || u.Content == ""
	switch {
	case blankUnder:
		out.Style.Fg = s.mix(bg, cmpColor(o.Style.Fg, s.fxFg), p)
	case p < 0.5 && u.Width == 1 && o.Width == 1:
		// A crossfade: what was under fades out, then the box fades in.
		out = u
		out.Style.Fg = s.mix(cmpColor(u.Style.Fg, s.fxFg), bg, 2*p)
	default:
		out.Style.Fg = s.mix(bg, cmpColor(o.Style.Fg, s.fxFg), 2*p-1)
	}
	if u.Style.Bg == nil && o.Style.Bg == nil {
		out.Style.Bg = nil
	} else {
		out.Style.Bg = bg
	}
	return out
}

// fadeGround is the colour a fade starts from where a cell has no ground
// of its own, or nil when the palette cannot blend.
func (s *Surface) fadeGround() color.Color {
	if s.pal.Depth > Color256 {
		return nil
	}
	if c := s.pal.Color(s.ground()); c != nil {
		return c
	}
	if c := s.pal.Color("canvas"); c != nil {
		return c
	}
	return s.pal.Color("panel")
}

func cmpColor(c, or color.Color) color.Color {
	if c == nil {
		return or
	}
	return c
}

// mixKey names a blend the surface already made: two colours and a step of
// sixteen.
type mixKey struct {
	a, b color.RGBA
	k    uint8
}

// lastMix is the last blend mix made.
type lastMix struct {
	a, b, c color.Color
	k       uint8
	ok      bool
}

// mix blends a toward b by t, in sixteen steps, at the palette's depth.
// Each blend is made once: an effect asks for the same few on every frame.
func (s *Surface) mix(a, b color.Color, t float64) color.Color {
	k := uint8(math.Round(min(max(t, 0), 1) * 16))
	switch {
	case a == nil || k >= 16 || a == b:
		return b
	case b == nil || k == 0:
		return a
	}
	// Neighbouring cells blend the same pair: compare the colours as
	// given before converting them for the map.
	if l := &s.lastMix; l.ok && l.k == k && l.a == a && l.b == b {
		return l.c
	}
	key := mixKey{toRGBA(a), toRGBA(b), k}
	if c, ok := s.mixes[key]; ok {
		s.lastMix = lastMix{a: a, b: b, k: k, c: c, ok: true}
		return c
	}
	if s.mixes == nil || len(s.mixes) > 4096 {
		s.mixes = map[mixKey]color.Color{}
	}
	m := mixRGB(key.a, key.b, float64(k)/16)
	var c color.Color = m
	if s.pal.Depth == Color256 {
		c = ansi.IndexedColor(nearest256(m))
	}
	s.mixes[key] = c
	return c
}

// hash01 is a well mixed number in [0, 1) for a cell and a seed.
func hash01(x, y int, seed uint32) float64 {
	h := uint64(x)*0x9e3779b97f4a7c15 ^ uint64(y)*0xc2b2ae3d27d4eb4f ^ uint64(seed)*0x165667b19e3779f9 //nolint:gosec // a hash wraps
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return float64(h>>11) / (1 << 53)
}

// paintSkeleton draws a placeholder for b while its content loads: a card
// keeps its frame and title, and the rest is bars of uneven length, with a
// band of light across them while motion is on.
func (s *Surface) paintSkeleton(b *box, c *canvas) {
	in := b.inner()
	if cw, ok := b.w.(*cardWidget); ok {
		cw.paint(s, b, c, in)
		in = viewport(b)
	}
	w, h := in.Dx(), in.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	bar := "▅"
	if s.pal.Depth == ASCII {
		bar = "-"
	}
	// The band runs on a diagonal, a row counting as two columns.
	band, center := 6.0, -1e9
	if s.opts.Motion {
		ph := float64(s.now.Sub(b.mo.start)%shimmerPeriod) / float64(shimmerPeriod)
		center = -band + ph*(float64(w+2*h)+2*band)
	}
	track, lit := s.pal.Color(RoleTrack), s.pal.Color("dim")
	last := h - 1
	if h >= 3 && last%3 == 2 {
		last--
	}
	for y := range h {
		if h >= 3 && y%3 == 2 {
			continue
		}
		frac := 0.6 + 0.35*hash01(y, w, 3)
		if y == last && h > 1 {
			frac = 0.4
		}
		n := max(1, int(float64(w)*frac))
		for x := range n {
			g, fg := bar, track
			if d := math.Abs(float64(x+2*y) - center); d < band/2 {
				st := 1 - d/(band/2)
				switch {
				case s.pal.Depth <= Color256:
					fg = s.mix(track, lit, 0.7*st)
				case st > 0.4 && s.pal.Depth == Color16:
					fg = lit
				case st > 0.4:
					g = "="
				}
			}
			cell := uv.Cell{Content: g, Width: 1, Style: uv.Style{Fg: fg}}
			if under := c.buf.CellAt(in.Min.X+x, in.Min.Y+y); under != nil {
				cell.Style.Bg = under.Style.Bg
			}
			if (image.Point{in.Min.X + x, in.Min.Y + y}).In(c.clip) {
				c.buf.Line(in.Min.Y + y)[in.Min.X+x] = cell
			}
		}
	}
}
