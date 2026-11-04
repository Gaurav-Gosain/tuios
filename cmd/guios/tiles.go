package main

import (
	"math"
	"slices"

	"github.com/Gaurav-Gosain/mygo/ui"

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

// tileKey names one value a pane's rectangle animates by.
type tileKey struct {
	pane string
	at   byte // x, y, w or h
}

// tiled draws the panes of s: the split tree, the zoomed pane over it, or
// the focused pane alone without a tree. right tells that the area
// reaches the window's right edge.
func (a *App) tiled(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, right bool) {
	key := "tiles-" + s.ID
	area := ui.Box(c.Key(key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Bind(a.paneRef(key))
	b := a.paneRef(key).Bounds(c)
	ew, eh := float64(b.W)+paneGap, float64(b.H)+paneGap

	multi := len(s.Panes) > 1
	target := map[string]frac{}
	from := map[string]frac{}
	var splits []fracSplit
	whole := frac{0, 0, 1, 1}
	z := s.PaneByID(s.Zoomed)
	if s.Layout != nil && multi {
		layoutFracs(s.Layout, whole, "split-"+s.ID, a.motion.isNew, target, from, &splits)
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

	// The rectangles this frame shows: each value on its way to the
	// target. A pane new to the tree starts in its slot.
	shownAt := map[string]frac{}
	moving := false
	for id, t := range target {
		// Shown, if under the zoomed pane: back from zoom, it is no pane
		// new to the tree.
		a.motion.notePane(id)
		if f, ok := from[id]; ok {
			area.Jump(tileKey{id, 'x'}, float32(f.x))
			area.Jump(tileKey{id, 'y'}, float32(f.y))
			area.Jump(tileKey{id, 'w'}, float32(f.w))
			area.Jump(tileKey{id, 'h'}, float32(f.h))
		}
		r := frac{
			float64(area.AnimateTo(tileKey{id, 'x'}, float32(t.x), motionLayout)),
			float64(area.AnimateTo(tileKey{id, 'y'}, float32(t.y), motionLayout)),
			float64(area.AnimateTo(tileKey{id, 'w'}, float32(t.w), motionLayout)),
			float64(area.AnimateTo(tileKey{id, 'h'}, float32(t.h), motionLayout)),
		}
		shownAt[id] = r
		if r != t {
			moving = true
		}
	}
	// Zoomed and at rest, only the zoomed pane is built: the others are
	// under it, and drawing them would cost frames for nothing.
	settledZoom := zoomed != "" && shownAt[zoomed] == target[zoomed]

	// Back to front: the panes, the one zoomed last (or the one that was,
	// while it shrinks back over the others).
	var order []string
	if s.Layout != nil && multi {
		order = s.Layout.Panes()
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
	dim := area.AnimateTo("zoom-dim", b2f(zoomed != ""), motionLayout)
	area.Children(func() {
		for _, id := range order {
			p := s.PaneByID(id)
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
			}
			a.motion.placed[id] = true
			top, left := t.y < eps, t.x < eps
			edgeRight := t.x+t.w > 1-eps && right
			wrap.Children(func() {
				a.paneCard(c, pal, bar, shown, s, p, top, left, edgeRight, multi)
			})
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
