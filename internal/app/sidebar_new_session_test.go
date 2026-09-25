package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// daemonRailOS is a rail with a daemon client attached but no live connection:
// enough for the pill to be offered, which is what these tests are about.
func daemonRailOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := sidebarTestOS(t, w, h, "left")
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	m.SessionName = "session-0"
	return m
}

// TestNextSessionNameSkipsTakenNames keeps the rail from asking the daemon for a
// name it will reject, and matches the CLI's own scheme.
func TestNextSessionNameSkipsTakenNames(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	if got := m.nextSessionName(); got != "session-0" {
		t.Errorf("first name is %q, want session-0", got)
	}

	m.DaemonClient.UpdateSessionCache([]session.SessionInfo{
		{Name: "session-0"}, {Name: "session-1"}, {Name: "other"},
	})
	if got := m.nextSessionName(); got != "session-2" {
		t.Errorf("with session-0 and session-1 taken, got %q, want session-2", got)
	}
}

// TestNewSessionWithoutADaemonSaysSo rather than panicking on a nil client: the
// row is hidden in standalone, but the key is still bound.
func TestNewSessionWithoutADaemonSaysSo(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.SidebarNewSession()
	if len(m.Notifications) == 0 {
		t.Fatal("no daemon and no explanation")
	}
	if got := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(got, "daemon") {
		t.Errorf("notification %q does not mention the daemon", got)
	}
}
