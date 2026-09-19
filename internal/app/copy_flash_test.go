package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	begin := m.copyFlashBandFor(0, width)
	if begin.centre >= 0 {
		t.Errorf("the sweep starts at column %.1f, want off the left edge", begin.centre)
	}
	end := m.copyFlashBandFor(1, width)
	if end.centre <= float64(width) {
		t.Errorf("the sweep ends at column %.1f, want past the right edge", end.centre)
	}

	// And in the middle it is lighting the middle.
	mid := m.copyFlashBandFor(0.5, width)
	if mid.intensity(width/2) <= 0 {
		t.Error("the middle of the sweep does not light the middle of the pane")
	}
}

// TestTheLightFallsOffRatherThanEnding. A hard edge reads as a block sliding
// across the text; the falloff is what makes it read as light passing over it.
func TestTheLightFallsOffRatherThanEnding(t *testing.T) {
	m := flashOS(t)
	band := m.copyFlashBandFor(0.5, 80)
	centre := int(band.centre)

	at := band.intensity(centre)
	near := band.intensity(centre + int(band.reach/3))
	far := band.intensity(centre + int(band.reach) + 1)

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
