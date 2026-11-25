package tuie2e

import (
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestATiledDragFollowsTheDragThreshold drags a tiled pane by its title bar
// with mouse.drag_threshold set away from its default, on both sides of it: at
// 12 a drag of 11 cells is put back and one of 12 drops, at 4 a drag of 3
// cells is put back and one of 4 drops. Each pair runs in bsp, master-stack
// and scrolling. In bsp the pane dragged is C of the drop board, carried
// straight up into B, and the drop-zone highlight is read mid-drag as well:
// none while the drag is short, and on the rectangle the pane lands in once it
// is far enough. In master-stack and scrolling the left pane is carried to the
// right along its own title bar into the right pane, and a drop swaps the two.
//
// How this could pass wrongly, written down first:
//   - A gesture that never reached the client would pass every row that is put
//     back. Each such row is paired with the row one cell further on the same
//     board and the same option, which must drop.
//   - An option that is not read behaves as the default 5, and a drag of 11 at
//     12 then drops, and one of 4 at 4 is put back: both rows fail.
//   - A row that is put back could pass because nothing is under the pointer at
//     the release. The paired row drops from the same press on the same line,
//     one cell further, so a pane is under it.
//   - The highlight could lag the frame. The preview's delay is 0 and the
//     screen is settled before the mid-drag frame is read.
//
// Negative controls: NEGATIVE_CONTROLS.md, "A tiled drag follows the drag
// threshold".
func TestATiledDragFollowsTheDragThreshold(t *testing.T) {
	type row struct {
		cells int
		drops bool
	}
	// Each threshold with the drag one cell short of it and the drag that
	// reaches it.
	options := []struct {
		threshold int
		rows      []row
	}{
		{threshold: 12, rows: []row{{cells: 11, drops: false}, {cells: 12, drops: true}}},
		{threshold: 4, rows: []row{{cells: 3, drops: false}, {cells: 4, drops: true}}},
	}

	// bspGesture presses C's title bar past its buttons and carries the
	// pointer straight up, a cell at a time, leaving the button down. It keeps
	// the frame from before the press and the settled one mid-drag, and works
	// out where the held pane is drawn the way checkHeld does.
	bspGesture := func(t *testing.T, term *tuitest.Terminal, p dropPanes, cells int) *drag {
		t.Helper()
		col := p.c.X + 10
		d := &drag{pane: p.c, col: col, x: col, y: p.c.Y - cells, board: p, term: term, artifactAt: artifactDir(t)}
		if err := settleScreen(term, uiTimeout); err != nil {
			t.Fatalf("the screen never settled before the press: %v", err)
		}
		d.pre = readCells(term.Screen())
		saveArtifact(t, term, d.artifactAt, "1-before")
		mousePress(t, term, col, p.c.Y, tuitest.MouseLeft, 0)
		for i := 1; i <= cells; i++ {
			mouseMotion(t, term, col, p.c.Y-i, tuitest.MouseLeft, 0)
		}
		if err := settleScreen(term, uiTimeout); err != nil {
			t.Fatalf("the screen never settled mid-drag: %v", err)
		}
		d.mid = readCells(term.Screen())
		saveArtifact(t, term, d.artifactAt, "2-mid-drag")
		h := winRect{X: d.x - (d.col - p.c.X), Y: d.y, Width: p.c.Width, Height: p.c.Height}
		h.X = min(max(h.X, p.box.X-(h.Width-20)), p.box.X+p.box.Width-20)
		h.Y = min(max(h.Y, p.box.Y-(h.Height-3)), p.box.Y+p.box.Height-3)
		d.held = h
		return d
	}

	// stripBoard starts a daemon session tiled in layout with the threshold
	// set, and two panes side by side, as tiledDragBoard does.
	stripBoard := func(t *testing.T, session, layout string, threshold int) (*tuitest.Terminal, string, winRect, winRect) {
		t.Helper()
		base := t.TempDir()
		writeConfig(t, base, "[startup]\ndaemon = true\ntiled = true\nlayout = '"+layout+"'\n\n[mouse]\ndrag_threshold = "+strconv.Itoa(threshold)+"\n")
		term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
		killDaemon(t, base)
		waitBoot(t, term)
		newWindow(t, term)
		newWindow(t, term)
		waitWindowCount(t, term, 2, "tiled drag threshold setup")
		rects := waitForSettledGeometry(t, base, 2)
		left, right := rects[0], rects[1]
		if right.X < left.X {
			left, right = right, left
		}
		if err := settleScreen(term, uiTimeout); err != nil {
			t.Fatalf("the screen never settled after the setup: %v", err)
		}
		return term, base, left, right
	}

	// stripGesture presses the left pane's title bar three cells from its right
	// end, past the buttons at the left end, carries the pointer cells to the
	// right on the same row a cell at a time, releases there and returns the
	// settled geometry by pane.
	stripGesture := func(t *testing.T, term *tuitest.Terminal, base string, left winRect, cells int) map[string]winRect {
		t.Helper()
		dir := artifactDir(t)
		saveArtifact(t, term, dir, "1-before")
		x, y := left.X+left.Width-3, left.Y
		mousePress(t, term, x, y, tuitest.MouseLeft, 0)
		for i := 1; i <= cells; i++ {
			mouseMotion(t, term, x+i, y, tuitest.MouseLeft, 0)
		}
		mouseRelease(t, term, x+cells, y, tuitest.MouseLeft, 0)
		if err := settleScreen(term, uiTimeout); err != nil {
			t.Fatalf("the screen never settled after the release: %v", err)
		}
		after := waitForSettledGeometry(t, base, 2)
		saveArtifact(t, term, dir, "2-after-release")
		byID := map[string]winRect{}
		for _, r := range after {
			byID[r.ID] = r
		}
		return byID
	}

	for _, layout := range []string{"bsp", "master-stack", "scrolling"} {
		t.Run(layout, func(t *testing.T) {
			for _, o := range options {
				t.Run(strconv.Itoa(o.threshold), func(t *testing.T) {
					for _, r := range o.rows {
						t.Run(strconv.Itoa(r.cells), func(t *testing.T) {
							session := "tdthr-" + layout + "-" + strconv.Itoa(o.threshold) + "-" + strconv.Itoa(r.cells)
							threshold := o.threshold
							if layout == "bsp" {
								term, base, p := dropBoardWith(t, session, chromeDepths[2].env,
									fillPreview+"\ndrop_preview_delay = '0'\n\n[mouse]\ndrag_threshold = "+strconv.Itoa(threshold)+"\n")
								d := bspGesture(t, term, p, r.cells)
								if !r.drops {
									d.checkHighlight(t, nil, false)
									got := d.drop(t, base)
									for _, was := range []winRect{p.a, p.b, p.c} {
										if got[was.ID] != was {
											t.Errorf("a drag of %d cells at drag_threshold %d moved a pane: %+v, was %+v", r.cells, threshold, got[was.ID], was)
										}
									}
								} else {
									got := d.drop(t, base)
									half := p.b.Width / 2
									wantC := winRect{ID: p.c.ID, X: p.b.X, Y: p.box.Y, Width: half, Height: p.box.Height}
									wantB := winRect{ID: p.b.ID, X: p.b.X + half, Y: p.box.Y, Width: half, Height: p.box.Height}
									if got[p.c.ID] != wantC {
										t.Errorf("a drag of %d cells at drag_threshold %d left C at %+v, want %+v", r.cells, threshold, got[p.c.ID], wantC)
									}
									if got[p.b.ID] != wantB {
										t.Errorf("a drag of %d cells at drag_threshold %d left B at %+v, want %+v", r.cells, threshold, got[p.b.ID], wantB)
									}
									if got[p.a.ID] != p.a {
										t.Errorf("a drag of %d cells at drag_threshold %d moved A: %+v, was %+v", r.cells, threshold, got[p.a.ID], p.a)
									}
									c := got[p.c.ID]
									d.checkHighlight(t, &c, false)
								}
								alive(t, term, "after a tiled drag")
								return
							}
							term, base, left, right := stripBoard(t, session, layout, threshold)
							got := stripGesture(t, term, base, left, r.cells)
							wantLeft, wantRight := left, right
							if r.drops {
								wantLeft = winRect{ID: left.ID, X: right.X, Y: right.Y, Width: right.Width, Height: right.Height}
								wantRight = winRect{ID: right.ID, X: left.X, Y: left.Y, Width: left.Width, Height: left.Height}
							}
							if got[left.ID] != wantLeft {
								t.Errorf("%s: a drag of %d cells at drag_threshold %d left the dragged pane at %+v, want %+v (drops: %v)",
									layout, r.cells, threshold, got[left.ID], wantLeft, r.drops)
							}
							if got[right.ID] != wantRight {
								t.Errorf("%s: a drag of %d cells at drag_threshold %d left the other pane at %+v, want %+v (drops: %v)",
									layout, r.cells, threshold, got[right.ID], wantRight, r.drops)
							}
							alive(t, term, "after a tiled drag")
						})
					}
				})
			}
		})
	}
}
