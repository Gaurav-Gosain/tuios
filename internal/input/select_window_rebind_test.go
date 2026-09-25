package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// threePaneOS is three panes on one workspace, with select_window_N moved off
// the digits so the handler cannot read the index back out of the key.
func threePaneOS(t *testing.T, sel map[string][]string) *app.OS {
	t.Helper()
	o := osWithBindings(t, func(kb *config.KeybindingsConfig) {
		for action, keys := range sel {
			kb.WindowManagement[action] = keys
		}
		// The digits are snap_corner_N by default, and layout is consulted after
		// window_management, so leaving them bound would shadow the rebind under
		// test with corner snapping.
		for _, action := range []string{"snap_corner_1", "snap_corner_2", "snap_corner_3", "snap_corner_4"} {
			kb.Layout[action] = nil
		}
	})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.CurrentWorkspace = 1
	o.Windows = []*terminal.Window{
		{ID: "a", X: 0, Y: 0, Width: 40, Height: 30, Workspace: 1},
		{ID: "b", X: 40, Y: 0, Width: 40, Height: 30, Workspace: 1},
		{ID: "c", X: 80, Y: 0, Width: 40, Height: 30, Workspace: 1},
	}
	o.FocusedWindow = 0
	return o
}

// TestSelectWindowWorksOnAReboundKey is the bug in one line: the handler used to
// parse the window index out of msg.String(), so select_window_3 on any key that
// is not the digit 3 focused nothing at all.
func TestSelectWindowWorksOnAReboundKey(t *testing.T) {
	o := threePaneOS(t, map[string][]string{"select_window_3": {"z"}})
	o, _ = HandleWindowManagementModeKey(tea.KeyPressMsg{Code: 'z', Text: "z"}, o)
	if o.FocusedWindow != 2 {
		t.Errorf("select_window_3 on z focused window %d, want 2", o.FocusedWindow)
	}
}

// TestSelectWindowStillWorksOnItsDigit keeps the default path honest.
func TestSelectWindowStillWorksOnItsDigit(t *testing.T) {
	o := threePaneOS(t, map[string][]string{"select_window_2": {"2"}})
	o, _ = HandleWindowManagementModeKey(tea.KeyPressMsg{Code: '2', Text: "2"}, o)
	if o.FocusedWindow != 1 {
		t.Errorf("select_window_2 on 2 focused window %d, want 1", o.FocusedWindow)
	}
}

// TestSelectWindowDoesNotFallBackToCornerSnap pins that the action does one
// thing. It used to corner-snap when the key carried no ctrl and tiling was off,
// so a user who unbound snap_corner_N got corner snapping anyway, from the
// action they had bound in its place.
func TestSelectWindowDoesNotFallBackToCornerSnap(t *testing.T) {
	o := threePaneOS(t, map[string][]string{"select_window_1": {"z"}})
	o.AutoTiling = false
	// Geometry only: Window carries an atomic pointer, so it cannot be copied.
	wantX, wantY := o.Windows[0].X, o.Windows[0].Y
	wantW, wantH := o.Windows[0].Width, o.Windows[0].Height
	o, _ = HandleWindowManagementModeKey(tea.KeyPressMsg{Code: 'z', Text: "z"}, o)
	if got := o.Windows[0]; got.X != wantX || got.Y != wantY ||
		got.Width != wantW || got.Height != wantH {
		t.Errorf("select_window_1 moved the window: %dx%d at %d,%d, want %dx%d at %d,%d",
			got.Width, got.Height, got.X, got.Y, wantW, wantH, wantX, wantY)
	}
	if o.FocusedWindow != 0 {
		t.Errorf("select_window_1 focused window %d, want 0", o.FocusedWindow)
	}
}
