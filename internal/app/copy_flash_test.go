package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	begin := m.copyFlashBandFor(0, width, 1)
	if begin.centre >= 0 {
		t.Errorf("the sweep starts at column %.1f, want off the left edge", begin.centre)
	}
	end := m.copyFlashBandFor(1, width, 1)
	if end.centre <= float64(width) {
		t.Errorf("the sweep ends at column %.1f, want past the right edge", end.centre)
	}

	// And in the middle it is lighting the middle.
	mid := m.copyFlashBandFor(0.5, width, 1)
	if mid.intensity(width/2, 0) <= 0 {
		t.Error("the middle of the sweep does not light the middle of the pane")
	}
}

// TestTheLightFallsOffRatherThanEnding. A hard edge reads as a block sliding
// across the text; the falloff is what makes it read as light passing over it.
func TestTheLightFallsOffRatherThanEnding(t *testing.T) {
	m := flashOS(t)
	band := m.copyFlashBandFor(0.5, 80, 1)
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
	band := m.copyFlashBandFor(0.5, 80, 6)

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

// TestTheSelectionShowsUnderTheSweep. A copy clears the selection, and light
// crossing nothing reads as a glitch rather than as an acknowledgement of what
// was taken, so the block is painted for as long as the sweep runs.
func TestTheSelectionShowsUnderTheSweep(t *testing.T) {
	m := flashOS(t)
	band := m.copyFlashBandFor(0.5, 80, 1)

	// A cell far from the light is still part of the block.
	if _, lit := band.styleFor(79, 0, false); !lit {
		t.Error("a cell outside the light is not painted, so the block disappears under the sweep")
	}
	// And once the sweep is over, nothing is painted.
	done := m.copyFlashBandFor(1, 80, 1)
	if _, lit := done.styleFor(40, 0, false); lit {
		t.Error("the block is still painted after the sweep has finished")
	}
}
