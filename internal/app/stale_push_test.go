package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A client does not open or close a pane. It asks the daemon to, and the daemon
// says what it did. Between the question and the answer the client does not know
// the session's window set, and that is the whole of these two tests: the
// keystroke that asks pushes state afterwards the way every keystroke does, and
// that push carries the window set as it was.
//
// The daemon reconciles such a push as stale and keeps the rectangles in it,
// because rectangles are the pushing client's to report. Here they are a layout
// for a window set that no longer exists, and it becomes canonical and is
// broadcast to everyone. Nothing downstream catches it: the check that asks
// whether a tiled layout is stale measures the box the panes span, and the panes
// of the older set span it exactly.
//
// What that looks like on screen is a pane at its old size on top of the pane
// that was split out of it.

// tiledPanes is the panes a client is laying out: on this workspace, on screen,
// and in the tiling.
func tiledPanes(m *OS) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		out = append(out, w)
	}
	return out
}

// overlapping names the first pair of tiled panes that share a cell, or "" when
// none do. Two panes on one cell is what the stale layout looks like: the pane
// that was split is still at its old size, and the pane it was split for is
// underneath it.
func overlapping(m *OS) string {
	ps := tiledPanes(m)
	for i := range ps {
		for j := i + 1; j < len(ps); j++ {
			a, b := ps[i], ps[j]
			if a.X < b.X+b.Width && b.X < a.X+a.Width &&
				a.Y < b.Y+b.Height && b.Y < a.Y+a.Height {
				return fmt.Sprintf("%s %s and %s %s",
					shortID(a.PTYID), rectOf(a), shortID(b.PTYID), rectOf(b))
			}
		}
	}
	return ""
}

// sameLayout says how two clients disagree about the panes, or "" when they do
// not. Every client draws its guest in the grid the rectangle leaves, and the
// daemon runs one shell in one grid, so a rectangle two clients read differently
// is two clients drawing a shell at a size only one of them is right about.
func sameLayout(a, b *OS) string {
	pa, pb := tiledPanes(a), tiledPanes(b)
	if len(pa) != len(pb) {
		return fmt.Sprintf("%d panes vs %d panes", len(pa), len(pb))
	}
	for _, wa := range pa {
		wb := winByPTY(b, wa.PTYID)
		if wb == nil {
			return fmt.Sprintf("pane %s is on one client and not the other", shortID(wa.PTYID))
		}
		if wa.X != wb.X || wa.Y != wb.Y || wa.Width != wb.Width || wa.Height != wb.Height {
			return fmt.Sprintf("pane %s: %s vs %s", shortID(wa.PTYID), rectOf(wa), rectOf(wb))
		}
	}
	return ""
}

// guestGridsMatchTheDaemon checks the one thing that is arithmetic rather than
// convention: the grid a client draws a pane's guest in has to be the grid the
// daemon runs that shell at.
func guestGridsMatchTheDaemon(t *testing.T, r *rig, m *OS) {
	t.Helper()
	for _, w := range tiledPanes(m) {
		st, err := r.ctl.GetTerminalState(w.PTYID, -1, 0)
		if err != nil || st == nil {
			t.Fatalf("pane %s: cannot read the daemon's size: %v", shortID(w.PTYID), err)
		}
		if st.Width != w.ContentWidth() || st.Height != w.ContentHeight() {
			t.Fatalf("pane %s: the daemon runs the shell at %dx%d, the client draws it at %dx%d",
				shortID(w.PTYID), st.Width, st.Height, w.ContentWidth(), w.ContentHeight())
		}
	}
}

// TestOpeningAPaneDoesNotPushTheOldLayout is the bug as the user meets it: two
// clients on one session, one of them opens a pane, and the pane that should
// have been split for it stays at its full width on somebody's screen.
//
// NEGATIVE CONTROL: measured. Taking the daemonWindowIntent guard out of
// SyncStateToDaemon - the one thing that declines the pre-mutation snapshot -
// fails it on the overlap, with the pane that was split still holding the whole
// box and the new pane inside it.
func TestOpeningAPaneDoesNotPushTheOldLayout(t *testing.T) {
	r, p, ex := twoClientsOnOneTiledSession(t)

	before := len(tiledPanes(r.m))
	t.Logf("before, A: %s", rects(r.m))
	t.Logf("before, B: %s", rects(p.m))

	// What a keystroke does: ask for the pane, then push, in that order.
	r.m.AddWindow("")
	r.m.SyncStateToDaemon()
	settlePanes(t, r, p, ex, before+1)

	t.Logf("after, A: %s", rects(r.m))
	t.Logf("after, B: %s", rects(p.m))

	if n := len(tiledPanes(r.m)); n != before+1 {
		t.Fatalf("the pane never arrived: %d panes, wanted %d", n, before+1)
	}
	if o := overlapping(r.m); o != "" {
		t.Fatalf("A is drawing two panes on the same cells: %s", o)
	}
	if o := overlapping(p.m); o != "" {
		t.Fatalf("B is drawing two panes on the same cells: %s", o)
	}
	if d := sameLayout(r.m, p.m); d != "" {
		t.Fatalf("the two clients disagree about the layout: %s", d)
	}
	guestGridsMatchTheDaemon(t, r, r.m)
	guestGridsMatchTheDaemon(t, r, p.m)
}

// paneInsideTheSpan is a pane the other panes span the box without. Closing that
// one is what makes the close half of this bug visible: the panes left behind
// hold their pre-close rectangles, and the hole where the closed pane was is
// invisible to a check that only measures the box the panes span, so no client
// retiles it away.
//
// It returns -1 when the layout has no such pane.
func paneInsideTheSpan(m *OS) int {
	ps := tiledPanes(m)
	if len(ps) < 3 {
		return -1
	}
	span := func(skip *terminal.Window) (int, int, int, int) {
		x0, y0, x1, y1 := 1<<30, 1<<30, -1<<30, -1<<30
		for _, w := range ps {
			if w == skip {
				continue
			}
			x0, y0 = min(x0, w.X), min(y0, w.Y)
			x1, y1 = max(x1, w.X+w.Width), max(y1, w.Y+w.Height)
		}
		return x0, y0, x1, y1
	}
	wx0, wy0, wx1, wy1 := span(nil)
	for _, w := range ps {
		if x0, y0, x1, y1 := span(w); x0 == wx0 && y0 == wy0 && x1 == wx1 && y1 == wy1 {
			for i := range m.Windows {
				if m.Windows[i] == w {
					return i
				}
			}
		}
	}
	return -1
}

// TestClosingAPaneDoesNotPushTheOldLayout is the same shape in reverse: the
// keystroke that asks the daemon to close a pane pushes a window set that still
// has the pane in it.
//
// The pane closed is one the others span the box without, because that is the
// case the staleness check cannot see. Close a pane at the edge and the layout
// left behind is short of the box, which the check does catch, and every client
// retiles its way back to agreement.
//
// NEGATIVE CONTROL: measured. Taking the daemonWindowIntent guard out of
// SyncStateToDaemon fails it with the two clients holding the surviving panes at
// different rectangles: one gave the space back and the other kept the layout
// from before the close.
func TestClosingAPaneDoesNotPushTheOldLayout(t *testing.T) {
	r, p, ex := clientsOnOneTiledSession(t, 3, false)

	before := len(tiledPanes(r.m))
	if before != 3 {
		t.Fatalf("the session did not start with three panes: %s", rects(r.m))
	}
	t.Logf("before, A: %s", rects(r.m))
	t.Logf("before, B: %s", rects(p.m))

	i := paneInsideTheSpan(r.m)
	if i < 0 {
		t.Fatalf("no pane the others span the box without: %s", rects(r.m))
	}
	t.Logf("closing %s, which the others span the box without", shortID(r.m.Windows[i].PTYID))
	r.m.DeleteWindow(i)
	r.m.SyncStateToDaemon()
	settlePanes(t, r, p, ex, before-1)

	t.Logf("after, A: %s", rects(r.m))
	t.Logf("after, B: %s", rects(p.m))

	if n := len(tiledPanes(r.m)); n != before-1 {
		t.Fatalf("the pane never went away: %d panes, wanted %d", n, before-1)
	}
	if d := sameLayout(r.m, p.m); d != "" {
		t.Fatalf("the two clients disagree about the layout: %s", d)
	}
	guestGridsMatchTheDaemon(t, r, r.m)
	guestGridsMatchTheDaemon(t, r, p.m)
}

// settlePanes delivers broadcasts until both clients hold want panes and the
// queue is empty. The count is waited for rather than slept past: the daemon
// answers the intent with a state push, and a fixed quiet period can expire
// before it lands.
func settlePanes(t *testing.T, r *rig, p *peer, ex *exchange, want int) {
	t.Helper()
	deadline := time.Now().Add(rigWait)
	for {
		ex.n = 0
		ex.settle(200, 50*time.Millisecond)
		_, queued := ex.take()
		if !queued && len(tiledPanes(r.m)) == want && len(tiledPanes(p.m)) == want {
			// Both clients have the set. Let whatever answer they owe the daemon
			// for placing it come back and be applied before anything is read.
			ex.n = 0
			ex.settle(200, 100*time.Millisecond)
			if _, queued := ex.take(); !queued {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fleet never settled on %d panes:\n A %s\n B %s", want, rects(r.m), rects(p.m))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
