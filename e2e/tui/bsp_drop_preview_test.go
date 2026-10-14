package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// appearance.drop_preview picks what a BSP drag draws where the pane would
// land (fill, outline, off), and appearance.drop_preview_delay how long the
// pointer rests on one zone before that shows. The board and the gesture are
// the ones in bsp_drop_zone_test.go.
//
// How these could pass wrongly, written down first:
//   - off could hide the highlight by breaking the drop. The off case drops on
//     A's left edge and holds the result to the split.
//   - outline could draw the fill too, or nothing at all. The outline case
//     holds every visible ring cell to the outline and every visible inside
//     cell to the ground it had before the drag.
//   - The delay could be read only as "the zone shows eventually", which an
//     immediate highlight passes too. The delay is long against the frame
//     read right after the move, which must show nothing, and the frame read
//     well past it must show the zone.
//   - The highlight could arrive only with the next event rather than when
//     the delay runs out. Nothing moves between the two frames of the delay
//     case, so a timer that does not ask for a frame leaves the first one up.
//   - A zone left before its delay could still show when the delay runs out,
//     on the zone the pointer is on by then. The left-zone case reads a frame
//     after the first zone's delay ran out and before the second's did.

// previewDelay is long against what one frame read takes, so a frame read
// right after a move cannot be one the delay has already run out for, and
// short enough to wait out.
const previewDelay = 3 * time.Second

// TestDropPreviewOffDrawsNothing carries C to A's left edge with the preview
// off and waits past the default delay: nothing is highlighted. The release
// still splits A.
func TestDropPreviewOffDrawsNothing(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-off", chromeDepths[2].env, "drop_preview = 'off'")
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	time.Sleep(time.Second)
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2b-mid-drag-later")
	d.checkHighlight(t, nil, false)
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
}

// TestDropPreviewOutlineDrawsOnlyTheFrame is the left-edge drop with the
// outline-only preview: the landing rectangle's outline is drawn, and the
// cells inside it keep their ground.
func TestDropPreviewOutlineDrawsOnlyTheFrame(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-outline", chromeDepths[2].env, "drop_preview = 'outline'")
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
	c := got[p.c.ID]
	d.checkPreview(t, &c, false, false)
}

// TestDropPreviewWaitsForItsDelay is the left-edge drop with a long delay.
// Right after the move nothing is highlighted; after the delay, with no
// further input, the landing rectangle is.
func TestDropPreviewWaitsForItsDelay(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-delay", chromeDepths[2].env, "drop_preview_delay = '"+previewDelay.String()+"'")
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	d.checkHighlight(t, nil, false)

	time.Sleep(previewDelay)
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2b-mid-drag-after-delay")
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
	c := got[p.c.ID]
	d.checkHighlight(t, &c, false)
}

// TestADropZoneLeftBeforeItsDelayNeverShows rests C on A's left edge for half
// the delay, then moves it to A's middle. Once the left edge's delay has run
// out, and before the middle's has, nothing is highlighted; once the middle's
// has, all of A is, and the release swaps.
func TestADropZoneLeftBeforeItsDelayNeverShows(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-left", chromeDepths[2].env, "drop_preview_delay = '"+previewDelay.String()+"'")
	// carry returns half a second after the pointer reached the edge.
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	edgeEntered := time.Now().Add(-500 * time.Millisecond)
	time.Sleep(previewDelay/2 - 500*time.Millisecond)

	d.x = p.a.X + p.a.Width/2
	for x := p.a.X + 2; x <= d.x; x++ {
		mouseMotion(t, term, x, d.y, tuitest.MouseLeft, 0)
	}
	middleEntered := time.Now()
	d.checkHeld(t)

	// Halfway between the two delays running out.
	time.Sleep(time.Until(edgeEntered.Add(previewDelay + middleEntered.Sub(edgeEntered)/2)))
	if time.Since(middleEntered) >= previewDelay {
		t.Fatalf("the frame between the two delays was read too late, %v after the middle was entered", time.Since(middleEntered))
	}
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2b-left-edge-delay-over")
	d.checkHighlight(t, nil, false)

	time.Sleep(time.Until(middleEntered.Add(previewDelay + time.Second)))
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2c-middle-delay-over")
	got := d.drop(t, base)
	whole := winRect{ID: p.c.ID, X: p.a.X, Y: p.a.Y, Width: p.a.Width, Height: p.a.Height}
	if got[p.c.ID] != whole {
		t.Errorf("dropped in A's middle, C is %+v, want A's slot %+v", got[p.c.ID], p.a)
	}
	d.checkHighlight(t, &whole, false)
}
