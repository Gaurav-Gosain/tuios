package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
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
	m := sidebarTestOS(t, 120, 40, "left") // no DaemonClient
	if m.SidebarCanCreateSession() {
		t.Fatal("standalone reported it can create sessions")
	}
	sidebarText(t, m)
	if _, ok := newSessionHit(m); ok {
		t.Error("standalone drew the new-session pill")
	}
}

// TestNewSessionSitsOnTheSessionsHeader puts the control on the section it
// makes another of. It used to be a "+ new" pinned to the rail's bottom edge,
// which put it directly under the agents block and read as "new agent". The
// collapsed strip stacks its own "+" above its toggle instead; its own test
// covers that.
func TestNewSessionSitsOnTheSessionsHeader(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{
		{"full", 120, 40},
		{"narrow", 80, 24},
	} {
		t.Run(size.name, func(t *testing.T) {
			m := daemonRailOS(t, size.w, size.h)
			lines, _ := m.sidebarPanelLines()

			hit, ok := newSessionHit(m)
			if !ok {
				t.Fatal("no new-session control with a daemon attached")
			}
			row := hit.Y0 - m.GetTopMargin()
			drawn := ansi.Strip(lines[row])
			if !strings.Contains(drawn, "sessions") {
				t.Errorf("the control is on row %d, %q; want the sessions header", row, drawn)
			}
			if !strings.Contains(drawn, sidebarAddGlyph(&config.Global)) {
				t.Errorf("the sessions header reads %q, want it to carry the add glyph", drawn)
			}
			// The header is the rail's first line, so the control is above every
			// row it could be mistaken for a member of.
			if row != 0 {
				t.Errorf("the sessions header is on row %d, want the rail's first", row)
			}

			// And the cursor can reach it: it is the nav list's first row, drawn
			// before the sessions beneath it.
			if len(m.SidebarNav) == 0 || m.SidebarNav[0].Kind != sidebarRowNewSession {
				t.Errorf("the nav list opens with %+v, want the add control", m.SidebarNav)
			}
		})
	}
}

// TestNewTerminalSitsOnTheTerminalsHeader is the same rule for the other
// section: the "+" that makes a pane is on the list of panes.
func TestNewTerminalSitsOnTheTerminalsHeader(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	lines, _ := m.sidebarPanelLines()

	var hit sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowNewWindow {
			hit = h
		}
	}
	if hit.X1 == 0 {
		t.Fatal("the terminals header drew no add control")
	}
	drawn := ansi.Strip(lines[hit.Y0-m.GetTopMargin()])
	if !strings.Contains(drawn, "terminals") || !strings.Contains(drawn, sidebarAddGlyph(&config.Global)) {
		t.Errorf("the control is on %q, want the terminals header carrying the add glyph", drawn)
	}
}

// TestFooterKeepsOnlyTheToggle: two affordances for one action is worse than
// one in the wrong place, so the footer's copy went rather than being kept.
func TestFooterKeepsOnlyTheToggle(t *testing.T) {
	m := daemonRailOS(t, 120, 40)
	lines, _ := m.sidebarPanelLines()

	if got := ansi.Strip(lines[len(lines)-1]); strings.Contains(got, "new") {
		t.Errorf("the footer still reads %q", got)
	}
	hit, _ := newSessionHit(m)
	last := m.GetTopMargin() + len(lines) - 1
	if hit.Y0 == last {
		t.Error("the add control is still on the rail's bottom line")
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
	m := sidebarTestOS(t, 120, 40, "left")
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
	m := sidebarTestOS(t, 120, 40, "left")
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
