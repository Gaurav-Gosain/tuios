package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withAutoEnterTerminalOnFocus sets the policy for a test and restores it after.
func withAutoEnterTerminalOnFocus(t *testing.T, on bool) {
	t.Helper()
	prev := config.AutoEnterTerminalOnFocus
	config.AutoEnterTerminalOnFocus = on
	t.Cleanup(func() { config.AutoEnterTerminalOnFocus = prev })
}

// twoPaneWM is two tiled panes in window-management mode, with the real
// keybind registry, so Tab / 1-9 / Alt+arrows travel the same HandleKeyPress
// path cmd/tuios registers.
func twoPaneWM(t *testing.T) *app.OS {
	t.Helper()
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Width, o.Height = 120, 40
	o.AutoTiling = true
	ws := o.CurrentWorkspace
	o.Windows = []*terminal.Window{
		{ID: "a", X: 0, Y: 0, Width: 60, Height: 40, Workspace: ws},
		{ID: "b", X: 60, Y: 0, Width: 60, Height: 40, Workspace: ws},
	}
	o.FocusedWindow = 0
	o.Mode = app.WindowManagementMode
	return o
}

func shiftTab() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
}

// TestNextWindowFromWindowModeEntersTerminalMode is the feature: Tab in
// window-management mode focuses the other pane and leaves the user typing in
// it, without a separate enter_terminal_mode key.
func TestNextWindowFromWindowModeEntersTerminalMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)
	start := focusedID(o)

	model, _ := HandleInput(press("tab"), o)
	o = model.(*app.OS)

	if got := focusedID(o); got == start {
		t.Fatalf("tab did not move focus from %q", start)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after next_window, want terminal mode", o.Mode)
	}
}

// TestPrevWindowFromWindowModeEntersTerminalMode is the other cycle key.
func TestPrevWindowFromWindowModeEntersTerminalMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)
	start := focusedID(o)

	o, _ = HandleKeyPress(shiftTab(), o)

	if got := focusedID(o); got == start {
		t.Fatalf("shift+tab did not move focus from %q", start)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after prev_window, want terminal mode", o.Mode)
	}
}

// TestNumberedSelectFromWindowModeEntersTerminalMode drives the registered
// select_window_N action. Digits 1-4 in window-management mode are snap-corner
// keys; numbered select is the action (and the leader-then-digit chord).
func TestNumberedSelectFromWindowModeEntersTerminalMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)
	if focusedID(o) != "a" {
		t.Fatalf("fixture focused %q, want a", focusedID(o))
	}

	o, _ = GetDispatcher().Dispatch("select_window_2", press("2"), o)

	if got := focusedID(o); got != "b" {
		t.Errorf("focused %q after select_window_2, want b", got)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after numbered select, want terminal mode", o.Mode)
	}
}

// TestPrefixNumberedSelectFromWindowModeEntersTerminalMode is the chord users
// actually press: leader, then a digit.
func TestPrefixNumberedSelectFromWindowModeEntersTerminalMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
	if !o.PrefixActive {
		t.Fatal("leader did not arm the prefix")
	}
	o, _ = HandleKeyPress(press("2"), o)

	if got := focusedID(o); got != "b" {
		t.Errorf("focused %q after leader,2, want b", got)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after prefix numbered select, want terminal mode", o.Mode)
	}
}

// TestDirectionalFocusFromWindowModeEntersTerminalMode uses the shipped
// Alt+arrows binding, which is honoured in window-management mode as well as
// terminal mode.
func TestDirectionalFocusFromWindowModeEntersTerminalMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)
	start := focusedID(o)

	o, _ = HandleKeyPress(altArrow("right"), o)

	if got := focusedID(o); got == start {
		t.Fatalf("alt+right did not move focus from %q", start)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after directional focus, want terminal mode", o.Mode)
	}
}

// TestRegisteredNextWindowActionEntersTerminalMode hits the dispatcher the
// registry routes next_window to, so a rebind of the key still gets the mode
// change.
func TestRegisteredNextWindowActionEntersTerminalMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)
	start := focusedID(o)

	o, _ = GetDispatcher().Dispatch("next_window", tea.KeyPressMsg{}, o)

	if got := focusedID(o); got == start {
		t.Fatalf("next_window did not move focus from %q", start)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after dispatching next_window, want terminal mode", o.Mode)
	}
}

// TestFocusingTheAlreadyFocusedPaneStaysInWindowMode is the no-op: selecting
// the pane that already has focus must not steal window-management keys.
func TestFocusingTheAlreadyFocusedPaneStaysInWindowMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)
	start := focusedID(o)

	o, _ = GetDispatcher().Dispatch("select_window_1", press("1"), o)

	if got := focusedID(o); got != start {
		t.Errorf("focused %q after selecting the current pane, want %q", got, start)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after a no-op focus, want window management", o.Mode)
	}
}

// TestDirectionalNoOpAtEdgeStaysInWindowMode is the other no-op: nothing that
// way, so focus and mode stay put.
func TestDirectionalNoOpAtEdgeStaysInWindowMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)
	start := focusedID(o)

	o, _ = HandleKeyPress(altArrow("left"), o)

	if got := focusedID(o); got != start {
		t.Errorf("alt+left from the left edge focused %q, want %q", got, start)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after a no-op directional focus, want window management", o.Mode)
	}
}

// TestAutoEnterOffKeepsWindowManagementMode is the opt-out: Tab still moves
// focus, and n/w remain the window-manager keys they were.
func TestAutoEnterOffKeepsWindowManagementMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, false)
	o := twoPaneWM(t)
	start := focusedID(o)

	o, _ = HandleKeyPress(press("tab"), o)

	if got := focusedID(o); got == start {
		t.Fatalf("tab did not move focus from %q with auto-enter off", start)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v with auto-enter off, want window management", o.Mode)
	}
}

// TestFocusWindowItselfDoesNotEnterTerminalMode pins the boundary: hover-focus
// and click-to-type=off both call FocusWindow, and must not inherit this
// policy. Only the registered focus commands enter.
func TestFocusWindowItselfDoesNotEnterTerminalMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)

	o.FocusWindow(1)

	if focusedID(o) != "b" {
		t.Errorf("FocusWindow(1) focused %q, want b", focusedID(o))
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after FocusWindow, want window management: hover and click-to-type=off use this path", o.Mode)
	}
}

// TestNonFocusWindowCommandStaysInWindowMode is n/w staying usable: a
// window-management key that is not a focus command must not dump the user
// into terminal mode. Zoom is the stand-in that does not spawn a PTY.
func TestNonFocusWindowCommandStaysInWindowMode(t *testing.T) {
	withAutoEnterTerminalOnFocus(t, true)
	o := twoPaneWM(t)

	o, _ = HandleKeyPress(press("z"), o)

	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after toggle_zoom, want window management", o.Mode)
	}
}
