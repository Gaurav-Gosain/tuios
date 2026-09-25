package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// dockTabTestOS is an OS with one window per named workspace, wide enough to
// draw a full dock.
func dockTabTestOS(t testing.TB, current int, workspaces ...int) *OS {
	t.Helper()
	wins := make([]*terminal.Window, 0, len(workspaces))
	for i, ws := range workspaces {
		w := newTestWindow(t, "dock-tab-"+strings.Repeat("x", i+1), 60, 20)
		w.Workspace = ws
		wins = append(wins, w)
	}
	m := newTestOS(wins[0])
	m.Windows = wins
	m.Width, m.Height = 160, 40
	m.CurrentWorkspace = current
	return m
}
