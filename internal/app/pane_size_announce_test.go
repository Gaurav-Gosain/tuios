package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// These tests pin the pane size invariant: the width and height the guest is
// told (the PTY winsize, captured from DaemonResizeFunc and mirrored by
// AnnouncedSize) must equal the emulator's own grid and the drawable box the
// renderer clips to (ContentWidth/ContentHeight). A guest told one more column
// than it can draw in wraps its prompt; told one less, it letterboxes. The
// invariant is checked across every bordered/borderless, sidebar, and zoom
// combination, and again after each layout transition.

// toldSize is the last size the fake daemon PTY was told, standing in for the
// real PTY's winsize.
type toldSize struct {
	w, h  int
	calls int
}

func newAnnounceWindow(t testing.TB, id string, w, h int) (*terminal.Window, *toldSize) {
	t.Helper()
	win := newTestWindow(t, id, w, h)
	told := &toldSize{}
	win.DaemonResizeFunc = func(rw, rh int) error {
		told.w, told.h = rw, rh
		told.calls++
		return nil
	}
	return win, told
}

// setSharedBorders flips the setting the way the settings panel and the command
// palette do, so the test exercises whatever those paths do about it.
func setSharedBorders(m *OS, v bool) {
	m.Settings.SharedBorders = v
	m.applyAppearanceLive(true)
}
