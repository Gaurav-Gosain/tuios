package app

import (
	"charm.land/lipgloss/v2"
	"image/color"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The band of light that crosses text after a copy.
//
// Copying is the one gesture in a terminal with no result to look at: the text
// does not change and the selection usually disappears. The sweep puts the
// feedback where the text was.

func flashOS(t *testing.T) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.Settings.CopyFlash = true
	m.Settings.CopyFlashMs = config.CopyFlashMsDefault
	m.Settings.CopyFlashColor = config.DefaultCopyFlashColor
	return m
}

// TestTheSweepCrossesTheWholePaneAndThenStops.
//
// The band starts off one edge and ends off the other, so the first and last
// columns are lit on the way past rather than the light appearing to start and
// stop inside the text.
//
// Negative control: making the span the pane's width rather than the width
// plus two reaches leaves the first column unlit at the start.
func TestTheSweepCrossesTheWholePaneAndThenStops(t *testing.T) {
	m := flashOS(t)
	const width = 80

	// At the very start the light is off the left edge, and the first column
	// is the first thing it reaches.
	begin := m.copyFlashBandFor(0, copyFlashBox{left: 0, right: width - 1, rows: 1})
	if begin.centre >= 0 {
		t.Errorf("the sweep starts at column %.1f, want off the left edge", begin.centre)
	}
	end := m.copyFlashBandFor(1, copyFlashBox{left: 0, right: width - 1, rows: 1})
	if end.centre <= float64(width) {
		t.Errorf("the sweep ends at column %.1f, want past the right edge", end.centre)
	}

	// And in the middle it is lighting the middle.
	mid := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: width - 1, rows: 1})
	if mid.intensity(width/2, 0) <= 0 {
		t.Error("the middle of the sweep does not light the middle of the pane")
	}
}

// TestTheLightFallsOffRatherThanEnding. A hard edge reads as a block sliding
// across the text; the falloff is what makes it read as light passing over it.
func TestTheLightFallsOffRatherThanEnding(t *testing.T) {
	m := flashOS(t)
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 80 - 1, rows: 1})
	centre := int(band.centre)

	at := band.intensity(centre, 0)
	near := band.intensity(centre+int(band.reach/3), 0)
	far := band.intensity(centre+int(band.reach)+1, 0)

	if !(at > near && near > far) {
		t.Errorf("the light does not fall off: centre %.2f, near %.2f, far %.2f", at, near, far)
	}
	if far != 0 {
		t.Errorf("the light reaches past its own reach: %.2f", far)
	}
	if at <= 0 {
		t.Error("the centre of the band is not lit")
	}
}

// TestTheSweepEndsAndIsForgotten, so an idle client holds nothing and asks for
// no frames on its account.
func TestTheSweepEndsAndIsForgotten(t *testing.T) {
	m := flashOS(t)
	m.Settings.CopyFlashMs = 1
	m.copyFlash = &copyFlash{WindowID: "w1", At: time.Now()}

	if !m.CopyFlashActive() {
		t.Fatal("ASSERTION: the sweep is not running, so there is nothing to end")
	}
	time.Sleep(5 * time.Millisecond)

	if m.CopyFlashActive() {
		t.Error("the sweep outlived the time it was given")
	}
	if m.copyFlash != nil {
		t.Error("the finished sweep was not forgotten")
	}
}

// TestTheSweepIsDrawnOnOnePaneOnly. A copy can be made while another pane is
// focused, and the light belongs to the text that was taken.
func TestTheSweepIsDrawnOnOnePaneOnly(t *testing.T) {
	m := flashOS(t)
	m.copyFlash = &copyFlash{WindowID: "w1", At: time.Now()}

	if _, ok := m.copyFlashProgress("w1"); !ok {
		t.Error("the pane the text came from draws no sweep")
	}
	if _, ok := m.copyFlashProgress("w2"); ok {
		t.Error("another pane draws the sweep")
	}
}

// TestTurningItOffRecordsNothing. Zero is a real value for the duration and
// the flag is a flag; neither should leave state behind.
func TestTurningItOffRecordsNothing(t *testing.T) {
	m := flashOS(t)
	m.Settings.CopyFlash = false
	m.NoteCopyFlash(nil)

	if m.copyFlash != nil {
		t.Error("a sweep was recorded with the setting off")
	}
	if m.CopyFlashActive() {
		t.Error("a sweep is running with the setting off")
	}
}

// TestACopyAsksForAFrame.
//
// The sweep was invisible and this is why: a pane's render is cached against
// its content, a copy changes nothing in the pane, so the cached frame was
// returned unchanged for as long as the pane stayed quiet. The light was being
// computed every frame and drawn into a frame nobody looked at.
//
// Negative control: dropping the ContentDirty line from NoteCopyFlash leaves
// the pane clean and this fails.
func TestACopyAsksForAFrame(t *testing.T) {
	m := flashOS(t)
	w := selectedWindow()
	w.ContentDirty = false

	m.NoteCopyFlash(w)

	if !w.ContentDirty {
		t.Error("a copy did not ask the pane for a frame, so the sweep is drawn into a cached one")
	}
	if m.copyFlash == nil {
		t.Fatal("the copy recorded no sweep")
	}
	if m.copyFlash.WindowID != w.ID {
		t.Errorf("the sweep is recorded against %q, want %q", m.copyFlash.WindowID, w.ID)
	}
}

// TestACopyWithNoSelectionRecordsNothing. The region comes from the selection,
// so there is nothing to sweep over without one.
func TestACopyWithNoSelectionRecordsNothing(t *testing.T) {
	m := flashOS(t)
	w := selectedWindow()
	w.CopyMode.State = terminal.CopyModeNormal

	m.NoteCopyFlash(w)

	if m.copyFlash != nil {
		t.Error("a copy with no selection recorded a sweep")
	}
}

// selectedWindow is a pane holding a visual selection, which is what a copy
// takes its region from.
func selectedWindow() *terminal.Window {
	return &terminal.Window{
		ID: "w1", Width: 80, Height: 24,
		CopyMode: &terminal.CopyMode{
			Active:      true,
			State:       terminal.CopyModeVisualChar,
			VisualStart: terminal.Position{X: 0, Y: 0},
			VisualEnd:   terminal.Position{X: 10, Y: 0},
		},
	}
}

// TestTheLightLeans. A vertical band crossing a paragraph looks like a wipe.
// A diagonal one looks like light falling across it, which is the thing worth
// having, and a character grid holds a diagonal exactly when its slope is a
// whole number of columns per row.
//
// Negative control: a slope of zero makes every row light the same column and
// this fails.
func TestTheLightLeans(t *testing.T) {
	m := flashOS(t)
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 80 - 1, rows: 6})

	// The column each row is brightest at, which has to move along as the
	// rows go down.
	brightest := func(row int) int {
		best, at := 0.0, -1
		for x := range 80 {
			if v := band.intensity(x, row); v > best {
				best, at = v, x
			}
		}
		return at
	}

	top, bottom := brightest(0), brightest(3)
	if top < 0 || bottom < 0 {
		t.Fatal("ASSERTION: a row is not lit at all, so there is no lean to measure")
	}
	if bottom <= top {
		t.Errorf("row 0 is brightest at column %d and row 3 at %d, so the light does not lean", top, bottom)
	}
	if want := top + 3*copyFlashSlope; bottom != want {
		t.Errorf("row 3 is brightest at column %d, want %d for a slope of %d", bottom, want, copyFlashSlope)
	}
}

// TestTheLightArrivesAndLeaves. Without an envelope the sweep switches on at
// full strength at one edge and off at the other, which reads as a wipe.
//
// Negative control: returning 1 from copyFlashEnvelope fails both ends here.
func TestTheLightArrivesAndLeaves(t *testing.T) {
	begin := copyFlashEnvelope(0.02)
	middle := copyFlashEnvelope(0.5)
	end := copyFlashEnvelope(0.98)

	if begin >= middle {
		t.Errorf("the sweep starts at %.2f against %.2f in the middle, so it does not arrive", begin, middle)
	}
	if end >= middle {
		t.Errorf("the sweep ends at %.2f against %.2f in the middle, so it does not leave", end, middle)
	}
	if middle < 0.99 {
		t.Errorf("the middle of the sweep is only %.2f bright", middle)
	}
	if copyFlashEnvelope(0) != 0 || copyFlashEnvelope(1) != 0 {
		t.Error("the sweep is lit before it starts or after it ends")
	}
}

// TestOnlyLitCellsArePainted.
//
// The sweep used to paint the whole block in the selection colour for its
// whole run, because the effect it copies keeps its selection. There, the
// selection is still there because the app leaves it; here a copy clears it,
// so painting it back put a block of colour on screen that read as the
// selection having come back. Worse, the last frame of the sweep stayed there
// until the pane changed for some other reason.
//
// Negative control: returning a painted style for an unlit cell fails here.
func TestOnlyLitCellsArePainted(t *testing.T) {
	m := flashOS(t)
	ground := lipgloss.Color("#101010")
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 80, rows: 1})

	// A cell the light is nowhere near.
	if _, lit := band.styleFor(80, 0, false, ground); lit {
		t.Error("a cell the light has not reached is painted, so the block is a slab")
	}
	// And one it is on.
	centre := int(band.centre)
	if _, lit := band.styleFor(centre, 0, false, ground); !lit {
		t.Error("the centre of the band is not painted")
	}
}

// TestTheSweepIsSizedToWhatWasCopied.
//
// The sweep used to cross the pane, so the light was only over the copied text
// for the fraction of the run the block occupied. A twenty column selection on
// a two hundred column pane was lit for about a twentieth of the duration, and
// what reached the screen was a blink.
//
// Negative control: sizing the band to the pane instead of the block puts the
// light outside the block at the halfway point and this fails.
func TestTheSweepIsSizedToWhatWasCopied(t *testing.T) {
	m := flashOS(t)
	// A short block near the left of a wide pane, which is the case that
	// showed the fault.
	box := copyFlashBox{left: 4, right: 23, rows: 1}

	// At the halfway point the light has to be inside the block.
	mid := m.copyFlashBandFor(0.5, box)
	lit := false
	for x := box.left; x <= box.right; x++ {
		if mid.intensity(x, 0) > 0.2 {
			lit = true
		}
	}
	if !lit {
		t.Error("halfway through the sweep, nothing in the copied block is lit")
	}

	// And it has to be lit for most of the run, not a sliver of it.
	runs := 0
	const steps = 20
	for i := range steps {
		band := m.copyFlashBandFor(float64(i)/steps, box)
		for x := box.left; x <= box.right; x++ {
			if band.intensity(x, 0) > 0.2 {
				runs++
				break
			}
		}
	}
	if runs < steps/2 {
		t.Errorf("the block is lit in %d of %d frames, so the sweep is mostly off the text", runs, steps)
	}
}

// TestTheSweepMeasuresTheBlock. The bounds come from the marked cells, so a
// block that is narrower than the pane is swept at its own width.
func TestTheSweepMeasuresTheBlock(t *testing.T) {
	g := pool.GetHighlightGrid()
	defer pool.PutHighlightGrid(g)
	g.Init(4, 60)
	// One row, so the block's bounds are that row's bounds. A selection over
	// several rows reaches column zero on every row after the first, which is
	// what a selection is, so its left edge is zero and says nothing.
	fillPaneRegion(g, terminal.Position{X: 5, Y: 0}, terminal.Position{X: 20, Y: 0}, 0, 0, 4, 60)

	box, ok := copyFlashBoxOf(g, 4, 60)
	if !ok {
		t.Fatal("the marked region measured as nothing")
	}
	if box.left != 5 || box.right != 20 {
		t.Errorf("the block spans columns %d to %d, want 5 to 20", box.left, box.right)
	}
	if box.rows != 1 {
		t.Errorf("the block is %d rows, want 1", box.rows)
	}
}

// TestAnEmptyRegionIsNotSwept, which is what a copied block scrolled out of
// view leaves behind.
func TestAnEmptyRegionIsNotSwept(t *testing.T) {
	g := pool.GetHighlightGrid()
	defer pool.PutHighlightGrid(g)
	g.Init(4, 60)

	if _, ok := copyFlashBoxOf(g, 4, 60); ok {
		t.Error("an empty region measured as a block to sweep")
	}
}

// TestEveryShapeCrossesTheWholeBlock.
//
// Four shapes, one rule: whatever the block is, the light starts off one end
// of it, passes over every part, and leaves off the other. A shape that ran
// out of span would leave part of the block dark, and one whose span was too
// long would spend the run off the text, which is the fault that made the
// sweep look like a blink.
//
// Negative control: taking the axis range from the block's columns for every
// shape leaves the vertical one lit in almost no frames.
func TestEveryShapeCrossesTheWholeBlock(t *testing.T) {
	box := copyFlashBox{left: 10, right: 40, rows: 6}

	for _, shape := range config.CopyFlashStyles {
		t.Run(shape, func(t *testing.T) {
			m := flashOS(t)
			m.Settings.CopyFlashStyle = shape
			ground := lipgloss.Color("#101010")

			// Every cell of the block is lit at some point in the run.
			const steps = 40
			for row := range box.rows {
				for x := box.left; x <= box.right; x++ {
					everLit := false
					for i := range steps {
						band := m.copyFlashBandFor(float64(i)/steps, box)
						if _, lit := band.styleFor(x, row, false, ground); lit {
							everLit = true
							break
						}
					}
					if !everLit {
						t.Fatalf("cell %d,%d is never lit", x, row)
					}
				}
			}

			// And something in the block is lit in most frames, not a
			// handful. Any cell counts: a horizontal sweep lights every row
			// at once and passes a given column in a moment, so asking about
			// one column would say it was mostly dark when it was not.
			runs := 0
			for i := range steps {
				band := m.copyFlashBandFor(float64(i)/steps, box)
				if blockLit(band, box, ground) {
					runs++
				}
			}
			if runs < steps/3 {
				t.Errorf("the block is lit in %d of %d frames", runs, steps)
			}
		})
	}
}

// TestTheTwoDiagonalsLeanOppositeWays, which is the whole difference between
// them and the thing a person picking one is choosing.
func TestTheTwoDiagonalsLeanOppositeWays(t *testing.T) {
	box := copyFlashBox{left: 0, right: 60, rows: 6}

	brightest := func(shape string, row int) int {
		m := flashOS(t)
		m.Settings.CopyFlashStyle = shape
		band := m.copyFlashBandFor(0.5, box)
		best, at := 0.0, -1
		for x := box.left; x <= box.right; x++ {
			if v := band.intensity(x, row); v > best {
				best, at = v, x
			}
		}
		return at
	}

	fwdTop, fwdBottom := brightest(config.CopyFlashDiagonal, 0), brightest(config.CopyFlashDiagonal, 5)
	revTop, revBottom := brightest(config.CopyFlashDiagonalReverse, 0), brightest(config.CopyFlashDiagonalReverse, 5)

	if fwdTop < 0 || fwdBottom < 0 || revTop < 0 || revBottom < 0 {
		t.Fatal("ASSERTION: a row is not lit at the halfway point, so there is no lean to compare")
	}
	if fwdBottom <= fwdTop {
		t.Errorf("the diagonal does not lean forward: top %d, bottom %d", fwdTop, fwdBottom)
	}
	if revBottom >= revTop {
		t.Errorf("the reverse diagonal does not lean back: top %d, bottom %d", revTop, revBottom)
	}
}

// TestAHorizontalSweepDoesNotLean. It is the shape for a single long line,
// where a diagonal barely leans at all over one row.
func TestAHorizontalSweepDoesNotLean(t *testing.T) {
	m := flashOS(t)
	m.Settings.CopyFlashStyle = config.CopyFlashHorizontal
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 60, rows: 4})

	if band.intensity(30, 0) != band.intensity(30, 3) {
		t.Error("a horizontal sweep lights different columns on different rows")
	}
}

// TestAVerticalSweepRunsDownTheRows, so every column of a row is lit together.
func TestAVerticalSweepRunsDownTheRows(t *testing.T) {
	m := flashOS(t)
	m.Settings.CopyFlashStyle = config.CopyFlashVertical
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 60, rows: 6})

	if band.intensity(0, 2) != band.intensity(60, 2) {
		t.Error("a vertical sweep lights a row unevenly")
	}
	// The row the light is on against a row it is not. Comparing the two ends
	// would compare two dark rows, which says nothing.
	near := band.intensity(0, int(band.centre))
	if near <= band.intensity(0, 0) {
		t.Error("a vertical sweep does not travel down the rows")
	}
}

// blockLit reports whether any cell of the block is lit on this frame.
func blockLit(band copyFlashBand, box copyFlashBox, ground color.Color) bool {
	for row := range box.rows {
		for x := box.left; x <= box.right; x++ {
			if _, lit := band.styleFor(x, row, false, ground); lit {
				return true
			}
		}
	}
	return false
}
