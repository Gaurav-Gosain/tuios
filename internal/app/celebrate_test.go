package app

import (
	"testing"
	"time"
)

// celebrateTestOS is a one pane screen with some text on it, so a burst has
// something to be drawn over and something to leave alone.
func celebrateTestOS(t *testing.T) *OS {
	t.Helper()
	withTheme(t, "catppuccin_mocha")
	win := newTestWindow(t, "celebrate", 70, 22)
	win.WriteOutput([]byte("\x1b[38;2;200;200;200mhello from under the confetti\x1b[0m\r\n"))
	win.MarkContentDirty()
	m := newTestOS(win)
	m.Width, m.Height = 90, 30
	m.Settings.AnimationsEnabled = true
	m.Settings.AnimationsSuppressed = false
	return m
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
