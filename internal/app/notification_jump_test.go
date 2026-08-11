package app

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// drawnNotifZones renders the dock so the message block records where it landed,
// and hands back the zones a click is tested against.
func drawnNotifZones(t *testing.T, m *OS) notifHitZones {
	t.Helper()
	m.renderDockString()
	if !m.notifHit.Active {
		t.Fatal("the dock drew no message block, so there is nothing to click")
	}
	return m.notifHit
}

// TestNotificationClickJumpsToItsPane checks a click on the message body lands
// on the pane it came from, switching workspace on the way, and takes the
// message off the dock.
func TestNotificationClickJumpsToItsPane(t *testing.T) {
	m := jumpTestOS(t)
	m.ShowNotificationFrom("yonder needs input", "warning", config.NotificationDuration,
		NotifTarget{SessionID: "main", WindowID: "yonder"})

	z := drawnNotifZones(t, m)
	if !m.NotificationClick(z.X0+3, z.Y) {
		t.Fatal("a click on the message body was not consumed")
	}
	if m.FocusedWindow != 1 {
		t.Fatalf("focus is %d, want the pane the message came from (1)", m.FocusedWindow)
	}
	if m.CurrentWorkspace != 3 {
		t.Fatalf("workspace is %d, want the target's workspace (3)", m.CurrentWorkspace)
	}
	if len(m.Notifications) != 0 {
		t.Fatalf("%d messages left; going there is the acknowledgment", len(m.Notifications))
	}
}

// TestNotificationDismissZoneDoesNotJump checks the block's right-hand end pops
// the visible message and leaves focus where it was.
func TestNotificationDismissZoneDoesNotJump(t *testing.T) {
	m := jumpTestOS(t)
	m.ShowNotification("first", "info", config.NotificationDuration)
	m.ShowNotificationFrom("yonder finished", "success", config.NotificationDuration,
		NotifTarget{SessionID: "main", WindowID: "yonder"})

	z := drawnNotifZones(t, m)
	if !m.NotificationClick(z.DismissX0, z.Y) {
		t.Fatal("a click on the dismiss zone was not consumed")
	}
	if m.FocusedWindow != 0 || m.CurrentWorkspace != 1 {
		t.Fatalf("dismissing jumped anyway: focus=%d workspace=%d", m.FocusedWindow, m.CurrentWorkspace)
	}
	if len(m.Notifications) != 1 || m.Notifications[0].Message != "first" {
		t.Fatalf("queue = %v, want only the message that was behind it", m.Notifications)
	}
}

// TestNotificationClickOutsideBlockIsNotOurs checks the block claims only its
// own columns, so the dock items beside it still get their clicks.
func TestNotificationClickOutsideBlockIsNotOurs(t *testing.T) {
	m := jumpTestOS(t)
	m.ShowNotificationFrom("yonder finished", "success", config.NotificationDuration,
		NotifTarget{SessionID: "main", WindowID: "yonder"})

	z := drawnNotifZones(t, m)
	if m.NotificationClick(z.X0-1, z.Y) {
		t.Fatal("a click left of the block was claimed by it")
	}
	if m.NotificationClick(z.X0+3, z.Y-1) {
		t.Fatal("a click on another row was claimed by the block")
	}
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

// TestNotificationKeyboardTwinWalksTheQueue checks prefix+j activates the newest
// targeted message and steps past the untargeted ones, and reports honestly when
// there is nothing to jump to.
func TestNotificationKeyboardTwinWalksTheQueue(t *testing.T) {
	m := jumpTestOS(t)
	if m.JumpToNotification() {
		t.Fatal("an empty queue claimed to have jumped")
	}

	m.ShowNotificationFrom("yonder finished", "success", config.NotificationDuration,
		NotifTarget{SessionID: "main", WindowID: "yonder"})
	m.ShowNotification("Copied", "info", config.NotificationDuration)

	if !m.JumpToNotification() {
		t.Fatal("a targeted message behind an untargeted one was not reachable")
	}
	if m.FocusedWindow != 1 {
		t.Fatalf("focus is %d, want the targeted pane (1)", m.FocusedWindow)
	}
	if len(m.Notifications) != 1 || m.Notifications[0].Message != "Copied" {
		t.Fatalf("queue = %v, want only the untargeted message left", m.Notifications)
	}
	if m.JumpToNotification() {
		t.Fatal("an untargeted-only queue claimed to have jumped")
	}
}

// TestTargetedNotificationIsUnderlined checks the affordance: a message you can
// follow is marked as one, and a message you cannot is not.
func TestTargetedNotificationIsUnderlined(t *testing.T) {
	m := jumpTestOS(t)
	m.ShowNotification("Copied to clipboard", "info", config.NotificationDuration)
	plain, ok := m.renderNotificationBlock(m.GetRenderWidth(), 0)
	if !ok {
		t.Fatal("no block for the untargeted message")
	}
	if hasUnderlineSGR(plain.Text) {
		t.Fatalf("an untargeted message drew underlined: %q", plain.Text)
	}

	m.Notifications = nil
	m.ShowNotificationFrom("yonder needs input", "warning", config.NotificationDuration,
		NotifTarget{SessionID: "main", WindowID: "yonder"})
	linked, ok := m.renderNotificationBlock(m.GetRenderWidth(), 0)
	if !ok {
		t.Fatal("no block for the targeted message")
	}
	if !hasUnderlineSGR(linked.Text) {
		t.Fatalf("a targeted message drew without its link mark: %q", linked.Text)
	}
}

// hasUnderlineSGR reports whether any SGR sequence in s sets attribute 4.
// Matching on the parameter rather than on a literal escape is necessary
// because lipgloss folds underline in with the colours in one sequence.
var sgrPattern = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

func hasUnderlineSGR(s string) bool {
	for _, seq := range sgrPattern.FindAllStringSubmatch(s, -1) {
		for _, p := range strings.Split(seq[1], ";") {
			if p == "4" {
				return true
			}
		}
	}
	return false
}

// TestAgentStateChangeNotifiesWithATarget checks the wiring at the source: an
// unattended pane changing state posts a message that points back at it, and a
// pane the user is already watching says nothing.
func TestAgentStateChangeNotifiesWithATarget(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m := jumpTestOS(t)

	m.noteAgentState(m.Windows[1], "needs_input")
	if len(m.Notifications) != 1 {
		t.Fatalf("an unattended pane needing input posted %d messages, want 1", len(m.Notifications))
	}
	n := m.Notifications[0]
	if n.Target == nil || n.Target.WindowID != "yonder" {
		t.Fatalf("message target = %v, want the pane that changed", n.Target)
	}

	m.Notifications = nil
	m.Windows[0].AgentState = "working"
	m.noteAgentState(m.Windows[0], "done")
	if len(m.Notifications) != 0 {
		t.Fatalf("the focused pane announced itself: %v", m.Notifications)
	}
	if !m.agentSeen("here") {
		t.Fatal("finishing under the user's eyes did not count as seen")
	}
}
