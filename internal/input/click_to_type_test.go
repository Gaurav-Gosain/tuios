package input

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withClickToType sets the policy for a test and restores it after.
func withClickToType(t *testing.T, mode string) {
	t.Helper()
	prev := config.ClickToType
	config.ClickToType = mode
	t.Cleanup(func() { config.ClickToType = prev })
}

// clickPane presses and releases the left button on a cell through HandleInput,
// the entry point cmd/tuios registers with SetInputHandler. The policy lives on
// the press and resolves on the release, and both have to travel the real
// routing: a handler called directly would prove nothing about whether a click
// reaches it.
func clickPane(o *app.OS, x, y int) {
	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
	HandleInput(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}, o)
}

// agePane pushes a pane's last click far enough into the past that the next one
// starts a new gesture, without sleeping for it.
func agePane(win *terminal.Window) {
	win.LastClickTime = time.Now().Add(-2 * multiClickInterval)
}

// TestClickToTypeSingleEntersTerminalMode is the default, and the behaviour
// every other policy is measured against: one click on a pane's content focuses
// it and leaves the user typing in it.
func TestClickToTypeSingleEntersTerminalMode(t *testing.T) {
	withClickToType(t, config.ClickToTypeSingle)
	o, wa, wb := twoPaneBSP(t)
	_, right := leftPaneOf(wa, wb)
	rightIdx := indexOf(o, right)
	cx, cy := contentCell(right)

	clickPane(o, cx, cy)

	if o.FocusedWindow != rightIdx {
		t.Errorf("focused window %d, want the pane that was clicked (%d)", o.FocusedWindow, rightIdx)
	}
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v, want terminal mode: single is the default and must not change", o.Mode)
	}
}

// TestClickToTypeOffOnlyFocuses is the setting the report asked for: the click
// picks the pane and the keyboard keeps driving the window manager.
func TestClickToTypeOffOnlyFocuses(t *testing.T) {
	withClickToType(t, config.ClickToTypeOff)
	o, wa, wb := twoPaneBSP(t)
	_, right := leftPaneOf(wa, wb)
	rightIdx := indexOf(o, right)
	cx, cy := contentCell(right)

	clickPane(o, cx, cy)

	if o.FocusedWindow != rightIdx {
		t.Errorf("focused window %d, want the pane that was clicked (%d)", o.FocusedWindow, rightIdx)
	}
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v, want window management: the click was not a request to type", o.Mode)
	}

	// Clicking again changes nothing, so the policy cannot be walked past by
	// repeating the gesture.
	clickPane(o, cx, cy)
	if o.Mode != app.WindowManagementMode {
		t.Errorf("mode = %v after a second click, want window management", o.Mode)
	}
}

// TestClickToTypeDoubleNeedsTwoClicks is the middle setting: the first click is
// a pointer, the second is a request to type.
func TestClickToTypeDoubleNeedsTwoClicks(t *testing.T) {
	withClickToType(t, config.ClickToTypeDouble)
	o, wa, wb := twoPaneBSP(t)
	_, right := leftPaneOf(wa, wb)
	rightIdx := indexOf(o, right)
	cx, cy := contentCell(right)

	clickPane(o, cx, cy)
	if o.FocusedWindow != rightIdx {
		t.Errorf("focused window %d, want the pane that was clicked (%d)", o.FocusedWindow, rightIdx)
	}
	if o.Mode != app.WindowManagementMode {
		t.Fatalf("mode = %v after one click, want window management", o.Mode)
	}

	clickPane(o, cx, cy)
	if o.Mode != app.TerminalMode {
		t.Errorf("mode = %v after the second click, want terminal mode", o.Mode)
	}
}

// TestClickToTypeDoubleIsTheSameDoubleClickAsSelection pins the policy to the
// one notion of a double click the mouse path already has. Two clicks too far
// apart in time, or on different cells, are two single clicks for selection and
// have to be two single clicks here.
func TestClickToTypeDoubleIsTheSameDoubleClickAsSelection(t *testing.T) {
	withClickToType(t, config.ClickToTypeDouble)

	t.Run("too slow", func(t *testing.T) {
		o, wa, wb := twoPaneBSP(t)
		_, right := leftPaneOf(wa, wb)
		cx, cy := contentCell(right)

		clickPane(o, cx, cy)
		agePane(right)
		clickPane(o, cx, cy)

		if o.Mode != app.WindowManagementMode {
			t.Errorf("mode = %v, want window management: the clicks were too far apart to be a double click", o.Mode)
		}
	})

	t.Run("too far apart", func(t *testing.T) {
		o, wa, wb := twoPaneBSP(t)
		_, right := leftPaneOf(wa, wb)
		cx, cy := contentCell(right)

		clickPane(o, cx, cy)
		clickPane(o, cx+multiClickSlop+3, cy+2)

		if o.Mode != app.WindowManagementMode {
			t.Errorf("mode = %v, want window management: the second click landed on another cell", o.Mode)
		}
	})
}

// TestClickToTypeDoubleSelectsNothing is the bargain with selection. The double
// click that opens terminal mode is spent on the mode: it highlights nothing and
// writes nothing to the clipboard, and the next click in the pane starts a fresh
// selection rather than arriving as the third click of a line select.
func TestClickToTypeDoubleSelectsNothing(t *testing.T) {
	withClickToType(t, config.ClickToTypeDouble)
	o, wa, wb := twoPaneBSP(t)
	_, right := leftPaneOf(wa, wb)
	cx, cy := contentCell(right)

	clickPane(o, cx, cy)
	clickPane(o, cx, cy)
	if o.Mode != app.TerminalMode {
		t.Fatalf("mode = %v, want terminal mode", o.Mode)
	}
	if right.InCopyMode() || right.HasSelection() {
		t.Error("the double click that changed mode also started a selection")
	}

	// The first click in the mode it opened, at the same cell and inside the
	// multi-click window: it must read as one click, not three.
	HandleInput(tea.MouseClickMsg{Button: tea.MouseLeft, X: cx, Y: cy}, o)
	if right.ClickCount != 1 {
		t.Errorf("click count = %d for the first click in terminal mode, want 1", right.ClickCount)
	}
}

// TestClickToTypeLeavesTheTitleBarAlone guards the gate every policy shares: it
// is about clicking into a pane's content. A title-bar press is a drag handle in
// every policy and never asks to type.
func TestClickToTypeLeavesTheTitleBarAlone(t *testing.T) {
	for _, mode := range config.ClickToTypeModes {
		t.Run(mode, func(t *testing.T) {
			withClickToType(t, mode)
			o, wa, wb := twoPaneBSP(t)
			_, right := leftPaneOf(wa, wb)
			rightIdx := indexOf(o, right)

			clickPane(o, right.X+2, right.Y)

			if o.FocusedWindow != rightIdx {
				t.Errorf("focused window %d, want the pane whose title bar was clicked (%d)", o.FocusedWindow, rightIdx)
			}
			if o.Mode != app.WindowManagementMode {
				t.Errorf("mode = %v, want window management: a title-bar press is a drag handle", o.Mode)
			}
		})
	}
}

// TestClickToTypeLeavesTheDockAlone keeps the policy inside a pane. The dock
// band owns its rows whatever a click into a pane would have done.
func TestClickToTypeLeavesTheDockAlone(t *testing.T) {
	for _, mode := range config.ClickToTypeModes {
		t.Run(mode, func(t *testing.T) {
			withClickToType(t, mode)
			o, _, _ := twoPaneBSP(t)

			clickPane(o, o.Width/2, o.Height-1)
			clickPane(o, o.Width/2, o.Height-1)

			if o.Mode != app.WindowManagementMode {
				t.Errorf("mode = %v after clicks on the dock band, want window management", o.Mode)
			}
		})
	}
}

// mouseModePane is a focused pane running an application that asked for the
// mouse, with its writes recorded, so a test can see exactly what the guest was
// sent.
func mouseModePane(t *testing.T, mode app.Mode) (*app.OS, *terminal.Window, *capturePty) {
	t.Helper()
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), mode)
	win := o.Windows[0]
	// Mouse bytes for a local pane go into the emulator rather than the pty, so
	// the pane is put in daemon mode with its write routed to the recording pty.
	win.DaemonMode = true
	win.DaemonWriteFunc = func(b []byte) error { _, err := pty.Write(b); return err }
	win.Terminal.Write([]byte("\x1b[?1002h\x1b[?1006h")) // button tracking, SGR encoding
	if !win.Terminal.HasMouseMode() {
		t.Fatal("the fixture pane did not take mouse tracking")
	}
	return o, win, pty
}

// TestClickToTypeDoubleReachesAMouseModeApp is the cost and the way out, for the
// panes where the mouse matters most. Terminal mode is what hands the mouse to
// the application, here as everywhere else, so under "double" the clicks that
// open the mode are the window manager's and the ones after it are the app's.
func TestClickToTypeDoubleReachesAMouseModeApp(t *testing.T) {
	withClickToType(t, config.ClickToTypeDouble)
	o, win, pty := mouseModePane(t, app.WindowManagementMode)
	cx, cy := contentCell(win)

	clickPane(o, cx, cy)
	if o.Mode != app.WindowManagementMode {
		t.Fatalf("mode = %v after one click, want window management", o.Mode)
	}
	if len(pty.got) != 0 {
		t.Errorf("the focusing click was forwarded to the guest: %q", pty.got)
	}

	clickPane(o, cx, cy)
	if o.Mode != app.TerminalMode {
		t.Fatalf("mode = %v after the second click, want terminal mode", o.Mode)
	}
	if len(pty.got) != 0 {
		t.Errorf("the click that changed mode was also forwarded to the guest: %q", pty.got)
	}

	clickPane(o, cx, cy)
	if len(pty.got) == 0 {
		t.Error("a click in the mode the double click opened did not reach the guest")
	}
}

// TestClickToTypeOffReachesAMouseModeAppThroughTheKeybinding is the same
// contract for the policy that never changes mode from a click: the way in is
// the key bound to enter_terminal_mode, and the app has the mouse from there.
func TestClickToTypeOffReachesAMouseModeAppThroughTheKeybinding(t *testing.T) {
	withClickToType(t, config.ClickToTypeOff)
	o, win, pty := mouseModePane(t, app.WindowManagementMode)
	cx, cy := contentCell(win)

	clickPane(o, cx, cy)
	if o.Mode != app.WindowManagementMode {
		t.Fatalf("mode = %v, want window management", o.Mode)
	}
	if len(pty.got) != 0 {
		t.Errorf("a click in window-management mode was forwarded to the guest: %q", pty.got)
	}

	HandleInput(tea.KeyPressMsg{Code: 'i', Text: "i"}, o)
	if o.Mode != app.TerminalMode {
		t.Fatalf("mode = %v after the enter_terminal_mode binding, want terminal mode", o.Mode)
	}

	clickPane(o, cx, cy)
	if len(pty.got) == 0 {
		t.Error("a click in terminal mode did not reach the guest that asked for the mouse")
	}
}

// TestClickToTypeLeavesTerminalModeClicksAlone pins the other side of the
// policy: it decides how terminal mode is entered and nothing about what a click
// does once the user is in it.
func TestClickToTypeLeavesTerminalModeClicksAlone(t *testing.T) {
	for _, mode := range config.ClickToTypeModes {
		t.Run(mode, func(t *testing.T) {
			withClickToType(t, mode)
			o, win, pty := mouseModePane(t, app.TerminalMode)
			cx, cy := contentCell(win)

			clickPane(o, cx, cy)

			if o.Mode != app.TerminalMode {
				t.Errorf("mode = %v, want the terminal mode the click started in", o.Mode)
			}
			if len(pty.got) == 0 {
				t.Error("the click did not reach the guest that asked for the mouse")
			}
		})
	}
}
