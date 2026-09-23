package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// celebrateTestOS is a one pane screen with some text on it, so a burst has
// something to be drawn over and something to leave alone.
func celebrateTestOS(t *testing.T) *OS {
	t.Helper()
	withTheme(t, "catppuccin_mocha")
	withTrueColorFrames(t)
	win := newTestWindow(t, "celebrate", 70, 22)
	win.WriteOutput([]byte("\x1b[38;2;200;200;200mhello from under the confetti\x1b[0m\r\n"))
	win.MarkContentDirty()
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.Settings.AnimationsEnabled = true
	m.Settings.AnimationsSuppressed = false
	return m
}

// celebrateGlyphCount counts the particle glyphs a frame contains.
func celebrateGlyphCount(frame string) int {
	n := 0
	for _, g := range []string{"✦", "✧", "•", "▪", "▫", "▘", "▝", "▗", "▖", "▀", "▐", "▄", "▌"} {
		n += strings.Count(frame, g)
	}
	return n
}

// TestCelebrationDrawsOverTheFrameAndLeavesItWhole is the property the feature
// rests on: particles are drawn over the composed frame while the burst runs,
// and the first frame after it ends is byte for byte the frame from before.
func TestCelebrationDrawsOverTheFrameAndLeavesItWhole(t *testing.T) {
	m := celebrateTestOS(t)
	before := m.composeFrame()
	if celebrateGlyphCount(before) != 0 {
		t.Fatal("the frame has confetti glyphs in it before any burst")
	}

	cmd := m.Celebrate(CelebrateOptions{})
	if cmd == nil {
		t.Fatal("a burst scheduled no frame, so it would never animate or end")
	}
	start := m.celebration.at

	// A quarter second in, every particle is born and in flight.
	if next := m.handleCelebrateFrame(start.Add(250 * time.Millisecond)); next == nil {
		t.Fatal("a burst still in flight stopped asking for frames")
	}
	mid := m.composeFrame()
	if got := celebrateGlyphCount(mid); got < 10 {
		t.Errorf("mid-burst frame has %d particle glyphs, want at least 10", got)
	}
	if mid == before {
		t.Error("the frame did not change while the burst was running")
	}

	// Past every particle's life the burst is over.
	if next := m.handleCelebrateFrame(start.Add(3 * time.Second)); next != nil {
		t.Error("a finished burst scheduled another frame")
	}
	if m.celebration.active() {
		t.Fatal("the burst is still active after every particle's life")
	}
	after := m.composeFrame()
	if after != before {
		t.Error("the frame after the burst differs from the frame before it")
	}
}

// TestCelebrationIsFreeWhenIdle. With no burst running nothing schedules a
// frame, a stray frame message schedules nothing, and the fast path is not
// taken away.
func TestCelebrationIsFreeWhenIdle(t *testing.T) {
	m := celebrateTestOS(t)
	if m.celebration.active() {
		t.Fatal("a new OS has a celebration running")
	}
	if cmd := m.handleCelebrateFrame(time.Now()); cmd != nil {
		t.Error("a frame message with no burst running scheduled another frame")
	}
	_, cmd := m.Update(celebrateFrameMsg{at: time.Now()})
	if cmd != nil {
		t.Error("Update scheduled work for a stray celebration frame")
	}
	if m.celebration.ticking {
		t.Error("an idle celebration holds a timer")
	}
}

// TestCelebrationBurstsShareOneTimer. A second burst fired while the first is
// running joins it instead of starting a second chain of frames, which would
// double the frame rate.
func TestCelebrationBurstsShareOneTimer(t *testing.T) {
	m := celebrateTestOS(t)
	if m.Celebrate(CelebrateOptions{}) == nil {
		t.Fatal("the first burst scheduled no frame")
	}
	if cmd := m.Celebrate(CelebrateOptions{Big: true}); cmd != nil {
		t.Error("a second burst started a second timer")
	}
}

// TestCelebrationStillWhenAnimationsAreOff. With animations disabled the burst
// is a sparkle that does not move: one timer to clear it, not a frame rate.
func TestCelebrationStillWhenAnimationsAreOff(t *testing.T) {
	m := celebrateTestOS(t)
	m.Settings.AnimationsEnabled = false
	before := m.composeFrame()
	if m.Celebrate(CelebrateOptions{}) == nil {
		t.Fatal("the sparkle scheduled nothing to clear it")
	}
	if m.celebration.moving() {
		t.Error("a celebration with animations off has moving particles")
	}
	if got := celebrateGlyphCount(m.composeFrame()); got == 0 {
		t.Error("the still sparkle drew nothing")
	}
	if m.handleCelebrateFrame(m.celebration.at.Add(time.Second)) != nil {
		t.Error("the sparkle scheduled a frame after it was cleared")
	}
	if m.composeFrame() != before {
		t.Error("the frame after the sparkle differs from the frame before it")
	}
}

// TestCelebrationTakesTheFastPathAway. A lone fullscreen pane skips the
// compositor, and the burst is drawn over the compositor's canvas.
func TestCelebrationTakesTheFastPathAway(t *testing.T) {
	win := newTestWindow(t, "celebrate-fast", 80, 24)
	m := newTestOS(win)
	m.Width, m.Height = 80, 26
	win.X, win.Y = 0, m.GetTopMargin()
	win.Width, win.Height = m.GetRenderWidth(), m.GetUsableHeight()
	if _, ok := m.fullscreenFastWindow(); !ok {
		t.Fatal("the geometry is not on the fast path, so this proves nothing")
	}
	var _ tea.Cmd = m.Celebrate(CelebrateOptions{})
	if _, ok := m.fullscreenFastWindow(); ok {
		t.Error("the fast path stayed eligible during a burst, so it would not be drawn")
	}
}
