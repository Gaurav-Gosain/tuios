package main

import (
	"maps"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// paneMatch is the identity a pane's card shares across the parents it
// shows in: the split tree, or the window alone while zoomed. A card
// built under the other parent then grows or shrinks from where the last
// frame showed it (ElementTransition.Match).
type paneMatch struct{ id string }

// paneTransition moves a pane's card when the frame builds it under
// another parent than the last frame did: from zoomed to tiled and back,
// or out of a split as the pane beside it closes. It grows or shrinks from
// where it showed (ElementTransition.Match). The terminal inside keeps its
// grid until the move ends (holdLeases), so the pane is resized once, at
// the end. The split tree's panes move by their rectangles instead
// (tiles.go).
func paneTransition(id string) ui.ElementTransition {
	return ui.ElementTransition{Animation: &motionLayout, Colors: true, Match: paneMatch{id}}
}

// leaseHold holds the size leases of the panes while the layout moves. A
// terminal's grid follows its box, and its box changes in every frame of a
// split, zoom or sidebar animation: each change would resize the pane's
// PTY, and the program would redraw a dozen times. Held, the terminal
// keeps its grid until the move ends, and the last size goes out once.
var leaseHold struct {
	mu      sync.Mutex
	until   time.Time
	pending map[*gated]heldAsk
	timer   *time.Timer
	// deferred are asks from a frame of a move, with no end grid to send
	// in their place: they wait for a frame at rest (released). A terminal
	// asks only when its grid changes, so one that asks nothing in that
	// frame is at the size it last asked for, and that goes out then.
	deferred map[*gated][4]int
	// released says the frame a move ends in was built (releaseLeases).
	released bool
}

// heldAsk is a terminal's ask held, and whether a frame at rest made it.
type heldAsk struct {
	l    [4]int
	rest bool
}

// holdLeases holds the panes' size leases for d from now, or longer if a
// hold already runs longer.
func holdLeases(d time.Duration) {
	leaseHold.mu.Lock()
	defer leaseHold.mu.Unlock()
	until := time.Now().Add(d)
	if until.Before(leaseHold.until) {
		return
	}
	leaseHold.until = until
	if leaseHold.timer == nil {
		leaseHold.timer = time.AfterFunc(d, flushLeases)
	} else {
		leaseHold.timer.Reset(d)
	}
}

// releaseLeases ends the hold once the frame the layout's move ends in has
// laid out the terminals: their size in that frame, the final one, is the
// one that goes out. Flushed at once, the hold sent the size of the frame
// before, one column off, and the final frame's size followed as a second
// resize: the shell redrew its prompt twice, and a prompt reflowed between
// the two stayed on the screen twice (QA-011).
func releaseLeases() {
	leaseHold.mu.Lock()
	defer leaseHold.mu.Unlock()
	leaseHold.released = true
	leaseHold.until = time.Now().Add(leaseSettle)
	if leaseHold.timer == nil {
		leaseHold.timer = time.AfterFunc(leaseSettle, flushLeases)
	} else {
		leaseHold.timer.Reset(leaseSettle)
	}
}

// leaseSettle is how long the hold lasts past the frame a move ends in:
// that frame's layout, and so its terminals' size, comes within it.
const leaseSettle = 40 * time.Millisecond

// heldLease keeps the lease of g while a hold runs, and reports whether it
// did.
func heldLease(g *gated, l [4]int, rest bool) bool {
	leaseHold.mu.Lock()
	defer leaseHold.mu.Unlock()
	if !time.Now().Before(leaseHold.until) {
		return false
	}
	if leaseHold.pending == nil {
		leaseHold.pending = map[*gated]heldAsk{}
	}
	leaseHold.pending[g] = heldAsk{l, rest}
	return true
}

// flushLeases sends the leases held, once the hold is over.
func flushLeases() {
	leaseHold.mu.Lock()
	if d := time.Until(leaseHold.until); d > 0 {
		leaseHold.timer.Reset(d)
		leaseHold.mu.Unlock()
		return
	}
	held := leaseHold.pending
	leaseHold.pending = nil
	if leaseHold.deferred == nil {
		leaseHold.deferred = map[*gated][4]int{}
	}
	deferred := leaseHold.deferred
	send := map[*gated][4]int{}
	for g, h := range held {
		delete(deferred, g)
		if h.rest {
			send[g] = h.l
			continue
		}
		// Asked from a frame of the move: the pane's end grid, sent as the
		// move began, stands (end_lease.go). With none known the ask is
		// the move's size, as small as a new pane's first sliver (6
		// columns, 1 row), and with no frame after it (a window behind
		// another, which builds none) a shell drew its prompt there. It
		// waits for a frame at rest instead (gm.md).
		if end, ok := g.endFor(); ok {
			send[g] = end
		} else {
			deferred[g] = h.l
		}
	}
	if leaseHold.released {
		maps.Copy(send, deferred)
		clear(deferred)
		leaseHold.released = false
	}
	leaseHold.mu.Unlock()
	for g, l := range send {
		_ = g.send(l)
	}
}

// ResizePixels asks for the pane at the view's size, unless the layout
// moves: then the last size asked for goes out as the move ends.
func (g *gated) ResizePixels(cols, rows, cellW, cellH int) error {
	noteCell(cellW, cellH)
	l := [4]int{cols, rows, cellW, cellH}
	if heldLease(g, l, !tilesMoving.Load()) {
		return nil
	}
	leaseHold.mu.Lock()
	delete(leaseHold.deferred, g)
	leaseHold.mu.Unlock()
	return g.send(l)
}

// motionWatch notes what lays out the panes shown from frame to frame, and
// which panes came into view.
type motionWatch struct {
	// view is the session and workspace shown, and layout what lays out
	// its panes (layoutSig). A change of layout holds the leases for as
	// long as the animation it starts.
	view, layout string
	// last are the panes the last frame showed, of view, and now those
	// this frame shows so far.
	last, now map[string]bool
	// placed are the panes the tiles place in this frame (tiles.go): they
	// move by their rectangles, not by a Match.
	placed map[string]bool
	// width is each placed pane's width in this frame, in DIPs.
	width map[string]float32
	// tiles are the rectangles of the panes of view, as they move
	// (tiles.go), and moved tells that they moved in the last frame.
	tiles map[string]*tileAnim
	moved bool
	// dragging is the pane whose title the pointer drags in this frame,
	// ghost its copy under the pointer as the last frame drew it, and
	// landing the copy let go on a drop, which the pane flies from.
	dragging string
	ghost    paneGhost
	landing  paneGhost
	// guess is the guessed card of a split (splitGuess) as the last frame
	// showed it.
	guess guessShown
	// top is the pane zoomed, or the one last zoomed while it shrinks
	// back: drawn over the others.
	top string
}

// isNew reports whether the frame shows pane id, of the view the last
// frame showed too, for the first time.
func (m *motionWatch) isNew(id string) bool { return m.last != nil && !m.last[id] }

// watchPanes starts a frame of the panes of view, laid out by layout.
// Another session or workspace shown moves nothing: its panes show at
// once, in place.
func (a *App) watchPanes(view, layout string) {
	m := &a.motion
	if view != m.view {
		m.last, m.top, m.tiles, m.moved = nil, "", nil, false
	} else {
		m.last = m.now
		if layout != m.layout {
			holdLeases(motionLayout.Duration + 40*time.Millisecond)
		}
	}
	m.view, m.layout, m.now, m.placed, m.width = view, layout, map[string]bool{}, map[string]bool{}, map[string]float32{}
	m.dragging = ""
}

// notePane notes that the frame shows pane id.
func (m *motionWatch) notePane(id string) {
	if m.now != nil {
		m.now[id] = true
	}
}

// guessShown is a guessed card and where it showed.
type guessShown struct {
	id string
	at frac
}

// paneGhost is a pane's copy under the pointer as it is dragged: where it
// shows, in fractions of the tiles' area.
type paneGhost struct {
	pane string
	at   frac
	when time.Time
}
