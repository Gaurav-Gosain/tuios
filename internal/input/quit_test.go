package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRequestQuitConfirmsOnlyWhenThereIsSomethingToLose pins the rule the three
// quit keybindings used to each carry their own copy of: put the dialog up when
// a window is running something, quit outright when nothing is.
func TestRequestQuitConfirmsOnlyWhenThereIsSomethingToLose(t *testing.T) {
	prev := config.AlwaysConfirmQuit
	t.Cleanup(func() { config.AlwaysConfirmQuit = prev })

	t.Run("nothing running quits outright", func(t *testing.T) {
		config.AlwaysConfirmQuit = false
		o := app.NewOS(app.OSOptions{})

		m, cmd := requestQuit(o)
		if cmd == nil {
			t.Fatal("expected a quit command, got none")
		}
		if m.ShowQuitConfirm {
			t.Fatal("put up the confirmation dialog with nothing to confirm")
		}
	})

	t.Run("confirm-always puts the dialog up", func(t *testing.T) {
		config.AlwaysConfirmQuit = true
		o := app.NewOS(app.OSOptions{})

		m, cmd := requestQuit(o)
		if cmd != nil {
			t.Fatal("quit outright despite confirm-quit being set")
		}
		if !m.ShowQuitConfirm {
			t.Fatal("confirmation dialog not shown")
		}
		if m.QuitConfirmSelection != 0 {
			t.Fatalf("dialog selection = %d, want 0 (Yes)", m.QuitConfirmSelection)
		}
	})
}

// TestDetachOutsideADaemonSessionIsNotADetach covers the branch every caller of
// detachSession has to handle: there is nothing to detach from outside a daemon
// session, and each caller does something different about it (one falls back to
// window-management mode, the other ignores the key). Reporting that rather than
// quitting is what keeps that decision with the caller.
func TestDetachOutsideADaemonSessionIsNotADetach(t *testing.T) {
	o := app.NewOS(app.OSOptions{})

	_, cmd, detached := detachSession(o)
	if detached {
		t.Fatal("reported a detach outside a daemon session")
	}
	if cmd != nil {
		t.Fatal("returned a quit command outside a daemon session")
	}
}
