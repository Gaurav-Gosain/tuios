package app

import (
	"testing"
)

// TestTheSaverFrameIsTheWholeFrame pins that the compositor is skipped while
// the saver is running.
func TestTheSaverFrameIsTheWholeFrame(t *testing.T) {
	m := saverPerfOS(t)
	const marker = "SAVERFRAME"
	m.screensaver.active = true
	m.screensaver.frame = marker

	got := string(m.composeView())

	if got != marker {
		t.Errorf("the frame is %d cells of composed screen, want the saver's own frame",
			len(got))
	}
}

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

// TestASaverWithNoFrameYetStillComposes pins the one case that has to fall
// through: the saver is on but has not produced a frame, and returning an empty
// string there would blank the screen.
func TestASaverWithNoFrameYetStillComposes(t *testing.T) {
	m := saverPerfOS(t)
	m.screensaver.active = true
	m.screensaver.frame = ""

	if got := string(m.composeView()); got == "" {
		t.Error("a saver with no frame yet blanked the screen")
	}
}
