package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// jumpTestOS is two panes on two workspaces, wide enough to draw a dock.
func jumpTestOS(t *testing.T) *OS {
	t.Helper()
	m := newNarrowOS(t, 120, 40)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.WorkspaceFocus = map[int]int{}
	m.Windows = []*terminal.Window{
		{ID: "here", CustomName: "here", Width: 40, Height: 20, Workspace: 1},
		{ID: "yonder", CustomName: "yonder", Width: 40, Height: 20, Workspace: 3},
	}
	m.FocusedWindow = 0
	return m
}

// TestNotificationDeadTargetsDegrade checks both ways a target can die: the pane
// closed under a live session, and the session itself gone. Neither may panic,
// error, or land on some other pane.
func TestNotificationDeadTargetsDegrade(t *testing.T) {
	t.Run("pane closed", func(t *testing.T) {
		m := jumpTestOS(t)
		m.jumpToNotifTarget(NotifTarget{SessionID: "main", WindowID: "ghost"})
		if m.FocusedWindow != 0 {
			t.Fatalf("a dead pane moved focus to %d", m.FocusedWindow)
		}
		if n := len(m.Notifications); n != 1 || m.Notifications[0].Type != "info" {
			t.Fatalf("want one info message about the closed pane, got %v", m.Notifications)
		}
		if !strings.Contains(m.Notifications[0].Message, "closed") {
			t.Fatalf("message = %q, want it to say the source is gone", m.Notifications[0].Message)
		}
	})

	t.Run("session gone", func(t *testing.T) {
		m := jumpTestOS(t)
		m.DaemonClient = session.NewTUIClient()
		m.DaemonClient.UpdateSessionCache([]session.SessionInfo{{Name: "main"}})
		m.jumpToNotifTarget(NotifTarget{SessionID: "vanished", WindowID: "yonder"})
		if m.FocusedWindow != 0 || m.CurrentWorkspace != 1 {
			t.Fatalf("a dead session moved focus to %d on workspace %d", m.FocusedWindow, m.CurrentWorkspace)
		}
		if n := len(m.Notifications); n != 1 || m.Notifications[0].Type != "info" {
			t.Fatalf("want one info message about the closed session, got %v", m.Notifications)
		}
	})
}

// TestJumpResolvesShortWindowID checks the lookup against the ids people
// actually print. The sidebar, list-windows and $TUIOS_WINDOW_ID all show the
// short form, so a link a pane printed carries a prefix, and a prefix naming
// exactly one live pane must jump. A prefix naming two may not guess.
func TestJumpResolvesShortWindowID(t *testing.T) {
	t.Run("unique prefix", func(t *testing.T) {
		m := jumpTestOS(t)
		m.Windows[1].ID = "534f6ba8-2da0-4526-a5f4-f1a8d9dcf19a"
		m.jumpToNotifTarget(NotifTarget{SessionID: "main", WindowID: "534f6ba8"})
		if m.FocusedWindow != 1 {
			t.Fatalf("focus = %d, want the pane the short id named", m.FocusedWindow)
		}
	})

	t.Run("ambiguous prefix", func(t *testing.T) {
		m := jumpTestOS(t)
		m.Windows[0].ID = "534f6ba8-2da0-4526-a5f4-f1a8d9dcf19a"
		m.Windows[1].ID = "534f6ba8-0000-0000-0000-000000000000"
		m.jumpToNotifTarget(NotifTarget{SessionID: "main", WindowID: "534f6ba8"})
		if m.FocusedWindow != 0 {
			t.Fatalf("focus = %d, want an ambiguous prefix to refuse the jump", m.FocusedWindow)
		}
	})

	t.Run("full id still exact", func(t *testing.T) {
		m := jumpTestOS(t)
		m.Windows[1].ID = "534f6ba8-2da0-4526-a5f4-f1a8d9dcf19a"
		m.jumpToNotifTarget(NotifTarget{SessionID: "main", WindowID: "534f6ba8-2da0-4526-a5f4-f1a8d9dcf19a"})
		if m.FocusedWindow != 1 {
			t.Fatalf("focus = %d, want the full id to jump as before", m.FocusedWindow)
		}
	})
}
