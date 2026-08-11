package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// daemonRailOS is a rail with a daemon client attached but no live connection:
// enough for the pill to be offered, which is what these tests are about.
func daemonRailOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := bandTestOS(t, w, h, "left")
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	m.SessionName = "session-0"
	return m
}

func newSessionHit(m *OS) (sidebarRowHit, bool) {
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowNewSession {
			return h, true
		}
	}
	return sidebarRowHit{}, false
}

// TestNewSessionPillIsHiddenInStandalone: a control that can never work is
// noise, so standalone does not draw it at all.
func TestNewSessionPillIsHiddenInStandalone(t *testing.T) {
	m := bandTestOS(t, 120, 40, "left") // no DaemonClient
	if m.SidebarCanCreateSession() {
		t.Fatal("standalone reported it can create sessions")
	}
	sidebarText(t, m)
	if _, ok := newSessionHit(m); ok {
		t.Error("standalone drew the new-session pill")
	}
}

// TestNewSessionPillSitsAfterTheSessions puts the pill where its result will
// appear: new sessions append, so the row that makes one goes last.
func TestNewSessionPillSitsAfterTheSessions(t *testing.T) {
	for _, size := range []struct {
		name  string
		w, h  int
		label string
	}{
		// The + sits on the session rows' glyph column and the label on their
		// name spine, so the two are two cells apart rather than adjacent.
		{"full", 120, 40, "+  new session"},
		{"narrow", 80, 24, "+  new"},
		{"glyph", 51, 37, "+"},
	} {
		t.Run(size.name, func(t *testing.T) {
			m := daemonRailOS(t, size.w, size.h)
			lines, _ := m.sidebarPanelLines()

			hit, ok := newSessionHit(m)
			if !ok {
				t.Fatal("no new-session row with a daemon attached")
			}
			if got := ansi.Strip(lines[hit.Y0-m.GetTopMargin()]); !strings.Contains(got, size.label) {
				t.Errorf("row reads %q, want it to carry %q", got, size.label)
			}

			// Nothing from the sessions tree may be drawn below it.
			for _, h := range m.SidebarHits {
				if h.Kind == sidebarRowSession && h.Y0 > hit.Y0 {
					t.Errorf("session row at y=%d sits below the pill at y=%d", h.Y0, hit.Y0)
				}
			}

			// And the cursor can reach it: the last nav row is the pill.
			last := m.SidebarNav[len(m.SidebarNav)-1]
			if last.Kind != sidebarRowNewSession {
				t.Errorf("last nav row is kind %v, want the new-session pill", last.Kind)
			}
		})
	}
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
	m := bandTestOS(t, 120, 40, "left")
	m.SidebarNewSession()
	if len(m.Notifications) == 0 {
		t.Fatal("no daemon and no explanation")
	}
	if got := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(got, "daemon") {
		t.Errorf("notification %q does not mention the daemon", got)
	}
}

// TestNewSessionPillActivatesByClickAndByKey proves both devices reach the same
// method. Without a live daemon the call fails at the wire, which is fine: what
// is under test is that the row routes there at all rather than sitting inert.
func TestNewSessionPillActivatesByClickAndByKey(t *testing.T) {
	m := bandTestOS(t, 120, 40, "left")
	sidebarText(t, m)

	// The pill is hidden here, so drive the routing through a synthetic row of
	// its kind: the switch arms are what must agree.
	m.SidebarNav = []sidebarNavRow{{Kind: sidebarRowNewSession, WindowIndex: -1}}
	m.SidebarCursor = 0
	if exit := m.SidebarActivateCursor(); exit {
		t.Error("the pill asked to leave the rail")
	}
	byKey := len(m.Notifications)

	m.SidebarHits = []sidebarRowHit{{X0: 0, X1: 10, Y0: m.GetTopMargin(), Y1: m.GetTopMargin() + 1, Kind: sidebarRowNewSession, WindowIndex: -1}}
	if !m.SidebarClick(1, m.GetTopMargin(), false) {
		t.Fatal("a click on the pill was not consumed")
	}
	if len(m.Notifications) <= byKey {
		t.Error("a click on the pill did nothing the keyboard did")
	}
}
