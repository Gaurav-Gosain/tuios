package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"image/color"
	"strings"
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

// TestCancellingWhenNothingIsRunningIsSafe, because it is called on every key
// press and every click.
func TestCancellingWhenNothingIsRunningIsSafe(t *testing.T) {
	m := flashOS(t)
	m.CancelCopyFlash()
	if m.CopyFlashActive() {
		t.Error("a sweep appeared from nowhere")
	}
}

// What the fade has to be true of, whatever it looks like.
//
// The effect this replaced swept a band of light across the copied block. Its
// worst fault was measurable rather than a matter of taste: it mixed the text
// toward the same colour as the ground, so at the centre of the band the two
// were equal and the characters were gone. Eleven to one down to one to one.
// Most of what follows exists so that cannot come back.

// paneColours are a theme's ground and text, for the contrast arithmetic.
func paneColours() (bg, fg color.Color) {
	return lipgloss.Color("#1E1E2E"), lipgloss.Color("#CDD6F4")
}

// TestTheFadeNeverTouchesTheText.
//
// Negative control: setting a foreground in styleFor fails here at every step.
func TestTheFadeNeverTouchesTheText(t *testing.T) {
	m := flashOS(t)
	bg, _ := paneColours()

	for step := range copyFlashSteps {
		progress := (float64(step) + 0.5) / copyFlashSteps
		st, lit := m.copyFlashBandFor(progress).styleFor(bg)
		if !lit {
			t.Fatalf("step %d is not drawn at all", step)
		}
		// A style with no foreground renders no foreground sequence.
		if got := st.Render("x"); strings.Contains(got, "38;2;") {
			t.Errorf("step %d sets a foreground: %q", step, got)
		}
	}
}

// TestTheTextStaysReadableThroughTheFade.
//
// The lift is capped against the floor the rest of the interface holds its
// marks to, so a theme with contrast to spare spends the whole ceiling and one
// without spends what it has. Either way the text never drops below the floor.
//
// Negative control: removing the MarkFloor term from copyFlashPeak takes the
// light theme below it.
func TestTheTextStaysReadableThroughTheFade(t *testing.T) {
	for _, tc := range []struct{ name, bg, fg string }{
		{"a dark theme", "#1E1E2E", "#CDD6F4"},
		{"a light theme", "#FDF6E3", "#657B83"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bg := lipgloss.Color(tc.bg)
			fg := lipgloss.Color(tc.fg)
			peak := copyFlashPeak(bg, fg)

			// At the strongest step, which is the whole peak.
			lifted := overlay.MixColors(bg, peak, 1)
			if got := overlay.ContrastRatio(fg, lifted); got < overlay.MarkFloor {
				t.Errorf("text measures %.2f against the peak, below the floor of %.2f",
					got, overlay.MarkFloor)
			}
		})
	}
}

// TestTheFadeIsVisibleOnBothEndsOfTheThemeRange.
//
// This is the test the fixed colour failed. One hex literal measured fourteen
// to one against a dark ground and one point oh three against a light one.
//
// Negative control: going back to a literal peak fails the light theme here.
func TestTheFadeIsVisibleOnBothEndsOfTheThemeRange(t *testing.T) {
	for _, tc := range []struct{ name, bg, fg string }{
		{"a dark theme", "#1E1E2E", "#CDD6F4"},
		{"a light theme", "#FDF6E3", "#657B83"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bg := lipgloss.Color(tc.bg)
			peak := copyFlashPeak(bg, lipgloss.Color(tc.fg))

			got := overlay.ContrastRatio(peak, bg)
			if got < copyFlashFloor {
				t.Errorf("the peak measures %.2f against the ground, too little to see", got)
			}
			if got > copyFlashCeiling+0.01 {
				t.Errorf("the peak measures %.2f against the ground, louder than the selection", got)
			}
		})
	}
}

// TestEveryCellOfTheRegionIsTheSameOnAGivenFrame. The region's shape is the
// message, and a uniform fill is what shows it: a gradient across cells is a
// handful of whole-cell steps, which is banding rather than light.
//
// Negative control: any position term in styleFor fails this.
func TestEveryCellOfTheRegionIsTheSameOnAGivenFrame(t *testing.T) {
	m := flashOS(t)
	bg, _ := paneColours()
	band := m.copyFlashBandFor(0.3)

	first, _ := band.styleFor(bg)
	want := first.Render(" ")
	for range 40 {
		st, _ := band.styleFor(bg)
		if got := st.Render(" "); got != want {
			t.Fatalf("two cells of one frame differ: %q against %q", got, want)
		}
	}
}

// TestTheFadeOnlyEverGetsFainter, and ends at nothing. A step that brightened
// again would read as a second event.
func TestTheFadeOnlyEverGetsFainter(t *testing.T) {
	prev := 2.0
	for step, level := range copyFlashLevels {
		if level >= prev {
			t.Errorf("step %d is %.2f against %.2f before it", step, level, prev)
		}
		prev = level
	}
	if copyFlashStepAt(1) != -1 {
		t.Error("the fade is still drawn once its time is up")
	}
	if copyFlashStepAt(0) != 0 {
		t.Error("the fade does not start at its strongest")
	}
}

// TestOneCopyCostsOneRepaintPerStep. The pane is redrawn on a step change
// rather than on every tick, so the cost is the six frames it shows rather
// than sixty a second for as long as it runs.
func TestOneCopyCostsOneRepaintPerStep(t *testing.T) {
	seen := map[int]bool{}
	for i := range 240 {
		if s := copyFlashStepAt(float64(i) / 240); s >= 0 {
			seen[s] = true
		}
	}
	if len(seen) != copyFlashSteps {
		t.Errorf("the fade shows %d distinct steps, want %d", len(seen), copyFlashSteps)
	}
}
