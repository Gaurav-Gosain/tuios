package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// ctxMenuOS builds an OS sized to a screen with one visible window filling the
// left half, one minimized window, and a registry, which between them can reach
// every context menu target.
func ctxMenuOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: 0, Width: max(w/2, 10), Height: max(h-2, 4), Workspace: 1},
		{ID: "b", CustomName: "logs", Width: 20, Height: 10, Workspace: 1, Minimized: true},
	}
	m.CurrentWorkspace, m.FocusedWindow = 1, 0
	return m
}
