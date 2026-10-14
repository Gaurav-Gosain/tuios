package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Dropping a dragged pane on an edge of another one in BSP splits that pane,
// and the dragged pane takes the half toward the edge. The middle of a pane
// still swaps the two. While the button is down the rectangle the pane would
// land in is highlighted, and the release has to put the pane exactly there.
//
// The board is three panes: A on the left at full height, B over C on the
// right. C is the one dragged, by its title bar near the left end, so the
// held pane hangs to the right of and below the pointer and the highlight has
// cells left to see.
//
// How these could pass wrongly, written down first:
//   - A split could come out as a swap. Every edge case checks the pane that
//     was not involved too: B has to take the full height C left, which no
//     swap gives.
//   - The highlight could be read off the held pane rather than the canvas
//     under it. The held pane's rectangle is worked out from the press and
//     the pointer, checked against the corner drawn there, and every cell it
//     covers is left out.
//   - The highlight could be somewhere other than where the pane lands and
//     still change some cell. The outline is checked cell by cell against the
//     rectangle the daemon reports after the drop: every visible cell of it
//     carries the outline, and no other visible cell does.
//   - A drag that never passes the drag threshold snaps back whatever the
//     zones do. Every drop here is at least twenty cells from its press.

// dropPanes is the board as the daemon has it.
type dropPanes struct {
	a, b, c winRect
	// box is the region the panes tile.
	box winRect
}

// dropBoard starts a daemon session tiled in bsp, with the three panes, and
// returns them. env picks the colour depth.
func dropBoard(t *testing.T, session string, env []string) (*tuitest.Terminal, string, dropPanes) {
	t.Helper()
	return dropBoardWith(t, session, env, "")
}

// dropBoardWith is dropBoard with appearance holding the lines of an
// [appearance] table, for the drop preview's options.
func dropBoardWith(t *testing.T, session string, env []string, appearance string) (*tuitest.Terminal, string, dropPanes) {
	t.Helper()
	base := t.TempDir()
	config := "[startup]\ndaemon = true\ntiled = true\nlayout = 'bsp'\n"
	if appearance != "" {
		config += "\n[appearance]\n" + appearance + "\n"
	}
	writeConfig(t, base, config)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}, env: env})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 3 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 3, "drop board setup")
	return term, base, boardShape(t, term, waitForSettledGeometry(t, base, 3))
}

// boardShape names the three panes of a board, and fails unless they are A
// on the left at full height and B over C on the right.
func boardShape(t *testing.T, term *tuitest.Terminal, rects []winRect) dropPanes {
	t.Helper()
	if len(rects) != 3 {
		t.Fatalf("the board has %d panes, want 3: %+v", len(rects), rects)
	}
	var p dropPanes
	for i, r := range rects {
		if i == 0 || r.X < p.a.X {
			p.a = r
		}
	}
	var right []winRect
	for _, r := range rects {
		if r.ID != p.a.ID {
			right = append(right, r)
		}
	}
	p.b, p.c = right[0], right[1]
	if p.c.Y < p.b.Y {
		p.b, p.c = p.c, p.b
	}
	if p.b.X != p.c.X || p.b.X < p.a.X+p.a.Width || p.c.Y < p.b.Y+p.b.Height ||
		p.a.Y != p.b.Y || p.a.Y+p.a.Height != p.c.Y+p.c.Height {
		t.Fatalf("the board is not A beside B over C: %+v\n%s", rects, term.Snapshot())
	}
	p.box = winRect{X: p.a.X, Y: p.a.Y, Width: p.b.X + p.b.Width - p.a.X, Height: p.a.Height}
	return p
}

// drag is one gesture on C: pressed on its title bar, carried to a point.
type drag struct {
	pane       winRect
	col        int // the column of the title bar pressed
	x, y       int // where the pointer is carried
	held       winRect
	pre, mid   groundCells
	board      dropPanes
	term       *tuitest.Terminal
	artifactAt string
}

// carry presses C's title bar near its left end and carries the pointer to
// (x, y) a cell at a time, leaving the button down. It keeps the frame from
// before the press and the one mid-drag, and checks the held pane is drawn
// where the arithmetic says.
func carry(t *testing.T, term *tuitest.Terminal, p dropPanes, x, y int) *drag {
	t.Helper()
	// Past the buttons at the left end of the title bar.
	d := &drag{pane: p.c, col: p.c.X + 10, x: x, y: y, board: p, term: term, artifactAt: artifactDir(t)}
	time.Sleep(500 * time.Millisecond)
	d.pre = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "1-before")

	mousePress(t, term, d.col, p.c.Y, tuitest.MouseLeft, 0)
	steps := max(abs(x-d.col), abs(y-p.c.Y))
	for i := 1; i <= steps; i++ {
		mouseMotion(t, term, d.col+(x-d.col)*i/steps, p.c.Y+(y-p.c.Y)*i/steps, tuitest.MouseLeft, 0)
	}
	time.Sleep(500 * time.Millisecond)
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2-mid-drag")

	d.checkHeld(t)
	return d
}

// checkHeld works out where the held pane is drawn with the pointer at (d.x,
// d.y), and checks the pane's corner is there. The pane follows the pointer
// with the offset of the press, held inside the content region the way the
// motion path clamps it.
func (d *drag) checkHeld(t *testing.T) {
	t.Helper()
	p := d.board
	h := winRect{X: d.x - (d.col - p.c.X), Y: d.y, Width: p.c.Width, Height: p.c.Height}
	h.X = min(max(h.X, p.box.X-(h.Width-20)), p.box.X+p.box.Width-20)
	h.Y = min(max(h.Y, p.box.Y-(h.Height-3)), p.box.Y+p.box.Height-3)
	d.held = h
	if h.X >= 0 && h.Y >= 0 && !cornerAt(d.term.Screen(), h.X, h.Y) {
		t.Fatalf("the held pane is not drawn at (%d,%d), where the press and the pointer put it\n%s", h.X, h.Y, d.term.Snapshot())
	}
}

// drop releases the button where the pointer is and returns the settled
// geometry by pane.
func (d *drag) drop(t *testing.T, base string) map[string]winRect {
	t.Helper()
	mouseRelease(t, d.term, d.x, d.y, tuitest.MouseLeft, 0)
	time.Sleep(time.Second)
	after := waitForSettledGeometry(t, base, 3)
	saveArtifact(t, d.term, d.artifactAt, "3-after-drop")
	byID := map[string]winRect{}
	for _, r := range after {
		byID[r.ID] = r
	}
	for a := range after {
		for b := a + 1; b < len(after); b++ {
			if geomOverlap(after[a], after[b]) {
				t.Errorf("the drop left two panes overlapping: %+v and %+v", after[a], after[b])
			}
		}
		if r := after[a]; r.X < d.board.box.X || r.Y < d.board.box.Y ||
			r.X+r.Width > d.board.box.X+d.board.box.Width || r.Y+r.Height > d.board.box.Y+d.board.box.Height {
			t.Errorf("the drop left a pane outside the tiled region %+v: %+v", d.board.box, r)
		}
	}
	return byID
}

// visible reports whether the cell is in the tiled region and not under the
// held pane.
func (d *drag) visible(x, y int) bool {
	return covers(d.board.box, x, y) && !covers(d.held, x, y)
}

// checkHighlight holds the mid-drag frame to a highlight on want and nowhere
// else, reading only cells the held pane does not cover. With want nil there
// must be no highlight at all.
//
// In colour the outline is a ground of its own and the inside a tint; at 16
// colours the outline is reverse video.
func (d *drag) checkHighlight(t *testing.T, want *winRect, depth16 bool) {
	t.Helper()
	d.checkPreview(t, want, depth16, true)
}

// checkPreview is checkHighlight for either preview style: with fill false
// (appearance.drop_preview = "outline") the outline is the same and the inside
// has to keep the ground it had before the drag.
func (d *drag) checkPreview(t *testing.T, want *winRect, depth16, fill bool) {
	t.Helper()
	ring := func(x, y int) bool {
		return want != nil && covers(*want, x, y) &&
			(x == want.X || x == want.X+want.Width-1 || y == want.Y || y == want.Y+want.Height-1)
	}
	var ringBg tuitest.Color
	ringSeen, insideSeen := false, false
	if want != nil && !depth16 {
		for y := want.Y; y < want.Y+want.Height && !ringSeen; y++ {
			for x := want.X; x < want.X+want.Width; x++ {
				if ring(x, y) && d.visible(x, y) {
					ringBg, ringSeen = d.mid[y][x].Bg, true
					break
				}
			}
		}
		if !ringSeen {
			t.Fatalf("no cell of the landing rectangle's outline %+v is visible past the held pane %+v; the fixture cannot see the highlight", *want, d.held)
		}
	}
	box := d.board.box
	for y := box.Y; y < box.Y+box.Height; y++ {
		for x := box.X; x < box.X+box.Width; x++ {
			if !d.visible(x, y) {
				continue
			}
			pre, mid := d.pre[y][x], d.mid[y][x]
			inside := want != nil && covers(*want, x, y) && !ring(x, y)
			switch {
			case ring(x, y) && depth16:
				if !mid.Reverse {
					t.Fatalf("outline cell (%d,%d) of the landing rectangle %+v is not reverse video mid-drag\n%s", x, y, *want, d.term.SnapshotStyled())
				}
			case ring(x, y):
				if mid.Bg != ringBg || mid.Bg == pre.Bg {
					t.Fatalf("outline cell (%d,%d) of the landing rectangle %+v has ground %+v (before the drag %+v, the outline's %+v)\n%s",
						x, y, *want, mid.Bg, pre.Bg, ringBg, d.term.SnapshotStyled())
				}
			case inside && !fill:
				insideSeen = true
				if mid.Bg != pre.Bg || mid.Reverse != pre.Reverse {
					t.Fatalf("cell (%d,%d) inside the landing rectangle %+v is painted mid-drag with the outline-only preview: %+v, was %+v\n%s",
						x, y, *want, mid, pre, d.term.SnapshotStyled())
				}
			case inside && !depth16:
				insideSeen = true
				if mid.Bg == pre.Bg || mid.Bg == ringBg {
					t.Fatalf("cell (%d,%d) inside the landing rectangle %+v kept ground %+v mid-drag: no tint\n%s",
						x, y, *want, mid.Bg, d.term.SnapshotStyled())
				}
			case inside:
			case covers(d.pane, x, y):
				// The slot C left is drawn empty while C is away, so it is
				// held to the bare ground rather than to the frame before.
				if mid.Bg.Kind != tuitest.ColorDefault || mid.Reverse {
					t.Fatalf("cell (%d,%d), in the slot C left and outside the landing rectangle, is painted mid-drag: %+v\n%s",
						x, y, mid, d.term.SnapshotStyled())
				}
			case mid.Bg != pre.Bg || mid.Reverse != pre.Reverse:
				t.Fatalf("cell (%d,%d), outside the landing rectangle, changed mid-drag: %+v, was %+v\n%s",
					x, y, mid, pre, d.term.SnapshotStyled())
			}
		}
	}
	if want != nil && !depth16 && !insideSeen {
		t.Fatalf("no cell inside the landing rectangle %+v is visible past the held pane %+v", *want, d.held)
	}
}

// edgeCase is one edge drop and what it has to leave behind.
type edgeCase struct {
	name string
	// at is where C is carried to.
	at func(p dropPanes) (int, int)
	// check holds the settled geometry to the split.
	check func(t *testing.T, p dropPanes, a, b, c winRect)
}

// near reports whether two lengths of a split are as close as an even split
// of an odd length and a divider allow.
func near(x, y int) bool { return abs(x-y) <= 2 }

var edgeCases = []edgeCase{
	{
		name: "left",
		at:   func(p dropPanes) (int, int) { return p.a.X + 1, p.a.Y + p.a.Height/2 },
		check: func(t *testing.T, p dropPanes, a, b, c winRect) {
			if c.X != p.a.X || c.Y != p.a.Y || c.Height != p.a.Height || a.X < c.X+c.Width ||
				a.X+a.Width != p.a.X+p.a.Width || a.Y != p.a.Y || a.Height != p.a.Height || !near(a.Width, c.Width) {
				t.Errorf("dropped on A's left edge: C %+v and A %+v, want them sharing A's slot %+v with C on the left", c, a, p.a)
			}
		},
	},
	{
		name: "right",
		at:   func(p dropPanes) (int, int) { return p.a.X + p.a.Width - 2, p.a.Y + p.a.Height/2 },
		check: func(t *testing.T, p dropPanes, a, b, c winRect) {
			if a.X != p.a.X || a.Y != p.a.Y || a.Height != p.a.Height || c.X < a.X+a.Width ||
				c.X+c.Width != p.a.X+p.a.Width || c.Y != p.a.Y || c.Height != p.a.Height || !near(a.Width, c.Width) {
				t.Errorf("dropped on A's right edge: A %+v and C %+v, want them sharing A's slot %+v with C on the right", a, c, p.a)
			}
		},
	},
	{
		name: "top",
		at:   func(p dropPanes) (int, int) { return p.a.X + p.a.Width/2, p.a.Y + 1 },
		check: func(t *testing.T, p dropPanes, a, b, c winRect) {
			if c.X != p.a.X || c.Y != p.a.Y || c.Width != p.a.Width || a.Y < c.Y+c.Height ||
				a.Y+a.Height != p.a.Y+p.a.Height || a.X != p.a.X || a.Width != p.a.Width || !near(a.Height, c.Height) {
				t.Errorf("dropped on A's top edge: C %+v and A %+v, want them sharing A's slot %+v with C on top", c, a, p.a)
			}
		},
	},
	{
		name: "bottom",
		at:   func(p dropPanes) (int, int) { return p.a.X + p.a.Width/2, p.a.Y + p.a.Height - 2 },
		check: func(t *testing.T, p dropPanes, a, b, c winRect) {
			if a.X != p.a.X || a.Y != p.a.Y || a.Width != p.a.Width || c.Y < a.Y+a.Height ||
				c.Y+c.Height != p.a.Y+p.a.Height || c.X != p.a.X || c.Width != p.a.Width || !near(a.Height, c.Height) {
				t.Errorf("dropped on A's bottom edge: A %+v and C %+v, want them sharing A's slot %+v with C below", a, c, p.a)
			}
		},
	},
	{
		// B is C's own neighbour, so taking C out first grows B to the whole
		// right-hand column, and the split is of that.
		name: "sibling-left",
		at:   func(p dropPanes) (int, int) { return p.b.X + 1, p.b.Y + p.b.Height/2 },
		check: func(t *testing.T, p dropPanes, a, b, c winRect) {
			if a != p.a {
				t.Errorf("dropped on B, A moved: %+v, was %+v", a, p.a)
			}
			if c.X != p.b.X || c.Y != p.b.Y || c.Height != p.a.Height || b.X < c.X+c.Width ||
				b.X+b.Width != p.b.X+p.b.Width || b.Y != p.b.Y || b.Height != p.a.Height || !near(b.Width, c.Width) {
				t.Errorf("dropped on B's left edge: C %+v and B %+v, want them sharing the whole right-hand column with C on the left", c, b)
			}
		},
	},
}

// TestADropOnAPaneEdgeSplitsIt drops C on each edge of A, and on the left edge
// of its own neighbour B. Each drop splits the target, C takes the half toward
// the edge, and when the target is A, B takes the full height C left behind.
// The highlight mid-drag is the rectangle C settles in.
func TestADropOnAPaneEdgeSplitsIt(t *testing.T) {
	for _, tc := range edgeCases {
		t.Run(tc.name, func(t *testing.T) {
			term, base, p := dropBoard(t, "drop-"+tc.name, chromeDepths[2].env)
			x, y := tc.at(p)
			d := carry(t, term, p, x, y)
			got := d.drop(t, base)
			a, b, c := got[p.a.ID], got[p.b.ID], got[p.c.ID]
			tc.check(t, p, a, b, c)
			if tc.name != "sibling-left" && (b.X != p.b.X || b.Width != p.b.Width || b.Y != p.a.Y || b.Height != p.a.Height) {
				t.Errorf("C left the right-hand column and B is %+v; B should have taken the whole column (%d,%d %dx%d)",
					b, p.b.X, p.a.Y, p.b.Width, p.a.Height)
			}
			d.checkHighlight(t, &c, false)
			alive(t, term, "after an edge drop")
		})
	}
}

// TestACentreDropStillSwaps drops C in the middle of A: the two swap, B stays,
// and the highlight mid-drag is all of A.
func TestACentreDropStillSwaps(t *testing.T) {
	term, base, p := dropBoard(t, "drop-centre", chromeDepths[2].env)
	d := carry(t, term, p, p.a.X+p.a.Width/2, p.a.Y+p.a.Height/2)
	got := d.drop(t, base)
	if got[p.c.ID] != (winRect{ID: p.c.ID, X: p.a.X, Y: p.a.Y, Width: p.a.Width, Height: p.a.Height}) {
		t.Errorf("dropped in A's middle, C is %+v, want A's slot %+v", got[p.c.ID], p.a)
	}
	if got[p.a.ID] != (winRect{ID: p.a.ID, X: p.c.X, Y: p.c.Y, Width: p.c.Width, Height: p.c.Height}) {
		t.Errorf("after the swap A is %+v, want C's slot %+v", got[p.a.ID], p.c)
	}
	if got[p.b.ID] != p.b {
		t.Errorf("a swap of A and C moved B: %+v, was %+v", got[p.b.ID], p.b)
	}
	c := got[p.c.ID]
	d.checkHighlight(t, &c, false)
}

// TestADropOnNothingSnapsBack drops C back in its own slot, and on the dock
// row. Neither has a pane to drop on, so nothing is highlighted and nothing
// moves.
func TestADropOnNothingSnapsBack(t *testing.T) {
	for _, where := range []string{"own-slot", "dock"} {
		t.Run(where, func(t *testing.T) {
			term, base, p := dropBoard(t, "drop-none-"+where, chromeDepths[2].env)
			before := []winRect{p.a, p.b, p.c}
			x, y := p.c.X+p.c.Width-4, p.c.Y+p.c.Height/2
			if where == "dock" {
				x, y = p.c.X+p.c.Width/2, p.box.Y+p.box.Height
			}
			d := carry(t, term, p, x, y)
			d.checkHighlight(t, nil, false)
			got := d.drop(t, base)
			for _, r := range before {
				if got[r.ID] != r {
					t.Errorf("a drop on %s moved a pane: %+v, was %+v", where, got[r.ID], r)
				}
			}
		})
	}
}

// TestAShortDragShowsNoDropZone carries C three cells up, into B's bottom
// edge zone but short of the drag threshold, past which a release counts as a
// drop. The release snaps C back there, so the highlight must not promise a
// drop either: nothing is highlighted, and nothing moves.
func TestAShortDragShowsNoDropZone(t *testing.T) {
	term, base, p := dropBoard(t, "drop-short", chromeDepths[2].env)
	before := []winRect{p.a, p.b, p.c}
	d := carry(t, term, p, p.c.X+10, p.c.Y-3)
	d.checkHighlight(t, nil, false)
	got := d.drop(t, base)
	for _, r := range before {
		if got[r.ID] != r {
			t.Errorf("a drag short of the threshold moved a pane: %+v, was %+v", got[r.ID], r)
		}
	}
}

// TestAnEdgeDropThatRebuildsTheSameTreeChangesNothing resizes B over C away
// from even, then drops C on B's bottom edge, which is where C already is.
// Rebuilding the split would put the divider back in the middle, so the drop
// has to snap back: no swap, no reset divider. The highlight says so by
// sitting on C's own slot.
func TestAnEdgeDropThatRebuildsTheSameTreeChangesNothing(t *testing.T) {
	term, base, p := dropBoard(t, "drop-same", chromeDepths[2].env)
	// B's bottom border is the division; dragged up six rows.
	divider := p.b.Y + p.b.Height - 1
	mouseDrag(t, term, p.b.X+p.b.Width/2, divider, p.b.X+p.b.Width/2, divider-6, tuitest.MouseLeft, 0)
	time.Sleep(time.Second)
	resized := waitForSettledGeometry(t, base, 3)
	p = boardShape(t, term, resized)
	if p.c.Height-p.b.Height < 8 {
		t.Fatalf("the divider drag did not move the divider: B %+v, C %+v", p.b, p.c)
	}

	d := carry(t, term, p, p.b.X+p.b.Width/2, p.b.Y+p.b.Height-2)
	got := d.drop(t, base)
	for _, r := range resized {
		if got[r.ID] != r {
			t.Errorf("an edge drop that changes nothing moved a pane: %+v, was %+v", got[r.ID], r)
		}
	}
	c := p.c
	d.checkHighlight(t, &c, false)
}

// TestAnEdgeDropPersistsAndReachesPeers drops C on A's left edge with a second
// client attached. The peer draws the new arrangement, a fresh client attached
// after the first two have gone gets it from the daemon, and a trip to another
// workspace and back keeps it.
func TestAnEdgeDropPersistsAndReachesPeers(t *testing.T) {
	const session = "drop-peer"
	term, base, p := dropBoard(t, session, chromeDepths[2].env)
	peer := attachIn(t, base, session, startOpts{cols: 120, rows: 40, env: chromeDepths[2].env})
	time.Sleep(time.Second)

	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
	after := []winRect{got[p.a.ID], got[p.b.ID], got[p.c.ID]}

	if err := peer.WaitFor(func(s tuitest.Screen) bool {
		for _, r := range after {
			if !cornerAt(s, r.X, r.Y) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Errorf("the peer never drew the panes at their new places %+v: %v\n%s", after, err, peer.Snapshot())
	}

	_ = term.Close()
	_ = peer.Close()
	fresh := attachIn(t, base, session, startOpts{cols: 120, rows: 40})
	saveArtifact(t, fresh, d.artifactAt, "4-reattached")
	if again := waitForSettledGeometry(t, base, 3); !sameGeometry(after, again) {
		t.Errorf("a fresh client laid the session out again:\n after the drop %+v\n reattached     %+v", after, again)
	}

	switchWorkspace(t, fresh, "2", 0)
	switchWorkspace(t, fresh, "1", 3)
	time.Sleep(time.Second)
	saveArtifact(t, fresh, d.artifactAt, "5-workspace-round-trip")
	if again := waitForSettledGeometry(t, base, 3); !sameGeometry(after, again) {
		t.Errorf("a trip to another workspace and back lost the arrangement:\n after the drop %+v\n back           %+v", after, again)
	}
	alive(t, fresh, "after reattaching to an edge-dropped layout")
}

// TestEdgeZonesAreBSPOnly makes the left-edge drop on A in the other two
// layouts, on the same board switched over from BSP so the workspace still
// has a tree a stray split could act on. Nothing is highlighted, and the
// drop does what it does there today: master-stack swaps, the scrolling
// strip swaps columns or snaps back, and no pane is split. BSP is the
// positive half, in TestADropOnAPaneEdgeSplitsIt.
func TestEdgeZonesAreBSPOnly(t *testing.T) {
	layouts := []struct{ query, row, announces string }{
		{"master-stack", "Layout: master-stack", "Layout: master-stack"},
		{"scrolling", "Layout: scrolling (niri-style)", "Layout: scrolling (niri)"},
	}
	for _, l := range layouts {
		t.Run(l.query, func(t *testing.T) {
			term, base, board := dropBoard(t, "drop-only-"+l.query, chromeDepths[2].env)
			runPaletteRow(t, term, l.query, l.row, l.announces)
			waitPaletteClosed(t, term, "after switching layout")
			// The switch's notice is drawn over the panes until it fades, and
			// it would read as a change between the two frames.
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), l.announces)
			}, 3*uiTimeout); err != nil {
				t.Fatalf("the layout notice never went away: %v\n%s", err, term.Snapshot())
			}
			rects := waitForSettledGeometry(t, base, 3)

			if l.query == "master-stack" {
				p := boardShape(t, term, rects)
				d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
				d.checkHighlight(t, nil, false)
				got := d.drop(t, base)
				if got[p.c.ID] != (winRect{ID: p.c.ID, X: p.a.X, Y: p.a.Y, Width: p.a.Width, Height: p.a.Height}) ||
					got[p.a.ID] != (winRect{ID: p.a.ID, X: p.c.X, Y: p.c.Y, Width: p.c.Width, Height: p.c.Height}) {
					t.Errorf("a drop on A's left edge in master-stack did not swap A and C: A %+v, C %+v", got[p.a.ID], got[p.c.ID])
				}
				return
			}

			// The strip is columns, one pane each, every one the full height
			// and wider than a BSP edge zone needs. C, the focused pane, is
			// carried onto another pane one cell in from where that pane
			// starts on screen, which in BSP would be its left edge.
			var held, target winRect
			for _, r := range rects {
				switch {
				case r.ID == board.c.ID:
					held = r
				case r.X+r.Width > 1 && r.X < 120:
					target = r
				}
			}
			if held.X < 0 || held.X+20 > 120 || target.ID == "" {
				t.Fatalf("the strip does not show C and another pane: %+v\n%s", rects, term.Snapshot())
			}
			p := dropPanes{c: held, box: winRect{X: 0, Y: held.Y, Width: 120, Height: held.Height}}
			d := carry(t, term, p, max(target.X, 0)+1, target.Y+target.Height/2)
			d.checkHighlight(t, nil, false)
			mouseRelease(t, term, d.x, d.y, tuitest.MouseLeft, 0)
			time.Sleep(time.Second)
			saveArtifact(t, term, d.artifactAt, "3-after-drop")
			for _, r := range waitForSettledGeometry(t, base, 3) {
				if r.Height != held.Height {
					t.Errorf("a drop on a column's left edge in the strip left a pane %d rows high, want every column full height (%d): %+v",
						r.Height, held.Height, r)
				}
			}
		})
	}
}

// TestTheDropZoneShowsAt16Colours is the left-edge drop on a 16-colour
// terminal, where the tint is no colour at all: the outline has to be there
// in reverse video, on cells that were not reverse before the drag.
func TestTheDropZoneShowsAt16Colours(t *testing.T) {
	term, base, p := dropBoard(t, "drop-16", chromeDepths[0].env)
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	got := d.drop(t, base)
	c := got[p.c.ID]
	plain := 0
	for x := c.X; x < c.X+c.Width; x++ {
		if d.visible(x, c.Y) && !d.pre[c.Y][x].Reverse {
			plain++
		}
	}
	if plain == 0 {
		t.Fatalf("every visible cell of the landing outline's top row %+v was already reverse before the drag; the fixture cannot tell the highlight apart", c)
	}
	d.checkHighlight(t, &c, true)
}

// TestAnEdgeDropResizesTheGuestOnce drags the armed pane of the drag-announce
// board (dragAnnouncePanes) onto the left edge of the full-height pane. Its
// size changes once, so the guest is told once, for the size it settled at.
//
// A swap into the full-height pane's slot is one size change as well, so the
// split is checked too: without it a drop that fell back to the swap passes.
func TestAnEdgeDropResizesTheGuestOnce(t *testing.T) {
	term, base := dragAnnounceBoard(t)
	p := boardShape(t, term, waitForSettledGeometry(t, base, 3))
	if !covers(p.c, 120, 32) {
		t.Fatalf("the press at (120,32) is not on the armed pane C %+v", p.c)
	}
	mouseDrag(t, term, 120, 32, 2, 20, tuitest.MouseLeft, 0)
	time.Sleep(3 * time.Second)
	saveArtifact(t, term, artifactDir(t), "after-drop")
	if n := countRows(term.Screen(), "WINCH"); n != 1 {
		t.Errorf("an edge drop that changed the pane's size announced %d resizes, want 1\n%s", n, term.Snapshot())
	}
	got := map[string]winRect{}
	for _, r := range waitForSettledGeometry(t, base, 3) {
		got[r.ID] = r
	}
	a, b, c := got[p.a.ID], got[p.b.ID], got[p.c.ID]
	edgeCases[0].check(t, p, a, b, c)
	if b.X != p.b.X || b.Width != p.b.Width || b.Y != p.a.Y || b.Height != p.a.Height {
		t.Errorf("C left the right-hand column and B is %+v; B should have taken the whole column (%d,%d %dx%d)",
			b, p.b.X, p.a.Y, p.b.Width, p.a.Height)
	}
	alive(t, term, "after an edge drop")
}
