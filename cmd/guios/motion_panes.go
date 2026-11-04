package main

import (
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
	pending map[*gated][4]int
	timer   *time.Timer
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

// releaseLeases ends the hold now and sends the leases held, in the frame
// the layout's move ends.
func releaseLeases() {
	leaseHold.mu.Lock()
	leaseHold.until = time.Now()
	if leaseHold.timer != nil {
		// Its work is done here: no wake-up after.
		leaseHold.timer.Stop()
	}
	leaseHold.mu.Unlock()
	flushLeases()
}

// heldLease keeps the lease of g while a hold runs, and reports whether it
// did.
func heldLease(g *gated, l [4]int) bool {
	leaseHold.mu.Lock()
	defer leaseHold.mu.Unlock()
	if !time.Now().Before(leaseHold.until) {
		return false
	}
	if leaseHold.pending == nil {
		leaseHold.pending = map[*gated][4]int{}
	}
	leaseHold.pending[g] = l
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
	leaseHold.mu.Unlock()
	for g, l := range held {
		_ = g.Stream.ResizePixels(l[0], l[1], l[2], l[3])
	}
}

// ResizePixels asks for the pane at the view's size, unless the layout
// moves: then the last size asked for goes out as the move ends.
func (g *gated) ResizePixels(cols, rows, cellW, cellH int) error {
	if heldLease(g, [4]int{cols, rows, cellW, cellH}) {
		return nil
	}
	return g.Stream.ResizePixels(cols, rows, cellW, cellH)
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
