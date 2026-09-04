package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withAutoEnterTerminalOnFocus sets the policy on one session. The setting is
// per client, so it needs no restore: the next test builds its own OS.
func withAutoEnterTerminalOnFocus(o *app.OS, mode config.AutoEnterTerminalPolicy) *app.OS {
	o.Settings.AutoEnterTerminalOnFocus = mode
	return o
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

func threePaneWM(t *testing.T) *app.OS {
	t.Helper()
	o := twoPaneWM(t)
	ws := o.CurrentWorkspace
	o.Windows = append(o.Windows, &terminal.Window{
		ID: "c", X: 80, Y: 0, Width: 40, Height: 40, Workspace: ws,
	})
	return o
}

// TestNextWindowFromWindowModeEntersTerminalMode is all: Tab in
// window-management mode focuses the other pane and leaves the user typing in
// it, without a separate enter_terminal_mode key.
func TestNextWindowFromWindowModeEntersTerminalMode(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalAll)
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

// TestPrevWindowFromWindowModeEntersTerminalMode is the other cycle key, all.
func TestPrevWindowFromWindowModeEntersTerminalMode(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalAll)
	start := focusedID(o)

	o, _ = HandleKeyPress(shiftTab(), o)

	if got := focusedID(o); got == start {
		t.Fatalf("shift+tab did not move focus from %q", start)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after prev_window, want terminal mode", o.Mode)
	}
}

// TestNumberedSelectFromWindowModeEntersTerminalMode is targeted: pick a pane
// by number, then type in it. Digits 1-4 in window-management mode are
// snap-corner keys; numbered select is the action (and the leader-then-digit
// chord).
func TestNumberedSelectFromWindowModeEntersTerminalMode(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalTargeted)
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
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalTargeted)

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
// terminal mode. targeted treats that as picking a pane.
func TestDirectionalFocusFromWindowModeEntersTerminalMode(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalTargeted)
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
// change when the policy is all.
func TestRegisteredNextWindowActionEntersTerminalMode(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalAll)
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
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalTargeted)
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
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalTargeted)
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
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalOff)
	start := focusedID(o)

	o, _ = HandleKeyPress(press("tab"), o)

	if got := focusedID(o); got == start {
		t.Fatalf("tab did not move focus from %q with auto-enter off", start)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v with auto-enter off, want window management", o.Mode)
	}
}

// TestDefaultOffLetsTabCycleThreePanes is the reviewer's reproduction: with
// the shipped default, Tab must keep cycling in window-management mode, or the
// second Tab goes to the shell and the third pane is unreachable.
func TestDefaultOffLetsTabCycleThreePanes(t *testing.T) {
	o := threePaneWM(t)
	if o.Settings.AutoEnterTerminalOnFocus != config.AutoEnterTerminalOff {
		t.Fatalf("session default is %q; this test needs the shipped off default", o.Settings.AutoEnterTerminalOnFocus)
	}

	o, _ = HandleKeyPress(press("tab"), o)
	if got := focusedID(o); got != "b" {
		t.Fatalf("first tab focused %q, want b", got)
	}
	if o.Mode != app.WindowManagementMode {
		t.Fatalf("mode = %v after first tab, want window management", o.Mode)
	}

	o, _ = HandleKeyPress(press("tab"), o)
	if got := focusedID(o); got != "c" {
		t.Errorf("second tab focused %q, want c", got)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after second tab, want window management", o.Mode)
	}
}

// TestTargetedLetsTabCycleThreePanes is the design: targeted is click-like
// (select, arrows), not cycle. Tab must still walk A → B → C.
func TestTargetedLetsTabCycleThreePanes(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(threePaneWM(t), config.AutoEnterTerminalTargeted)

	o, _ = HandleKeyPress(press("tab"), o)
	if got := focusedID(o); got != "b" {
		t.Fatalf("first tab focused %q, want b", got)
	}
	if o.Mode != app.WindowManagementMode {
		t.Fatalf("mode = %v after first tab under targeted, want window management", o.Mode)
	}

	o, _ = HandleKeyPress(press("tab"), o)
	if got := focusedID(o); got != "c" {
		t.Errorf("second tab focused %q, want c", got)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after second tab under targeted, want window management", o.Mode)
	}
}

// TestDirectionalFocusFromWindowModeWithDefaultOff drives Alt+Right through
// HandleKeyPress, the same path a user presses, so a dispatcher-only test
// cannot hide a routing miss. Focus still moves; mode stays window-management.
func TestDirectionalFocusFromWindowModeWithDefaultOff(t *testing.T) {
	o := twoPaneWM(t)
	if o.Settings.AutoEnterTerminalOnFocus != config.AutoEnterTerminalOff {
		t.Fatalf("session default is %q; this test needs the shipped off default", o.Settings.AutoEnterTerminalOnFocus)
	}
	start := focusedID(o)

	o, _ = HandleKeyPress(altArrow("right"), o)

	if got := focusedID(o); got == start {
		t.Fatalf("alt+right did not move focus from %q", start)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after directional focus with default off, want window management", o.Mode)
	}
}

// TestFocusWindowItselfDoesNotEnterTerminalMode pins the boundary: hover-focus
// and click-to-type=off both call FocusWindow, and must not inherit this
// policy. Only the registered focus commands enter.
func TestFocusWindowItselfDoesNotEnterTerminalMode(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalAll)

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
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalAll)

	o, _ = HandleKeyPress(press("z"), o)

	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after toggle_zoom, want window management", o.Mode)
	}
}

// TestAutoEnterPathIsSilent is the review: a Tab that auto-enters must not
// toast "Terminal mode" on every keypress. The explicit enter binding still
// does.
func TestAutoEnterPathIsSilent(t *testing.T) {
	o := withAutoEnterTerminalOnFocus(twoPaneWM(t), config.AutoEnterTerminalAll)
	before := len(o.Notifications)

	o, _ = HandleKeyPress(press("tab"), o)

	if o.Mode != app.TerminalMode {
		t.Fatalf("mode = %v after tab under all, want terminal mode", o.Mode)
	}
	if got := len(o.Notifications); got != before {
		t.Errorf("auto-enter queued %d notifications, want %d (silent)", got, before)
	}
}

func TestExplicitEnterTerminalModeStillNotifies(t *testing.T) {
	o := twoPaneWM(t)
	before := len(o.Notifications)

	o, _ = HandleKeyPress(press("i"), o)

	if o.Mode != app.TerminalMode {
		t.Fatalf("mode = %v after enter_terminal_mode, want terminal mode", o.Mode)
	}
	if got := len(o.Notifications); got <= before {
		t.Errorf("explicit enter queued %d notifications, want more than %d", got, before)
	}
}
