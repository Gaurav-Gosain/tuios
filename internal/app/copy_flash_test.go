package app

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// TestAnythingTheUserDoesEndsTheSweep.
//
// The sweep is a short acknowledgement of a copy. Once a key has been pressed
// or a click has landed, the user is no longer looking at what was copied, and
// a sweep left running carried on painting a region whose text had moved
// underneath it. That is what leaving copy mode mid-sweep looked like.
//
// Negative control: without CancelCopyFlash the sweep is still running here
// and this fails.
func TestAnythingTheUserDoesEndsTheSweep(t *testing.T) {
	m := flashOS(t)
	w := selectedWindow()
	m.Windows = []*terminal.Window{w}
	m.NoteCopyFlash(w)

	if !m.CopyFlashActive() {
		t.Fatal("ASSERTION: no sweep is running, so there is nothing to cancel")
	}
	w.ContentDirty = false

	m.CancelCopyFlash()

	if m.CopyFlashActive() {
		t.Error("the sweep survived the thing that should have ended it")
	}
	if !w.ContentDirty {
		t.Error("the pane was not asked for the frame without the sweep in it")
	}
}

// TestTheSweepNeverRepaintsTheText.
//
// The worst fault the sweep had, and the reason it read as an effect behaving
// strangely: it carried the cell's foreground toward the same colour as its
// background, so at the centre of the band the two were equal and the
// characters were gone. Eleven to one down to one to one on a dark theme.
//
// Light passing over text does not repaint the text.
//
// Negative control: setting a foreground in styleFor fails this at the centre
// of the band.
func TestTheSweepNeverRepaintsTheText(t *testing.T) {
	m := flashOS(t)
	ground := lipgloss.Color("#1E1E2E")
	box := copyFlashBox{left: 0, right: 60, top: 10, bottom: 10}
	band := m.copyFlashBandFor(0.5, box)

	centre := int(band.peakColumn(box.top))
	st, lit := band.styleFor(centre, box.top, true, ground)
	if !lit {
		t.Fatal("ASSERTION: the centre of the band is not lit, so there is nothing to check")
	}
	if got := st.Render("x"); strings.Contains(got, "38;2;") {
		t.Errorf("the brightest cell repaints its text: %q", got)
	}
}

// TestTheTintIsDerivedFromTheGround.
//
// One literal cannot serve both ends of the theme range: the pale gold this
// shipped with measures fourteen to one against a dark ground and one point oh
// three against a light one, so it was a strobe on one theme and invisible on
// the other.
//
// Negative control: going back to a fixed colour fails the light theme's floor
// and the dark theme's ceiling at once.
func TestTheTintIsDerivedFromTheGround(t *testing.T) {
	for _, tc := range []struct{ name, bg string }{
		{"a dark theme", "#1E1E2E"},
		{"a light theme", "#FDF6E3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bg := lipgloss.Color(tc.bg)
			tint := overlay.Tone(bg, copyFlashLift)

			got := overlay.ContrastRatio(tint, bg)
			if got < 1.3 {
				t.Errorf("the tint measures %.2f against the ground, too little to see", got)
			}
			if got > copyFlashLift+0.05 {
				t.Errorf("the tint measures %.2f against the ground, louder than the selection", got)
			}
		})
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
