package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// hasNotification reports whether a live notification message contains s.
func hasNotification(m *OS, s string) bool {
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, s) {
			return true
		}
	}
	return false
}

// TestSessionSwitcherRowClickActivates checks a click on a session switcher row
// activates it exactly like Enter, instead of only moving the selection: the
// switcher closes and the switch is attempted. Outside daemon mode the attempt
// fails, and that failure notification is the proof the click called
// SwitchToSession rather than stopping at selection.
func TestSessionSwitcherRowClickActivates(t *testing.T) {
	m := &OS{Width: 120, Height: 40}
	m.ShowSessionSwitcher = true
	m.SessionSwitcherItems = []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: "alpha", Title: "alpha", IsCurrent: true},
		{Kind: sessiontree.KindSession, ID: "bravo", Title: "bravo"},
	}

	m.overlayRowClick("session", overlayRowHit{Idx: 1}, 0, 0)

	if m.ShowSessionSwitcher {
		t.Errorf("switcher still open after clicking a session row: the click selected instead of activating")
	}
	if !hasNotification(m, "Switch failed") {
		t.Errorf("no switch attempt recorded; notifications: %+v", m.Notifications)
	}
}

// TestSessionSwitcherCurrentRowClickCloses checks clicking the current session's
// row does not try to switch to itself: it reports and closes, mirroring Enter.
func TestSessionSwitcherCurrentRowClickCloses(t *testing.T) {
	m := &OS{Width: 120, Height: 40}
	m.ShowSessionSwitcher = true
	m.SessionSwitcherItems = []sessiontree.Node{
		{Kind: sessiontree.KindSession, ID: "alpha", Title: "alpha", IsCurrent: true},
	}

	m.overlayRowClick("session", overlayRowHit{Idx: 0}, 0, 0)

	if m.ShowSessionSwitcher {
		t.Errorf("switcher still open after clicking the current session row")
	}
	if hasNotification(m, "Switch failed") {
		t.Errorf("clicking the current session must not attempt a switch")
	}
	if !hasNotification(m, "Already on this session") {
		t.Errorf("expected the already-current notice; notifications: %+v", m.Notifications)
	}
}

// TestSidebarWindowRowOtherSessionAttemptsSwitch checks a sidebar window row
// pointing into another session routes through the session switch first. The
// switch fails outside daemon mode; the failure notification is the proof the
// row did not silently do nothing.
func TestSidebarWindowRowOtherSessionAttemptsSwitch(t *testing.T) {
	m := &OS{Width: 120, Height: 40, SessionName: "alpha"}

	m.sidebarFocusWindow(sidebarRowHit{
		Kind:        sidebarRowWindow,
		SessionID:   "bravo",
		WindowID:    "some-window",
		WindowIndex: -1,
	})

	if !hasNotification(m, "Switch failed") {
		t.Errorf("window row of another session did not attempt the switch; notifications: %+v", m.Notifications)
	}
}

// TestSidebarSessionRowClickAttemptsSwitch checks the full hit-test route for a
// non-current session row: a click at the row's recorded coordinates goes
// through SidebarClick to SwitchToSession. Outside daemon mode the switch
// fails; the failure notification proves the click reached the switch call.
func TestSidebarSessionRowClickAttemptsSwitch(t *testing.T) {
	m := &OS{Width: 120, Height: 40, SessionName: "alpha"}
	withSidebar(t, true, "left", 28)

	// A recorded hit for another session's row, as sidebarPanelLines would
	// record it inside the band.
	m.SidebarHits = []sidebarRowHit{{
		X0: 0, Y0: 4, X1: 28, Y1: 5,
		Kind:        sidebarRowSession,
		SessionID:   "bravo",
		WindowIndex: -1,
	}}

	if !m.SidebarClick(3, 4, false) {
		t.Fatalf("click inside the band was not consumed")
	}
	if !hasNotification(m, "Switch failed") {
		t.Errorf("session row click did not reach SwitchToSession; notifications: %+v", m.Notifications)
	}
}
