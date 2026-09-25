package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// dockSessionOS builds a full OS and draws one frame, which is what puts the
// dock's session controls on screen and records where they landed.
func dockSessionOS(t *testing.T, daemon bool) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 160, 40
	o.EffectiveWidth, o.EffectiveHeight = 160, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	if daemon {
		o.IsDaemonSession = true
		o.DaemonClient = &session.TUIClient{}
		o.SessionName = "session-1"
	}
	_ = o.View()
	return o
}

// dockSessionColumn scans the dock row for a column routing to the given
// control, or -1. It reads the rects the renderer recorded, which the app
// package's own test already ties to the cells that were drawn.
func dockSessionColumn(o *app.OS, want app.DockSessionAction) int {
	y := o.Height - 1
	for x := range o.Width {
		if o.DockSessionActionAt(x, y) == want {
			return x
		}
	}
	return -1
}

// TestDockCloseAlwaysConfirms is the control's whole contract: the click never
// ends the session, it only ever asks.
func TestDockCloseAlwaysConfirms(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		o := dockSessionOS(t, daemon)
		x := dockSessionColumn(o, app.DockSessionClose)
		if x < 0 {
			t.Fatalf("daemon %v: the dock drew no close control", daemon)
		}

		o, cmd := handleMouseClick(clickAt(x, o.Height-1), o)
		if !o.ShowSessionClose {
			t.Errorf("daemon %v: clicking close did not raise the confirmation", daemon)
		}
		if cmd != nil {
			t.Errorf("daemon %v: clicking close returned a command; it must only ask", daemon)
		}
		if o.QuitRequested {
			t.Errorf("daemon %v: clicking close asked to quit outright", daemon)
		}

		// Esc backs out and leaves the session alone.
		o, _ = HandleKeyPress(press("esc"), o)
		if o.ShowSessionClose || o.QuitRequested {
			t.Errorf("daemon %v: esc did not cancel cleanly", daemon)
		}
	}
}

// TestDockLeaveControlFollowsTheRunPath is the correctness requirement read
// from the click path: with no daemon the control is not there to click.
func TestDockLeaveControlFollowsTheRunPath(t *testing.T) {
	if x := dockSessionColumn(dockSessionOS(t, false), app.DockSessionLeave); x >= 0 {
		t.Errorf("a session with no daemon offered a leave-running control at column %d", x)
	}
	if x := dockSessionColumn(dockSessionOS(t, true), app.DockSessionLeave); x < 0 {
		t.Error("a daemon session offered no leave-running control")
	}
}

// TestCloseSessionKeybindRaisesTheSameDialog checks the keyboard twin asks the
// same question rather than being a quiet shortcut past it.
func TestCloseSessionKeybindRaisesTheSameDialog(t *testing.T) {
	o := dockSessionOS(t, true)
	o.PrefixActive = true
	o, cmd := HandlePrefixCommand(press("X"), o)
	if !o.ShowSessionClose {
		t.Fatal("ctrl+b X did not raise the close confirmation")
	}
	if cmd != nil || o.QuitRequested {
		t.Error("ctrl+b X did something besides asking")
	}
}

// clickAt is a plain left press at a cell.
func clickAt(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}
}
