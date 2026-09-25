package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Focusing a window on another workspace switches there first. The switch used to
// pick its own default focus (the workspace's first visible window) and fire the
// focus hooks for it before the caller's real target got focused, so a
// cross-workspace focus fired AfterFocusChange twice and, in scrolling layout,
// scrolled to the wrong pane. The target workspace here holds two windows so the
// switch's default pick differs from the target, exposing the extra event.
func TestCrossWorkspaceFocusFiresFocusHookOnce(t *testing.T) {
	m := hookTestOS(t)
	m.NumWorkspaces = 4
	m.CurrentWorkspace = 1
	m.Windows = []*terminal.Window{
		{ID: "ws1-win", Width: 40, Height: 20, Workspace: 1},
		{ID: "ws2-default", Width: 40, Height: 20, Workspace: 2},
		{ID: "ws2-target", Width: 40, Height: 20, Workspace: 2},
	}
	m.FocusedWindow = 0
	r := record(t, m)

	m.FocusWindow(2) // ws2-target, on another workspace

	if m.CurrentWorkspace != 2 {
		t.Fatalf("CurrentWorkspace = %d, want 2", m.CurrentWorkspace)
	}
	if m.FocusedWindow != 2 {
		t.Fatalf("FocusedWindow = %d, want 2", m.FocusedWindow)
	}
	if ctx := r.only(t, m, hooks.AfterFocusChange); ctx.WindowID != "ws2-target" {
		t.Errorf("AfterFocusChange WindowID = %q, want %q", ctx.WindowID, "ws2-target")
	}
}
