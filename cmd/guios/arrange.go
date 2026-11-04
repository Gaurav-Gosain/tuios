package main

import (
	"errors"
	"math"
	"slices"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// The layouts tuios places by rectangles: master-stack, scrolling and
// floating, and panes floating over the split tree. The client lays them out
// with tuios's own engine in the cells of the session's box
// (internal/guios/client/arrange.go); here the cells are drawn at the pane
// area's scale. A floating pane moves by its header and resizes from its
// edges, and one let go at an edge of the area snaps to a half, a quarter or
// the whole area, as in the TUI.

// floatDrag is a floating pane being moved or resized.
type floatDrag struct {
	session, pane string
	el            string     // the element dragged: the header's grip or gap, or an edge
	edges         int        // 0 moves it; else edgeLeft | edgeRight | edgeTop | edgeBottom
	start         proto.Rect // its cells when the drag began
	dx, dy        float32    // how far the pointer went, in pixels
	px, py        float32    // the pointer in the area, for the snap
}

const (
	edgeLeft = 1 << iota
	edgeRight
	edgeTop
	edgeBottom
)

// masterDrag is the line between the masters and the stack being dragged.
type masterDrag struct {
	session string
	ratio   float64
}

// arranged draws a session's panes at their rectangles: the layout's
// first, then those floating over it, the topmost last.
func (a *App) arranged(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, right bool) {
	ar := s.Arrange
	key := "arrange-" + s.ID
	area := ui.Box(c.Key(key)).Grow(1).Basis(0).MinWidth(0).MinHeight(0).Clip().Bind(a.paneRef(key))
	b := a.paneRef(key).Bounds(c)
	floats := map[string]bool{}
	for _, id := range ar.Float {
		floats[id] = true
	}
	if a.floatHere == nil {
		a.floatHere = map[string]bool{}
	}
	for _, p := range s.Panes {
		a.floatHere[p.ID] = floats[p.ID]
	}
	var order []string
	if s.Layout != nil {
		order = s.Layout.Panes()
	} else {
		for _, p := range s.Panes {
			if !floats[p.ID] {
				order = append(order, p.ID)
			}
		}
	}
	// The focused floating pane is in front, as the TUI raises the
	// window it focuses.
	fl := slices.Clone(ar.Float)
	if f := a.focusOf(s); floats[f] {
		fl = append(slices.DeleteFunc(fl, func(id string) bool { return id == f }), f)
	}
	order = append(order, fl...)
	if a.fdrag.session == s.ID && a.fdrag.pane != "" {
		if x, y, over := area.PointerPosition(); over {
			a.fdrag.px, a.fdrag.py = x, y
		}
	}
	area.Children(func() {
		if s.Layout != nil {
			// The split tree under the panes floating over it.
			a.tiled(c, pal, bar, shown, s, right)
		}
		for _, id := range order {
			p := s.PaneByID(id)
			r, ok := ar.Rects[id]
			if p == nil || (!ok && s.Layout == nil) {
				continue
			}
			if !ok {
				continue // drawn by the tree
			}
			a.arrangedPane(c, pal, bar, shown, s, p, r, b, floats[id], right)
		}
		a.masterLine(c, pal, s, b)
		a.snapPreview(c, pal, s, b)
	})
}

// pxRect is a rectangle of cells in the area's pixels.
func pxRect(box, r proto.Rect, b ui.Rect) (x, y, w, h float32) {
	sx, sy := b.W/float32(max(box.W, 1)), b.H/float32(max(box.H, 1))
	return float32(r.X-box.X) * sx, float32(r.Y-box.Y) * sy, float32(r.W) * sx, float32(r.H) * sy
}

// cellRect is a rectangle of the area's pixels in cells.
func cellRect(box proto.Rect, b ui.Rect, x, y, w, h float32) proto.Rect {
	sx, sy := float32(max(box.W, 1))/max(b.W, 1), float32(max(box.H, 1))/max(b.H, 1)
	round := func(v float32) int { return int(math.Round(float64(v))) }
	return proto.Rect{X: box.X + round(x*sx), Y: box.Y + round(y*sy), W: max(round(w*sx), 1), H: max(round(h*sy), 1)}
}

// arrangedPane is one pane at its rectangle. A tiled pane keeps the gap to
// its neighbours, as the split tree's dividers do; a floating one has a
// shadow and edges to resize it by.
func (a *App) arrangedPane(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, p *proto.Pane, r proto.Rect, b ui.Rect, float, right bool) {
	box := s.Arrange.Box
	x, y, w, h := pxRect(box, r, b)
	d := &a.fdrag
	dragged := d.session == s.ID && d.pane == p.ID
	if dragged {
		x, y, w, h = d.rect(box, b)
	}
	wrap := ui.Column(c.Key("at-" + p.ID)).Absolute().AlignItems(ui.Stretch)
	if b.W <= 0 {
		// Before the area has a size: in percent of it.
		wrap.LeftPercent(float32(r.X-box.X) * 100 / float32(max(box.W, 1))).TopPercent(float32(r.Y-box.Y) * 100 / float32(max(box.H, 1))).
			WidthPercent(float32(r.W) * 100 / float32(max(box.W, 1))).HeightPercent(float32(r.H) * 100 / float32(max(box.H, 1)))
	} else {
		wrap.Left(x).Top(y).Width(w).Height(h)
	}
	if !float {
		// Half the gap on each side a neighbour is on.
		half := float32(paneGap) / 2
		var l, t, rr, bb float32
		if r.X > box.X {
			l = half
		}
		if r.Y > box.Y {
			t = half
		}
		if r.X+r.W < box.X+box.W {
			rr = half
		}
		if r.Y+r.H < box.Y+box.H {
			bb = half
		}
		wrap.Padding(t, rr, bb, l)
	} else {
		wrap.Radius(14).Shadow(0, 1, 3, 0, pal.cardShadow).Shadow(0, 14, 36, 0, pal.floatShadow)
	}
	// A pane the area cuts, as the scrolling strip's are, touches no edge
	// of the window.
	top := r.Y == box.Y && !float
	left := r.X == box.X && !float
	edgeRight := r.X+r.W == box.X+box.W && !float && right
	wrap.Children(func() {
		a.paneCard(c, pal, bar, shown, s, p, top, left, edgeRight, true)
		if float {
			a.resizeEdges(c, s, p, r, b)
		}
	})
}

// rect is the dragged pane's rectangle in the area's pixels now.
func (d *floatDrag) rect(box proto.Rect, b ui.Rect) (x, y, w, h float32) {
	x, y, w, h = pxRect(box, d.start, b)
	if d.edges == 0 {
		return x + d.dx, y + d.dy, w, h
	}
	// The smallest a pane gets, as the TUI's drag keeps it.
	minW := float32(config.DefaultWindowWidth) * b.W / float32(max(box.W, 1))
	minH := float32(config.DefaultWindowHeight) * b.H / float32(max(box.H, 1))
	if d.edges&edgeLeft != 0 {
		nw := max(w-d.dx, minW)
		x, w = x+w-nw, nw
	}
	if d.edges&edgeRight != 0 {
		w = max(w+d.dx, minW)
	}
	if d.edges&edgeTop != 0 {
		nh := max(h-d.dy, minH)
		y, h = y+h-nh, nh
	}
	if d.edges&edgeBottom != 0 {
		h = max(h+d.dy, minH)
	}
	return x, y, w, h
}

// dragFloat follows a floating pane's move or resize by an element's drag,
// and places the pane where it is let go.
func (a *App) dragFloat(c *ui.Context, el string, e ui.Element, s *proto.Session, p *proto.Pane, edges int) bool {
	d := &a.fdrag
	dx, dy, ok := e.Dragged()
	mine := d.session == s.ID && d.pane == p.ID && d.el == el
	switch {
	case ok && (dx != 0 || dy != 0 || mine):
		if !mine {
			r, has := s.Arrange.Rects[p.ID]
			if !has {
				return false
			}
			*d = floatDrag{session: s.ID, pane: p.ID, el: el, edges: edges, start: r}
			a.setFocus(s, p.ID)
		}
		d.dx += dx
		d.dy += dy
		return true
	case mine && !e.Pressed():
		b := a.paneRef("arrange-" + s.ID).Bounds(c)
		box := s.Arrange.Box
		x, y, w, h := d.rect(box, b)
		cells := cellRect(box, b, x, y, w, h)
		params := proto.ArrangeParams{Session: s.ID, Op: "rect", Pane: p.ID, Rect: &cells}
		if edges == 0 {
			// The TUI snaps by where the pointer lets go.
			at := cellRect(box, b, d.px, d.py, 0, 0)
			params.At = &at
		}
		// Shown where it was let go until the state that has it comes,
		// kept on screen as the client keeps it.
		if edges == 0 {
			r := layout.IntoView(layout.Rect{X: cells.X, Y: cells.Y, W: cells.W, H: cells.H}, layout.Rect{X: box.X, Y: box.Y, W: box.W, H: box.H})
			cells.X, cells.Y = r.X, r.Y
		}
		s.Arrange.Rects[p.ID] = cells
		if r := snapped(s, params); r != nil {
			s.Arrange.Rects[p.ID] = *r
		}
		*d = floatDrag{}
		a.call(s.ID, "arrange", params, nil)
		return true
	}
	return mine
}

// snapped is the rectangle a drop at params.At snaps to, nil for none.
func snapped(s *proto.Session, params proto.ArrangeParams) *proto.Rect {
	if params.At == nil {
		return nil
	}
	box := s.Arrange.Box
	region := layout.Rect{X: box.X, Y: box.Y, W: box.W, H: box.H}
	q := layout.SnapZoneAt(region, params.At.X, params.At.Y)
	if q == layout.NoSnap {
		return nil
	}
	r := layout.SnapBounds(region, q)
	return &proto.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
}

// resizeEdges are a floating pane's edges and corners, which resize it.
func (a *App) resizeEdges(c *ui.Context, s *proto.Session, p *proto.Pane, r proto.Rect, b ui.Rect) {
	const grip = 8
	for _, e := range []struct {
		key    string
		edges  int
		cursor ui.Cursor
	}{
		{"e-l", edgeLeft, ui.CursorResizeEW}, {"e-r", edgeRight, ui.CursorResizeEW},
		{"e-t", edgeTop, ui.CursorResizeNS}, {"e-b", edgeBottom, ui.CursorResizeNS},
		{"e-tl", edgeTop | edgeLeft, ui.CursorResizeNWSE}, {"e-br", edgeBottom | edgeRight, ui.CursorResizeNWSE},
		{"e-tr", edgeTop | edgeRight, ui.CursorResizeNESW}, {"e-bl", edgeBottom | edgeLeft, ui.CursorResizeNESW},
	} {
		h := ui.Box(c.Key(e.key)).Absolute().Cursor(e.cursor).KeepFocus()
		corner := e.edges&(edgeLeft|edgeRight) != 0 && e.edges&(edgeTop|edgeBottom) != 0
		switch {
		case corner:
			h.Width(grip * 2).Height(grip * 2)
		case e.edges&(edgeLeft|edgeRight) != 0:
			h.Width(grip).Top(grip * 2).Bottom(grip * 2)
		default:
			h.Height(grip).Left(grip * 2).Right(grip * 2)
		}
		if e.edges&edgeLeft != 0 {
			h.Left(-grip / 2)
		}
		if e.edges&edgeRight != 0 {
			h.Right(-grip / 2)
		}
		if e.edges&edgeTop != 0 {
			h.Top(-grip / 2)
		}
		if e.edges&edgeBottom != 0 {
			h.Bottom(-grip / 2)
		}
		a.dragFloat(c, e.key, h, s, p, e.edges)
	}
}

// snapPreview shows, while a floating pane is moved to an edge, the half,
// quarter or whole area it snaps to.
func (a *App) snapPreview(c *ui.Context, pal *palette, s *proto.Session, b ui.Rect) {
	d := a.fdrag
	if d.session != s.ID || d.pane == "" || d.edges != 0 || b.W <= 0 {
		return
	}
	box := s.Arrange.Box
	at := cellRect(box, b, d.px, d.py, 0, 0)
	r := snapped(s, proto.ArrangeParams{At: &at})
	if r == nil {
		return
	}
	x, y, w, h := pxRect(box, *r, b)
	ui.Box(c.Key("snap")).Absolute().PassThrough().Left(x+4).Top(y+4).Width(max(w-8, 0)).Height(max(h-8, 0)).Radius(14).
		Background(pal.accent.Alpha(0.10)).InsetShadow(0, 0, 28, 0, pal.accent.Alpha(0.25))
}

// masterLine is the line between the masters and the stack, which a drag
// moves: the masters' share changes when it is let go.
func (a *App) masterLine(c *ui.Context, pal *palette, s *proto.Session, b ui.Rect) {
	ml := s.Arrange.Master
	if ml == nil || b.W <= 0 {
		return
	}
	box := s.Arrange.Box
	size, cells, origin := b.W, box.W, box.X
	if !ml.Vertical {
		size, cells, origin = b.H, box.H, box.Y
	}
	share := float64(ml.At-origin) / float64(max(cells, 1))
	md := &a.mdrag
	if md.session == s.ID {
		share = md.ratio
	}
	pos := float32(share) * size
	line := ui.Box(c.Key("master-line")).Absolute().KeepFocus()
	if ml.Vertical {
		line.Left(pos - paneGap/2).Top(0).Bottom(0).Width(paneGap).Cursor(ui.CursorResizeColumn)
	} else {
		line.Top(pos - paneGap/2).Left(0).Right(0).Height(paneGap).Cursor(ui.CursorResizeRow)
	}
	held := md.session == s.ID
	if line.Hovered() || line.Pressed() || held {
		line.Draw(func(p *ui.Painter, r ui.Rect) {
			if held {
				// Where the masters will end when it is let go.
				if ml.Vertical {
					p.Fill(ui.Rect{X: r.X + r.W/2 - 1, Y: r.Y, W: 2, H: r.H}, pal.accent.Alpha(0.8), 1)
				} else {
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H/2 - 1, W: r.W, H: 2}, pal.accent.Alpha(0.8), 1)
				}
			}
			if ml.Vertical {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, pal.textFaint.Alpha(0.7), 1.5)
			} else {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 18, Y: r.Y + r.H/2 - 1.5, W: 36, H: 3}, pal.textFaint.Alpha(0.7), 1.5)
			}
		})
	}
	if dx, dy, ok := line.Dragged(); ok {
		if md.session != s.ID {
			md.session, md.ratio = s.ID, share
		}
		delta := dx
		if !ml.Vertical {
			delta = dy
		}
		md.ratio = min(max(md.ratio+float64(delta/size), layout.MinSplitRatio), layout.MaxSplitRatio)
	} else if md.session == s.ID && !line.Pressed() {
		// The masters' share, from their own side of the line.
		ratio := md.ratio
		if !ml.Before {
			ratio = 1 - ratio
		}
		md.session = ""
		a.call(s.ID, "arrange", proto.ArrangeParams{Session: s.ID, Op: "master.ratio", Ratio: ratio}, nil)
	}
}

// arrangedNeighbour is the pane beside the focused one toward dir, by the
// rectangles, as tuios's directional focus finds it.
func arrangedNeighbour(s *proto.Session, from, dir string) string {
	ar := s.Arrange
	side, ok := layout.ParseSide(dir)
	if ar == nil || !ok {
		return ""
	}
	fr, ok := ar.Rects[from]
	if !ok {
		return ""
	}
	var ids []string
	var cands []layout.Rect
	for _, p := range s.Panes {
		if r, ok := ar.Rects[p.ID]; ok && p.ID != from {
			ids = append(ids, p.ID)
			cands = append(cands, layout.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H})
		}
	}
	if i := layout.Neighbour(layout.Rect{X: fr.X, Y: fr.Y, W: fr.W, H: fr.H}, cands, side, false); i >= 0 {
		return ids[i]
	}
	return ""
}

// paneOrder is a session's panes in the order its layout takes them.
func paneOrder(s *proto.Session) []string {
	if s.Layout != nil && s.Arrange == nil {
		return s.Layout.Panes()
	}
	ids := make([]string, 0, len(s.Panes))
	for _, p := range s.Panes {
		ids = append(ids, p.ID)
	}
	return ids
}

// arrange asks the session's host for a change to the shown layout. A
// change that moved the focus, as the strip's focus actions do, puts the
// keyboard there.
func (a *App) arrange(s *proto.Session, op string, set func(*proto.ArrangeParams)) {
	if s == nil {
		return
	}
	p := proto.ArrangeParams{Session: s.ID, Op: op, Pane: a.focusOf(s)}
	if set != nil {
		set(&p)
	}
	cl := a.hostOf(s.ID).cl
	if cl == nil {
		return
	}
	sid := s.ID
	go func() {
		var res proto.ArrangeResult
		err := cl.Call("arrange", p, &res)
		a.update(func() {
			if errors.Is(err, client.ErrTooSmall) {
				a.toast(locales.ToastLayoutTooSmall())
				return
			}
			if err != nil {
				a.toast(err.Error())
				return
			}
			if s := a.session(sid); s != nil && res.Focus != "" && s.PaneByID(res.Focus) != nil {
				if a.focus == nil {
					a.focus = map[string]string{}
				}
				a.focus[sid] = res.Focus
				s.Focus = res.Focus
				a.paneRef(res.Focus).Focus()
				a.wantFocus = res.Focus
			}
		})
	}()
}
