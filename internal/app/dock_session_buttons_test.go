package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// dockSessionOS is an OS with one live pane, which is the plainest dock the
// session controls can ride on.
func dockSessionOS(t testing.TB, width int, daemon bool) *OS {
	t.Helper()
	a := newTestWindow(t, "alpha", 60, 20)
	m := newTestOS(a)
	m.Windows = []*terminal.Window{a}
	a.Workspace = 1
	m.Width, m.Height = width, 40
	m.CurrentWorkspace = 1
	m.FocusedWindow = 0
	if daemon {
		m.IsDaemonSession = true
		m.DaemonClient = &session.TUIClient{}
		m.SessionName = "session-1"
	}
	return m
}
