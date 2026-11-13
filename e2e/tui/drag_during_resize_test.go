package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestALeftPressDuringAResizeCancelsTheResize holds the right button down on
// A, which starts a resize, then presses the left button on C's title bar and
// carries the pointer to the top left corner of the board before both buttons
// come up. This is the gesture the nightly FuzzModel run 43367280897 shrank to.
//
// The left press cancels the resize and starts nothing: every pane stays where
// the tree has it, no menu opens, and no two panes overlap. The left press used
// to start a move of C on top of the resize. The release then ended the
// gesture as a resize, which never runs the tiling drop, so C stayed at the
// corner over A.
//
// How this could pass wrongly, written down first:
//   - The right press could miss A and start nothing. The test then checks
//     only a left drag, which drops on A and swaps. The board is checked after
//     the release against the board from before the press, so a swap fails it.
//   - The left press could miss C's title bar. It is placed past the buttons at
//     the left end of the bar, the column carry uses.
//   - The pane could move back only on a later retile. The geometry is read once
//     the left button is up and before the right one is, then held for a while.
func TestALeftPressDuringAResizeCancelsTheResize(t *testing.T) {
	term, base, p := dropBoard(t, "drag-during-resize", chromeDepths[2].env)
	art := artifactDir(t)
	saveArtifact(t, term, art, "1-before")

	rx, ry := p.a.X+5, p.a.Y+8
	if !covers(p.a, rx, ry) {
		t.Fatalf("the right press at (%d,%d) is not on A %+v", rx, ry, p.a)
	}
	mousePress(t, term, rx, ry, tuitest.MouseRight, 0)
	pause(t, "the right press has no mark on screen until it moves", 200*time.Millisecond)

	col, row := p.c.X+10, p.c.Y
	mousePress(t, term, col, row, tuitest.MouseLeft, 0)
	tx, ty := p.box.X, p.box.Y
	steps := max(abs(tx-col), abs(ty-row))
	for i := 1; i <= steps; i++ {
		mouseMotion(t, term, col+(tx-col)*i/steps, row+(ty-row)*i/steps, tuitest.MouseLeft, 0)
	}
	saveArtifact(t, term, art, "2-held")
	mouseRelease(t, term, tx, ty, tuitest.MouseLeft, 0)

	want := map[string]winRect{p.a.ID: p.a, p.b.ID: p.b, p.c.ID: p.c}
	board := func() (bool, string) {
		after := waitForSettledGeometry(t, base, 3)
		var bad []string
		for a := range after {
			if after[a] != want[after[a].ID] {
				bad = append(bad, fmt.Sprintf("pane %s at %+v, want %+v", after[a].ID, after[a], want[after[a].ID]))
			}
			for b := a + 1; b < len(after); b++ {
				if geomOverlap(after[a], after[b]) {
					bad = append(bad, fmt.Sprintf("%+v overlaps %+v", after[a], after[b]))
				}
			}
		}
		return len(bad) == 0, strings.Join(bad, "\n") + "\n" + term.Snapshot()
	}
	waitUntil(t, "the panes are where the tree has them after the left button comes up", uiTimeout, board)
	stayTrue(t, "the panes stay where the tree has them", time.Second, board)

	mouseRelease(t, term, tx, ty, tuitest.MouseRight, 0)
	stayTrue(t, "the right release opens no menu and moves nothing", time.Second, func() (bool, string) {
		if strings.Contains(term.Screen().Text(), "Close pane") {
			return false, "the pane menu is open\n" + term.Snapshot()
		}
		return board()
	})
	saveArtifact(t, term, art, "3-after-release")
	if !cornerAt(term.Screen(), p.c.X, p.c.Y) {
		t.Fatalf("C is not drawn in its slot %+v\n%s", p.c, term.Snapshot())
	}
	alive(t, term, "after a left press during a resize")
}
