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

// TestTheSweepStartsAndEndsOffTheBlock.
//
// The light has to arrive from outside the copied text and leave on the other
// side, so the first and last cells are lit on the way past rather than the
// sweep appearing to begin and end inside the block.
//
// Stated about the block rather than about columns, because with a diagonal
// the column a row is lit at depends on which row it is.
func TestTheSweepStartsAndEndsOffTheBlock(t *testing.T) {
	m := flashOS(t)
	box := copyFlashBox{left: 4, right: 40, top: 20, bottom: 25}
	ground := lipgloss.Color("#101010")

	if blockLit(m.copyFlashBandFor(0, box), box, ground) {
		t.Error("the block is lit before the sweep starts")
	}
	if blockLit(m.copyFlashBandFor(1, box), box, ground) {
		t.Error("the block is lit after the sweep has finished")
	}
	if !blockLit(m.copyFlashBandFor(0.5, box), box, ground) {
		t.Error("nothing in the block is lit halfway through the sweep")
	}
}

// TestTheLightFallsOffRatherThanEnding. A hard edge reads as a block sliding
// across the text; the falloff is what makes it read as light passing over it.
func TestTheLightFallsOffRatherThanEnding(t *testing.T) {
	m := flashOS(t)
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 80 - 1, top: 20, bottom: 20})
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
	box := copyFlashBox{left: 0, right: 80 - 1, top: 20, bottom: 25}
	band := m.copyFlashBandFor(0.5, box)

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

	top, bottom := brightest(box.top), brightest(box.top+3)
	if top < 0 || bottom < 0 {
		t.Fatal("ASSERTION: a row is not lit at all, so there is no lean to measure")
	}
	if bottom <= top {
		t.Errorf("the top row is brightest at column %d and three rows down at %d, so the light does not lean", top, bottom)
	}
	if want := top + 3*copyFlashSlope; bottom != want {
		t.Errorf("three rows down is brightest at column %d, want %d for a slope of %d", bottom, want, copyFlashSlope)
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
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 80, top: 20, bottom: 20})

	// A cell the light is nowhere near.
	if _, lit := band.styleFor(80, 0, false, ground); lit {
		t.Error("a cell the light has not reached is painted, so the block is a slab")
	}
	// And one it is on.
	centre := int(band.peakColumn(0))
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
	box := copyFlashBox{left: 4, right: 23, top: 20, bottom: 20}

	// At the halfway point the light has to be inside the block.
	mid := m.copyFlashBandFor(0.5, box)
	lit := false
	for x := box.left; x <= box.right; x++ {
		if mid.intensity(x, box.top) > 0.2 {
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
			if band.intensity(x, box.top) > 0.2 {
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
	if box.rows() != 1 {
		t.Errorf("the block is %d rows, want 1", box.rows())
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
	box := copyFlashBox{left: 10, right: 40, top: 20, bottom: 25}

	for _, shape := range config.CopyFlashStyles {
		t.Run(shape, func(t *testing.T) {
			m := flashOS(t)
			m.Settings.CopyFlashStyle = shape
			ground := lipgloss.Color("#101010")

			// Every cell of the block is lit at some point in the run.
			const steps = 40
			for row := box.top; row <= box.bottom; row++ {
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
	box := copyFlashBox{left: 0, right: 60, top: 20, bottom: 25}

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

	fwdTop, fwdBottom := brightest(config.CopyFlashDiagonal, 20), brightest(config.CopyFlashDiagonal, 25)
	revTop, revBottom := brightest(config.CopyFlashDiagonalReverse, 20), brightest(config.CopyFlashDiagonalReverse, 25)

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
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 60, top: 20, bottom: 23})

	if band.intensity(30, 20) != band.intensity(30, 23) {
		t.Error("a horizontal sweep lights different columns on different rows")
	}
}

// TestAVerticalSweepRunsDownTheRows, so every column of a row is lit together.
func TestAVerticalSweepRunsDownTheRows(t *testing.T) {
	m := flashOS(t)
	m.Settings.CopyFlashStyle = config.CopyFlashVertical
	band := m.copyFlashBandFor(0.5, copyFlashBox{left: 0, right: 60, top: 20, bottom: 25})

	if band.intensity(0, 22) != band.intensity(60, 22) {
		t.Error("a vertical sweep lights a row unevenly")
	}
	// The row the light is on against a row it is not. Comparing the two ends
	// would compare two dark rows, which says nothing.
	near := band.intensity(0, int(band.centre))
	if near <= band.intensity(0, 20) {
		t.Error("a vertical sweep does not travel down the rows")
	}
}

// blockLit reports whether any cell of the block is lit on this frame.
func blockLit(band copyFlashBand, box copyFlashBox, ground color.Color) bool {
	for row := box.top; row <= box.bottom; row++ {
		for x := box.left; x <= box.right; x++ {
			if _, lit := band.styleFor(x, row, false, ground); lit {
				return true
			}
		}
	}
	return false
}

// TestEveryShapeLightsABlockThatIsNotAtTheTopOfThePane.
//
// This is the fault that made three of the four shapes do nothing. The band's
// travel was worked out over rows zero to n, while the cells were drawn at the
// pane's real row numbers. For a block twenty rows down, the diagonal was off
// by twenty times its slope and the light passed to one side of the text, and
// the vertical sweep travelled over rows zero to n and never reached row
// twenty at all. Only the horizontal shape worked, because it is the one with
// no row term, which is exactly what was reported.
//
// One row, because a single-line selection is the case that showed it and the
// case with the least margin for error.
//
// Negative control: measuring the axis range from zero rather than from the
// block's own rows fails every shape here but horizontal.
func TestEveryShapeLightsABlockThatIsNotAtTheTopOfThePane(t *testing.T) {
	ground := lipgloss.Color("#101010")

	for _, shape := range config.CopyFlashStyles {
		t.Run(shape, func(t *testing.T) {
			m := flashOS(t)
			m.Settings.CopyFlashStyle = shape

			// A short selection on one row, a long way down a tall pane.
			box := copyFlashBox{left: 12, right: 30, top: 34, bottom: 34}

			lit := 0
			const steps = 40
			for i := range steps {
				if blockLit(m.copyFlashBandFor(float64(i)/steps, box), box, ground) {
					lit++
				}
			}
			if lit == 0 {
				t.Fatal("the block is never lit, so this shape does nothing")
			}
			if lit < steps/3 {
				t.Errorf("the block is lit in %d of %d frames, so the sweep is mostly off the text", lit, steps)
			}

			// And every cell of it is reached.
			for x := box.left; x <= box.right; x++ {
				ever := false
				for i := range steps {
					if _, on := m.copyFlashBandFor(float64(i)/steps, box).styleFor(x, box.top, false, ground); on {
						ever = true
						break
					}
				}
				if !ever {
					t.Errorf("column %d is never lit", x)
				}
			}
		})
	}
}

// TestTheLightSpreadsItsShadesRatherThanStackingThemAtTheEdge.
//
// Every step of the gradient is a whole cell, so what makes the band read as
// light rather than as a bar with a fringe is how many cells carry a middling
// brightness. A square falloff is steep at the centre and shallow at the edge,
// so it stacks its cells at the two ends. Smoothstep is flat at both ends and
// steepest between them, which moves cells into the middle of the range.
//
// The two curves are compared against each other rather than against a
// number. The gain is real but modest, about forty percent of lit cells
// mid-range against thirty-four, and a floor picked to sit between those two
// figures would be a number chosen to pass rather than a property worth
// holding.
//
// Negative control: returning the square from intensity makes the two counts
// equal and this fails.
func TestTheLightSpreadsItsShadesRatherThanStackingThemAtTheEdge(t *testing.T) {
	m := flashOS(t)
	box := copyFlashBox{left: 0, right: 60, top: 10, bottom: 10}
	band := m.copyFlashBandFor(0.5, box)

	midRange := func(f func(float64) float64) (mid, lit int) {
		for x := box.left; x <= box.right; x++ {
			d := band.position(x, box.top) - band.centre
			if d < 0 {
				d = -d
			}
			if d >= band.reach {
				continue
			}
			v := f(1-d/band.reach) * band.amp
			if v <= 0 {
				continue
			}
			lit++
			if v > 0.25 && v < 0.75 {
				mid++
			}
		}
		return mid, lit
	}

	square := func(t float64) float64 { return t * t }
	_, lit := midRange(square)
	if lit == 0 {
		t.Fatal("ASSERTION: nothing is lit, so there is no gradient to measure")
	}

	// What the band actually draws, against what a square would have drawn
	// over the same cells.
	actual := 0
	for x := box.left; x <= box.right; x++ {
		if v := band.intensity(x, box.top); v > 0.25 && v < 0.75 {
			actual++
		}
	}
	squareMid, _ := midRange(square)

	if actual <= squareMid {
		t.Errorf("the falloff puts %d lit cells mid-range against %d for a square, so it is no smoother",
			actual, squareMid)
	}
}

// TestTheLightIsBrightestInTheMiddleOfItself, whatever the falloff is. The
// shape of the curve is a judgement; this is the part that is not.
func TestTheLightIsBrightestInTheMiddleOfItself(t *testing.T) {
	m := flashOS(t)
	box := copyFlashBox{left: 0, right: 60, top: 10, bottom: 10}
	band := m.copyFlashBandFor(0.5, box)

	// The column, not the centre: for a diagonal the two differ by the row's
	// share of the lean.
	centre := int(band.peakColumn(box.top))
	at := band.intensity(centre, box.top)
	for _, d := range []int{2, 4, 6} {
		if out := band.intensity(centre+d, box.top); out >= at {
			t.Errorf("%d cells from the centre is %.2f against %.2f at it", d, out, at)
		}
	}
}
