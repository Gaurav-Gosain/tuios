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
	prev := config.Global.AlwaysConfirmQuit
	t.Cleanup(func() { config.Global.AlwaysConfirmQuit = prev })

	t.Run("nothing running quits outright", func(t *testing.T) {
		o := app.NewOS(app.OSOptions{})
		o.Settings.AlwaysConfirmQuit = false

		m, cmd := requestQuit(o)
		if cmd == nil {
			t.Fatal("expected a quit command, got none")
		}
		if m.ShowQuitMenu {
			t.Fatal("put up the quit menu with nothing to confirm")
		}
	})

	t.Run("confirm-always puts the menu up", func(t *testing.T) {
		o := app.NewOS(app.OSOptions{})
		o.Settings.AlwaysConfirmQuit = true

		m, cmd := requestQuit(o)
		if cmd != nil {
			t.Fatal("quit outright despite confirm-quit being set")
		}
		if !m.ShowQuitMenu {
			t.Fatal("quit menu not shown")
		}
		if m.QuitMenuSelected != 0 {
			t.Fatalf("menu selection = %d, want 0 (the default row)", m.QuitMenuSelected)
		}
	})

	t.Run("daemon session always gets the menu", func(t *testing.T) {
		o := app.NewOS(app.OSOptions{IsDaemonSession: true})
		o.Settings.AlwaysConfirmQuit = false

		m, cmd := requestQuit(o)
		if cmd != nil {
			t.Fatal("a daemon-session quit ran a command instead of opening the menu")
		}
		if !m.ShowQuitMenu {
			t.Fatal("quit menu not shown in a daemon session")
		}
		if len(m.QuitMenuItems) == 0 || m.QuitMenuItems[0].Kind != app.QuitDetach {
			t.Fatalf("daemon quit menu default row = %+v, want Detach first", m.QuitMenuItems)
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
