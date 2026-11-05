package main

import (
	"math"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// The split tree's panes, each at a rectangle of its own in one parent.
// A change of layout (a split, a pane closing, zoom, a divider moved by a
// key, the splits evened out) moves every pane's rectangle at once, on one
// curve (motionLayout), from where the last frame showed it: no pane is
// built under another parent, so none fades and none jumps once the move
// ends.
//
// A rectangle is a fraction of the area the gaps included: (W+gap) x
// (H+gap), a pane's box being its rectangle less a gap on the right and
// below. The fractions animate, so the panes follow a window or a sidebar
// resizing at once, and only a change of layout moves them.

// frac is a pane's rectangle in fractions of the area.
type frac struct{ x, y, w, h float64 }

// fracSplit is a split of the tree: where its divider goes.
type fracSplit struct {
	key string
	l   *proto.Layout
	r   frac // the split's rectangle
	at  float64
}

// layoutFracs places the panes of l in r: their rectangles in out, the
// splits in splits, and for a pane split off just now (fresh), the slot it
// grows out of: no width (or height) at its edge of the split.
func layoutFracs(l *proto.Layout, r frac, key string, fresh func(string) bool, out, from map[string]frac, splits *[]fracSplit) {
	if l.Leaf() {
		out[l.Pane] = r
		return
	}
	ratio := l.Ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	ra, rb := r, r
	if l.Row {
		ra.w = r.w * ratio
		rb.x, rb.w = r.x+ra.w, r.w-ra.w
		*splits = append(*splits, fracSplit{key, l, r, rb.x})
	} else {
		ra.h = r.h * ratio
		rb.y, rb.h = r.y+ra.h, r.h-ra.h
		*splits = append(*splits, fracSplit{key, l, r, rb.y})
	}
	// A pane split off grows out of the other side's far edge: the
	// split starts with all its room on the other side.
	isFresh := func(n *proto.Layout) bool { return n.Leaf() && fresh(n.Pane) }
	if fa, fb := isFresh(l.A), isFresh(l.B); fa != fb {
		slot := r
		switch {
		case fb && l.Row:
			slot.x, slot.w = r.x+r.w, 0
		case fb:
			slot.y, slot.h = r.y+r.h, 0
		case l.Row:
			slot.w = 0
		default:
			slot.h = 0
		}
		if fb {
			from[l.B.Pane] = slot
		} else {
			from[l.A.Pane] = slot
		}
	}
	layoutFracs(l.A, ra, key+"a", fresh, out, from, splits)
	layoutFracs(l.B, rb, key+"b", fresh, out, from, splits)
}

// splitGuess is a split on its way to the daemon, drawn at once: the
// layout as it will be, with an empty card where the new pane will be.
// The pane the daemon makes takes the card's place and its motion, so the
// split moves once, from the key, not from the daemon's answer.
type splitGuess struct {
	id     string        // the empty card's
	layout *proto.Layout // the layout with it
	panes  int           // the session's panes as it was made
}

// guessSplit draws the split p asks for in s before the daemon answers:
// in the split tree, of a pane it shows, with nothing zoomed.
func (a *App) guessSplit(s *proto.Session, p proto.CreateParams) {
	if s.PaneByID(s.Zoomed) != nil || s.PaneByID(p.Split) == nil {
		return
	}
	if ar := s.Arrange; ar != nil {
		// The layouts placed by rectangles place the new pane themselves.
		if _, placed := ar.Rects[p.Split]; placed || len(ar.Float) > 0 {
			return
		}
	}
	base := s.Layout
	if len(s.Panes) < 2 {
		base = nil
	} else if base == nil || !slices.Contains(base.Panes(), p.Split) {
		return
	}
	if a.guesses == nil {
		a.guesses = map[string]*splitGuess{}
	}
	id := "guess-" + s.ID
	a.guesses[s.ID] = &splitGuess{id: id, layout: base.Split(p.Split, id, p.Direction, 0.5), panes: len(s.Panes)}
	// The panes keep their grids while it moves, as for any layout change.
	holdLeases(motionLayout.Duration + 40*time.Millisecond)
}

// tileAnim is a pane's rectangle on its way from one place to another,
// on motionLayout's curve. guios eases it itself, not with AnimateTo: the
// move ends in the frame whose rectangle rounds to the target, so no frame
// after the last change asks to be drawn.
type tileAnim struct {
	from, cur, to frac
	start         time.Time
	// spring lands it on a critically damped spring (stiffness 1000),
	// as a dropped pane lands, rather than on motionLayout's curve.
	spring bool
}

// landOmega is the landing spring's angular frequency: the square root of
// its stiffness, 1000, at a mass of 1.
var landOmega = math.Sqrt(1000)

// step moves the rectangle to where it is at now, in an area of ew x eh
// DIPs, and returns how far it still has to go, 0 when it is there.
func (t *tileAnim) step(now time.Time, instant bool, ew, eh float64) float64 {
	if t.cur == t.to {
		return 0
	}
	k := float64(now.Sub(t.start)) / float64(motionLayout.Duration)
	if instant || (k >= 1 && !t.spring) {
		t.cur = t.to
		return 0
	}
	var e float64
	if t.spring {
		// Critically damped: the distance left shrinks as (1+wt)e^-wt.
		// The e2e build's GUIOS_MOTION_SCALE slows it with the rest.
		w := landOmega * max(now.Sub(t.start).Seconds(), 0) * float64(200*time.Millisecond) / float64(motionLayout.Duration)
		e = 1 - (1+w)*math.Exp(-w)
	} else {
		e = float64(easeOut(float32(max(k, 0))))
	}
	lerp := func(a, b float64) float64 { return a + (b-a)*e }
	t.cur = frac{lerp(t.from.x, t.to.x), lerp(t.from.y, t.to.y), lerp(t.from.w, t.to.w), lerp(t.from.h, t.to.h)}
	d := max(math.Abs(t.to.x-t.cur.x)*ew, math.Abs(t.to.y-t.cur.y)*eh,
		math.Abs(t.to.w-t.cur.w)*ew, math.Abs(t.to.h-t.cur.h)*eh)
	if d < 0.5 {
		// Within half a DIP of the target: drawn there, it rounds to it.
		t.cur = t.to
		return 0
	}
	return d
}

// progress is how far the rectangle is from its start to its target, 0
// to 1.
func (t *tileAnim) progress() float32 {
	if t.cur == t.to {
		return 1
	}
	span := math.Abs(t.to.w-t.from.w) + math.Abs(t.to.h-t.from.h) + math.Abs(t.to.x-t.from.x) + math.Abs(t.to.y-t.from.y)
	if span == 0 {
		return 1
	}
	return float32(1 - (math.Abs(t.to.w-t.cur.w)+math.Abs(t.to.h-t.cur.h)+math.Abs(t.to.x-t.cur.x)+math.Abs(t.to.y-t.cur.y))/span)
}

// tiled draws the panes of s: the split tree, the zoomed pane over it, or
// the focused pane alone without a tree. right tells that the area
// reaches the window's right edge.
func (a *App) tiled(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, right bool) {
	key := "tiles-" + s.ID
	area := ui.Box(c.Key(key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Bind(a.paneRef(key))
	b := a.paneRef(key).Bounds(c)
	ew, eh := float64(b.W)+paneGap, float64(b.H)+paneGap

	lay := s.Layout
	g := a.guesses[s.ID]
	if g != nil && len(s.Panes) > g.panes {
		// The daemon's pane came with the state before its answer.
		delete(a.guesses, s.ID)
		g = nil
	}
	if g != nil {
		lay = g.layout
	}
	multi := len(s.Panes) > 1 || g != nil
	target := map[string]frac{}
	from := map[string]frac{}
	var splits []fracSplit
	whole := frac{0, 0, 1, 1}
	z := s.PaneByID(s.Zoomed)
	if lay != nil && multi {
		layoutFracs(lay, whole, "split-"+s.ID, a.motion.isNew, target, from, &splits)
	} else if p := s.FocusPane(); z == nil && p != nil {
		target[p.ID] = whole
	}
	// The zoomed pane fills the area, over the others: they stay under it
	// while it grows, and come back under it as it shrinks.
	zoomed := ""
	if z != nil {
		zoomed = z.ID
		target[z.ID] = whole
		a.motion.top = z.ID
	}
	// A window too small for every pane at the smallest pane size shows
	// the focused pane alone, as zoom does, until it grows again (QA-006).
	// The session is not zoomed: the TUI on it keeps its layout.
	hidden := 0
	if f := a.focusOf(s); zoomed == "" && g == nil && multi && b.W > 0 && a.tooSmall(target, ew, eh) {
		if _, ok := target[f]; ok {
			zoomed, hidden = f, len(target)-1
			target[f] = whole
			a.motion.top = f
		}
	}

	// The guessed card went as the daemon answered: the pane it made
	// goes on from where the card showed.
	if last := a.motion.guess; last.id != "" && (g == nil || g.id != last.id) {
		for id := range from {
			if _, ok := target[last.id]; !ok {
				from[id] = last.at
			}
		}
		a.motion.guess = guessShown{}
	}
	// The rectangles this frame shows: each value on its way to the
	// target. A pane new to the tree starts in its slot.
	shownAt := map[string]frac{}
	now := c.Now()
	instant := c.Instant() || c.Preferences().ReduceMotion || b.W <= 0
	if a.motion.tiles == nil {
		a.motion.tiles = map[string]*tileAnim{}
	}
	var left float64 // the largest distance still to go, in DIPs
	// A move begins half a frame along, as a slide does (slideLead): the
	// key came between frames, so the frame that answers it moves already.
	lead := now.Add(-slideLead)
	for id, t := range target {
		// Shown, if under the zoomed pane: back from zoom, it is no pane
		// new to the tree.
		a.motion.notePane(id)
		an := a.motion.tiles[id]
		f, fresh := from[id]
		switch {
		case an == nil && !fresh:
			an = &tileAnim{cur: t, to: t}
		case an == nil || fresh:
			an = &tileAnim{from: f, cur: f, to: t, start: lead}
		case an.to != t:
			// Retargeted on the way: on from where it shows.
			an.from, an.to, an.start, an.spring = an.cur, t, lead, false
		}
		if l := a.motion.landing; l.pane == id && an.start.Equal(lead) && t != l.at {
			// Dropped: from where its copy was let go, on a spring.
			an.from, an.cur, an.start, an.spring = l.at, l.at, lead, true
			a.motion.landing = paneGhost{}
		}
		a.motion.tiles[id] = an
		left = max(left, an.step(now, instant, ew, eh))
		shownAt[id] = an.cur
	}
	if l := a.motion.landing; l.pane != "" && now.Sub(l.when) > 300*time.Millisecond {
		// Let go where it was: nothing moved it.
		a.motion.landing = paneGhost{}
	}
	for id := range a.motion.tiles {
		if _, ok := target[id]; !ok {
			delete(a.motion.tiles, id)
		}
	}
	moving := left > 0
	if moving {
		c.AnimationFrame()
	} else if a.motion.moved {
		// The last frame of the move: the terminals take their new size
		// in it, not in frames of their own after it.
		releaseLeases()
	}
	a.motion.moved = moving
	if g != nil {
		a.motion.guess = guessShown{g.id, shownAt[g.id]}
	}
	// Zoomed and at rest, only the zoomed pane is built: the others are
	// under it, and drawing them would cost frames for nothing.
	settledZoom := zoomed != "" && shownAt[zoomed] == target[zoomed]

	// Back to front: the panes, the one zoomed last (or the one that was,
	// while it shrinks back over the others).
	var order []string
	if lay != nil && multi {
		order = lay.Panes()
	}
	for id := range target {
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	if top := a.motion.top; top != "" {
		if _, ok := target[top]; ok {
			for i, id := range order {
				if id == top {
					order = append(append(order[:i:i], order[i+1:]...), top)
					break
				}
			}
		}
		if zoomed == "" && !moving {
			a.motion.top = ""
		}
	}
	px := func(f frac) (x, y, w, h float32) {
		round := func(v float64) float64 { return math.Round(v) }
		x0, y0 := round(f.x*ew), round(f.y*eh)
		x1, y1 := round((f.x+f.w)*ew-paneGap), round((f.y+f.h)*eh-paneGap)
		return float32(x0), float32(y0), float32(max(x1-x0, 0)), float32(max(y1-y0, 0))
	}
	const eps = 1e-6
	// The panes under the zoomed one dim as it grows over them, and light
	// up as it shrinks back: one fill, under the zoomed pane.
	// It follows the zoomed pane's own move: no frame of its own after.
	dim := float32(0)
	if top := a.motion.top; top != "" {
		if an := a.motion.tiles[top]; an != nil {
			k := an.progress()
			if zoomed != "" {
				dim = k
			} else {
				dim = 1 - k
			}
		}
	}
	area.Children(func() {
		for _, id := range order {
			p := s.PaneByID(id)
			if g != nil && id == g.id {
				x, y, w, h := px(shownAt[id])
				paned(ui.Column(c.Key("tile-"+id)).Absolute().Left(x).Top(y).Width(w).Height(h).Radius(rPane).Background(pal.card), pal)
				continue
			}
			if p == nil || (settledZoom && id != zoomed) {
				continue
			}
			if id == a.motion.top && dim > 0 && !settledZoom && len(order) > 1 {
				ui.Box(c.Key("zoom-dim")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough().
					Background(pal.overlay.Alpha(min(1, 1.6*dim)))
			}
			t := target[id]
			wrap := ui.Column(c.Key("tile-" + id)).Absolute().AlignItems(ui.Stretch)
			if b.W <= 0 || b.H <= 0 {
				// Before the area has a size: in percent of it, gaps aside.
				wrap.LeftPercent(float32(t.x * 100)).TopPercent(float32(t.y * 100)).WidthPercent(float32(t.w * 100)).HeightPercent(float32(t.h * 100))
			} else {
				x, y, w, h := px(shownAt[id])
				wrap.Left(x).Top(y).Width(w).Height(h)
				a.motion.width[id] = w
			}
			a.motion.placed[id] = true
			top, left := t.y < eps, t.x < eps
			edgeRight := t.x+t.w > 1-eps && right
			wrap.Children(func() {
				a.paneCard(c, pal, bar, shown, s, p, top, left, edgeRight, multi)
			})
		}
		a.dragGhost(c, pal, s, area, shownAt, px)
		if hidden > 0 && settledZoom {
			narrowNote(c, pal, hidden)
		}
		if zoomed != "" || b.W <= 0 {
			return
		}
		for _, sp := range splits {
			x, y, w, h := px(sp.r)
			if sp.l.Row {
				x, w = float32(math.Round(sp.at*ew))-paneGap, paneGap
			} else {
				y, h = float32(math.Round(sp.at*eh))-paneGap, paneGap
			}
			a.divider(c, pal, s, sp.l, sp.key, ui.Rect{X: x, Y: y, W: w, H: h}, float32(sp.r.w*ew), float32(sp.r.h*eh))
		}
	})
}

// tooSmall reports whether a pane of the layout at target, in an area of
// ew x eh DIPs, the gaps included, has fewer cells than the smallest pane
// (minPaneCols by minPaneRows), measured with the cell reportGrid keeps.
func (a *App) tooSmall(target map[string]frac, ew, eh float64) bool {
	cw, ch := float64(a.cell.w), float64(a.cell.h)
	if cw <= 0 || ch <= 0 {
		return false
	}
	for _, f := range target {
		if (f.w*ew-paneGap)/cw < minPaneCols || (f.h*eh-paneGap-headerH)/ch < minPaneRows {
			return true
		}
	}
	return false
}

// narrowNote says, over the bottom of the pane area, how many panes the
// window is too small to show.
func narrowNote(c *ui.Context, pal *palette, hidden int) {
	label := locales.PaneNarrow(hidden)
	ui.Row(c.Key("narrow-note")).Absolute().Bottom(10).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
		ui.Row(c).Height(28).Padding(0, 12, 0, 12).AlignItems(ui.Center).Radius(14).
			Background(pal.popover).Border(1, pal.edgeHigh).Label(label).Children(func() {
			ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
		})
	})
}

// ghostW and ghostH are the largest the copy of a dragged pane shows.
const ghostW, ghostH = 480, 320

// dragGhost draws the copy of the pane being dragged under the pointer:
// the pane scaled to fit 480 x 320 DIPs, at 75 % opacity, as one cached
// bitmap that takes no input. Let go on another pane, the pane flies to
// its new place from where the copy was (tileAnim.spring).
func (a *App) dragGhost(c *ui.Context, pal *palette, s *proto.Session, area ui.Element, shownAt map[string]frac, px func(frac) (x, y, w, h float32)) {
	id := a.motion.dragging
	r, ok := shownAt[id]
	p := s.PaneByID(id)
	if id == "" || !ok || p == nil {
		a.motion.ghost = paneGhost{}
		return
	}
	mx, my, in := area.PointerPosition()
	if !in {
		return
	}
	b := a.paneRef("tiles-" + s.ID).Bounds(c)
	ew, eh := float64(b.W)+paneGap, float64(b.H)+paneGap
	_, _, w, h := px(r)
	sc := min(ghostW/max(w, 1), ghostH/max(h, 1), 1)
	// The pointer holds the copy by its title, as it held the pane. The
	// copy takes no input, so the pane under it takes the drop.
	gx, gy := mx-24, my-12
	cx, cy := gx+sc*w/2, gy+sc*h/2
	ghost := ui.Column(c.Key("ghost")).Absolute().Left(cx-w/2).Top(cy-h/2).Width(w).Height(h).
		Scale(sc, sc).Opacity(0.75).Cache().IgnoreInput(true)
	a.motion.ghost = paneGhost{id, frac{float64(gx) / ew, float64(gy) / eh, (float64(sc*w) + paneGap) / ew, (float64(sc*h) + paneGap) / eh}, c.Now()}
	ghost.Children(func() {
		card := ui.Column(c.Key("card")).Grow(1).Radius(rPane).Clip().Background(pal.card).
			Shadow(0, 14, 36, 0, pal.floatShadow)
		paned(card, pal).Children(func() {
			ui.Row(c).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, 16).Gap(sp2).Children(func() {
				ui.Icon(c, programIcon(p)).Size(14, 14).TextColor(pal.textFaint)
				oneLine(c, p.PaneName(), fsBody, wSemibold).TextColor(pal.text)
			})
			if e := a.terms[id]; e != nil && e.term != nil {
				ui.Box(c).Grow(1).MinHeight(0).Children(func() {
					terminal.View(c.Key("term"), e.term).Fill()
				})
			}
		})
	})
}
