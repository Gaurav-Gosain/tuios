package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// railFromMode enters the rail the way a user does, through the leader chord, so
// the entry travels the same routing the bubbletea Update loop uses. It leaves
// the second of two panes focused.
func railFromMode(t *testing.T, mode app.Mode) *app.OS {
	t.Helper()
	withSidebarGlobals(t, "left")
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Width, o.Height = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "w1", X: 0, Y: 0, Width: 60, Height: 39, Workspace: o.CurrentWorkspace},
		{ID: "w2", X: 60, Y: 0, Width: 60, Height: 39, Workspace: o.CurrentWorkspace},
	}
	o.FocusedWindow = 1
	o.Mode = mode

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
	o, _ = HandleKeyPress(press("e"), o)
	if !o.SidebarFocused {
		t.Fatalf("ctrl+b e did not focus the rail from mode %v", mode)
	}
	return o
}

// TestEscFromRailReturnsToTheModeAndPaneItCameFrom is the rail's half of the
// bargain: it borrows the keyboard, so esc hands back both the mode and the pane
// the user left, not window management and whatever the rail last touched.
func TestEscFromRailReturnsToTheModeAndPaneItCameFrom(t *testing.T) {
	for _, mode := range []app.Mode{app.TerminalMode, app.WindowManagementMode} {
		t.Run(map[app.Mode]string{app.TerminalMode: "terminal", app.WindowManagementMode: "window"}[mode], func(t *testing.T) {
			o := railFromMode(t, mode)

			// Stand in for the rail moving the user off their pane while it holds
			// the keyboard: a peek, a jump, a section walk.
			o.FocusWindow(0)
			o.Mode = app.WindowManagementMode

			o, _ = HandleKeyPress(press("esc"), o)

			if o.SidebarFocused {
				t.Fatal("esc did not leave the rail")
			}
			if o.Mode != mode {
				t.Errorf("esc left the user in mode %v, they entered the rail from %v", o.Mode, mode)
			}
			if got := o.GetFocusedWindow(); got == nil || got.ID != "w2" {
				t.Errorf("esc focused %v, the user entered the rail from w2", got)
			}
		})
	}
}

// TestEscFromRailWithThePaneGoneLandsSomewhereUsable covers the pane that closed
// while the rail held the keyboard. Terminal mode with nothing to type into is
// the state the app cannot be left in.
func TestEscFromRailWithThePaneGoneLandsSomewhereUsable(t *testing.T) {
	o := railFromMode(t, app.TerminalMode)
	o.Windows = nil
	o.FocusedWindow = -1

	o, _ = HandleKeyPress(press("esc"), o)

	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v, want window management: the pane it came from is gone", o.Mode)
	}
}

// TestEscLeavesTheRailWithNoBindings is the escape hatch that must survive the
// return: a config predating the rail's section leaves every rail key dead, and
// the scope swallows what it does not bind.
func TestEscLeavesTheRailWithNoBindings(t *testing.T) {
	withSidebarGlobals(t, "left")
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.Sidebar = map[string][]string{}
	})
	o.Windows = []*terminal.Window{{ID: "w1", Width: 60, Height: 39, Workspace: o.CurrentWorkspace}}
	o.FocusedWindow = 0
	o.Mode = app.TerminalMode
	o.EnterSidebarFocus()

	o, _ = HandleKeyPress(press("esc"), o)
	if o.SidebarFocused {
		t.Fatal("esc could not leave a rail whose section binds nothing")
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v, want terminal", o.Mode)
	}
}
