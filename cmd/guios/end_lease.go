package main

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// End grids (Mac round 6, a pane split from the CLI). A terminal tells the
// daemon its size only from a built frame: the view lays out its grid and
// asks (MyGo, viewResize). A window that builds no frame, as one occluded
// while an agent splits a pane, left the new pane at the daemon's own
// layout size (18x6); one lone frame, the first of the split's move, left
// it at the move's first width (2 columns), which the lease hold then sent.
// A program that starts there lays out wrong until the person looks.
//
// guios knows the tiles' end boxes without a frame: the layout's fractions
// of the tiles' area. With what a frame at rest taught it (the scale, the
// cell in pixels, the chrome of a card around its terminal), it turns each
// end box into the grid MyGo will lay out, and sends that as soon as the
// box is known: on a state push, and in the first frame of a move. A frame
// at rest still asks for its own grid, and wins.

// leaseMath is what turns a tile's box into a terminal's grid.
type leaseMath struct {
	scale float32
	// inset is a card's box less its terminal's, by the kind of tile
	// (tileKind): the header, and the edges a card keeps by the window's.
	inset map[[3]bool][2]float32
	// area is each session's tiles area, the gaps in (ew, eh), and right
	// whether its tiles reach the window's right edge, as the last frame
	// laid them out.
	area  map[string][2]float64
	right map[string]bool
	// sent is the last set of end grids sent for each session.
	sent map[string]string
	// pending are end grids of panes with no stream yet, sent on attach.
	pending map[string][2]int
	// restBefore says the last frame laid the tiles out at rest, so the
	// terminals' bounds it left are the ones a frame at rest shows.
	restBefore bool
}

// cellPx is the terminals' cell in pixels, as the last ask gave it.
var cellPx struct {
	mu   sync.Mutex
	w, h int
}

// tilesMoving tells the asks made while the tiles move from those of a
// frame at rest (flushLeases). The asks come on the terminals' goroutines.
var tilesMoving atomic.Bool

func noteCell(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	cellPx.mu.Lock()
	cellPx.w, cellPx.h = w, h
	cellPx.mu.Unlock()
}

// tileKind is what a card's chrome depends on: whether it is at the top,
// at the left and at the window's right edge.
func tileKind(t frac, right bool) [3]bool {
	const eps = 1e-6
	return [3]bool{t.y < eps, t.x < eps, t.x+t.w > 1-eps && right}
}

// tileBox is the card's box for t in an area of ew x eh, as tiled places it.
func tileBox(t frac, ew, eh float64) (w, h float32) {
	x0, y0 := math.Round(t.x*ew), math.Round(t.y*eh)
	x1, y1 := math.Round((t.x+t.w)*ew-paneGap), math.Round((t.y+t.h)*eh-paneGap)
	return float32(max(x1-x0, 0)), float32(max(y1-y0, 0))
}

// gridFor is the grid MyGo lays out for a terminal box of w x h DIPs
// (plugins/terminal paint.go, view.layout), and false while what it needs
// is not known.
func (m *leaseMath) gridFor(w, h float32) (cols, rows int, ok bool) {
	cellPx.mu.Lock()
	cw, ch := cellPx.w, cellPx.h
	cellPx.mu.Unlock()
	s := m.scale
	if s <= 0 || cw <= 0 || ch <= 0 || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	round := func(v float32) int { return int(math.Round(float64(v))) }
	ox, oy := round(termPadX*s), round(termPadY*s)
	cols = max((round(w*s)-2*ox)/cw, 1)
	rows = max((round(h*s)-2*oy)/ch, 1)
	return cols, rows, true
}

// learnTiles notes, in a frame at rest, the chrome of each tiled card
// around its terminal, and the tiles' area.
func (a *App) learnTiles(c *ui.Context, sid string, target map[string]frac, ew, eh float64, right bool) {
	m := &a.lease
	if m.area == nil {
		m.area, m.right, m.inset = map[string][2]float64{}, map[string]bool{}, map[[3]bool][2]float32{}
	}
	m.area[sid], m.right[sid] = [2]float64{ew, eh}, right
	for id, t := range target {
		if e := a.terms[id]; e == nil || e.term == nil {
			continue
		}
		tb := a.paneRef(id).Bounds(c)
		w, h := tileBox(t, ew, eh)
		if tb.W <= 0 || tb.H <= 0 || tb.W > w || tb.H > h {
			continue
		}
		m.inset[tileKind(t, right)] = [2]float32{w - tb.W, h - tb.H}
	}
}

// sendEndGrids sends the end grid of each pane of target, laid out in an
// area of ew x eh, once for each set of them.
func (a *App) sendEndGrids(sid string, target map[string]frac, ew, eh float64, right bool) {
	m := &a.lease
	if len(m.inset) == 0 {
		return
	}
	type grid struct {
		id         string
		cols, rows int
	}
	var gs []grid
	for id, t := range target {
		in, ok := m.inset[tileKind(t, right)]
		if !ok {
			for _, v := range m.inset {
				in, ok = v, true
				break
			}
		}
		w, h := tileBox(t, ew, eh)
		cols, rows, known := m.gridFor(w-in[0], h-in[1])
		if !ok || !known {
			return
		}
		gs = append(gs, grid{id, cols, rows})
	}
	slices.SortFunc(gs, func(x, y grid) int {
		if x.id < y.id {
			return -1
		}
		return 1
	})
	sig := fmt.Sprint(gs)
	if m.sent == nil {
		m.sent = map[string]string{}
	}
	if m.sent[sid] == sig {
		return
	}
	m.sent[sid] = sig
	for _, g := range gs {
		a.endLease(g.id, g.cols, g.rows)
	}
}

// endLease sends pane id's end grid, or keeps it for its attach.
func (a *App) endLease(id string, cols, rows int) {
	if e := a.terms[id]; e != nil && e.gate != nil {
		e.gate.sendEnd(cols, rows)
		return
	}
	if a.lease.pending == nil {
		a.lease.pending = map[string][2]int{}
	}
	a.lease.pending[id] = [2]int{cols, rows}
}

// attachedEnd sends the end grid kept for a pane whose stream just came.
func (a *App) attachedEnd(id string, g *gated) {
	if p, ok := a.lease.pending[id]; ok {
		delete(a.lease.pending, id)
		g.sendEnd(p[0], p[1])
	}
}

// stateEndGrids lays out the shown session's tiles from a state, with the
// last frame's area, and sends their end grids: no frame needed.
func (a *App) stateEndGrids(s *proto.Session) {
	m := &a.lease
	if s == nil || s.ID != a.selected {
		return
	}
	area, ok := m.area[s.ID]
	if !ok || s.Zoomed != "" || a.narrowed[s.ID] || a.guesses[s.ID] != nil || a.slides[s.ID] != nil {
		return
	}
	if s.LayoutMode != "" && s.LayoutMode != "bsp" {
		return
	}
	target := map[string]frac{}
	whole := frac{0, 0, 1, 1}
	switch {
	case s.Layout != nil && len(s.Panes) > 1:
		var splits []fracSplit
		layoutFracs(s.Layout, whole, "split-"+s.ID, func(string) bool { return false }, target, map[string]frac{}, &splits)
	case s.FocusPane() != nil:
		target[s.FocusPane().ID] = whole
	}
	for id := range target {
		if s.PaneByID(id) == nil {
			delete(target, id)
		}
	}
	if len(target) > 0 {
		a.sendEndGrids(s.ID, target, area[0], area[1], m.right[s.ID])
	}
}

// endGridFresh is how long an end grid stands in for the asks held while
// the tiles move (flushLeases).
const endGridFresh = 2 * time.Second

// sendEnd sends cols x rows as the pane's end grid, with the cell the
// terminals last asked with.
func (g *gated) sendEnd(cols, rows int) {
	cellPx.mu.Lock()
	cw, ch := cellPx.w, cellPx.h
	cellPx.mu.Unlock()
	l := [4]int{cols, rows, cw, ch}
	g.lmu.Lock()
	g.end, g.endAt = l, time.Now()
	g.lmu.Unlock()
	_ = g.send(l)
}

// endFor is the pane's end grid while it is fresh.
func (g *gated) endFor() ([4]int, bool) {
	g.lmu.Lock()
	defer g.lmu.Unlock()
	return g.end, !g.endAt.IsZero() && time.Since(g.endAt) < endGridFresh
}

// send asks the daemon for l, unless it was the last size asked.
func (g *gated) send(l [4]int) error {
	g.lmu.Lock()
	if g.lastSent == l {
		g.lmu.Unlock()
		return nil
	}
	g.lastSent = l
	g.lmu.Unlock()
	return g.Stream.ResizePixels(l[0], l[1], l[2], l[3])
}
