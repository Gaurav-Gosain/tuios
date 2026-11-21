package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type toldSize struct {
	w, h  int
	calls int
}

func setSharedBorders(m *OS, v bool) {
	m.Settings.SharedBorders = v
	m.applyAppearanceLive(true)
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
