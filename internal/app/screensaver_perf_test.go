package app

import (
	"strings"
	"testing"
)

// The saver covers every cell the compositor would produce, so composing them
// underneath it is the whole cost of a frame spent on cells nobody sees. It
// ticks at the session's frame rate, which is 240 on a screen that will take
// it, so that was four frames' worth of work in the time it had to draw one and
// the animation ran visibly slow.

// TestTheSaverFrameIsTheWholeFrame pins that the compositor is skipped while
// the saver is running.
func TestTheSaverFrameIsTheWholeFrame(t *testing.T) {
	m := saverPerfOS(t)
	const marker = "SAVERFRAME"
	m.screensaver.active = true
	m.screensaver.frame = marker

	got := m.composeFrame()

	if got != marker {
		t.Errorf("the frame is %d cells of composed screen, want the saver's own frame",
			len(got))
	}
}

// TestASaverWithNoFrameYetStillComposes pins the one case that has to fall
// through: the saver is on but has not produced a frame, and returning an empty
// string there would blank the screen.
func TestASaverWithNoFrameYetStillComposes(t *testing.T) {
	m := saverPerfOS(t)
	m.screensaver.active = true
	m.screensaver.frame = ""

	if got := m.composeFrame(); got == "" {
		t.Error("a saver with no frame yet blanked the screen")
	}
}

// TestTheSaverCostsNothingToDrawIsNotTrueOfTheComposer is the measurement the
// fix is for, as an assertion rather than a benchmark: composing the screen
// under the saver does strictly more work than returning its frame.
func TestTheSaverSkipsTheWorkTheCompositorWouldDo(t *testing.T) {
	m := saverPerfOS(t)
	plain := m.composeFrame()
	if len(plain) == 0 {
		t.Fatal("the composed screen is empty, so this measures nothing")
	}

	m.screensaver.active = true
	m.screensaver.frame = strings.Repeat("x", 10)
	saved := m.composeFrame()

	if len(saved) >= len(plain) {
		t.Errorf("the saver frame is %d bytes and the composed screen is %d: the compositor still ran",
			len(saved), len(plain))
	}
}

// saverPerfOS is a client with one pane, big enough that composing it is real
// work.
func saverPerfOS(t *testing.T) *OS {
	t.Helper()
	win := newTestWindow(t, "saverperf00000000000000000000001", 80, 24)
	win.X, win.Y = 0, 0
	win.Workspace = 1
	win.WriteOutput([]byte("hello from the pane\r\n"))
	m := newTestOS(win)
	m.CurrentWorkspace = 1
	m.Width, m.Height = 100, 30
	return m
}
