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
