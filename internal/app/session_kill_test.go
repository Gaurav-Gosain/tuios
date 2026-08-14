package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Killing a session is the rail's one irreversible action, and it used to be
// offered on every session row while only ever acting on the attached one: the
// row carried its session all the way to the menu, and the actions the menu
// dispatched addressed the client's own session whatever row they came from.
// The rows were dimmed everywhere else to cover for it. These pin the fix, which
// is that the row decides and the confirmation says which session it means.

// killSessionOS is a client attached to session-1 (three panes) with another
// session, "docs", renamed to "Payments API" on screen and holding two panes,
// one of them mid-task. The listing is seeded rather than fetched: what is being
// tested is what the menu and the dialog do with a session this client is not
// in, not the wire underneath.
func killSessionOS(t *testing.T) *OS {
	t.Helper()
	m := sessionCloseOS(t)
	m.DaemonClient.UpdateSessionCache([]session.SessionInfo{
		{Name: "session-1", WindowCount: 3},
		{Name: "docs", DisplayName: "Payments API", WindowCount: 2, Windows: []session.WindowSummary{
			{ID: "d1", Title: "claude", AgentState: "working"},
			{ID: "d2", Title: "shell"},
		}},
	})
	return m
}

// menuActions is the runnable rows of a menu, dimmed ones marked, which is what
// the user can actually take.
func menuActions(items []ContextMenuItem) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		if it.Action != "" {
			out[it.Action] = !it.Dim
		}
	}
	return out
}

// TestEverySessionRowOffersToKillTheSessionItNames is the defect the report
// named: the kill rows only worked on the selected session.
func TestEverySessionRowOffersToKillTheSessionItNames(t *testing.T) {
	m := killSessionOS(t)

	title, items := m.sessionMenu("docs")
	if title != "Payments API" {
		t.Errorf("the menu is headed %q, want the label of the row it opened on", title)
	}
	runnable := menuActions(items)
	if !runnable["kill_session"] {
		t.Errorf("another session's menu offers no kill: %v", runnable)
	}
	// The two attached-session rows say what becomes of this client, which is
	// not a question another session's row can raise.
	for _, act := range []string{"kill_session_next", "kill_session_quit"} {
		if _, ok := runnable[act]; ok {
			t.Errorf("another session's menu carries %q, which acts on the attached session", act)
		}
	}

	_, items = m.sessionMenu("session-1")
	runnable = menuActions(items)
	if _, ok := runnable["kill_session"]; ok {
		t.Error("the attached session's menu carries the row meant for the others")
	}
	if !runnable["kill_session_next"] || !runnable["kill_session_quit"] {
		t.Errorf("the attached session lost its kill rows: %v", runnable)
	}
}

// TestTheConfirmationNamesAndCountsTheSessionItWouldKill: a destructive dialog
// that counts the wrong session's panes is worse than one that counts none.
func TestTheConfirmationNamesAndCountsTheSessionItWouldKill(t *testing.T) {
	m := killSessionOS(t)

	m.OpenSessionCloseFor("docs")
	body := sessionCloseText(t, m)
	if !strings.Contains(body, "Close Payments API?") {
		t.Errorf("the dialog did not name the session it would kill:\n%s", body)
	}
	if !strings.Contains(body, "2 panes, 1 agent still working") {
		t.Errorf("the dialog counted something other than that session:\n%s", body)
	}

	// The attached session is still counted off live state, and asking for it by
	// name is the same as asking for it with no target at all.
	m.OpenSessionCloseFor("session-1")
	if m.SessionCloseTarget != "" {
		t.Errorf("the attached session was held as target %q, want the empty target", m.SessionCloseTarget)
	}
	body = sessionCloseText(t, m)
	if !strings.Contains(body, "Close session-1?") || !strings.Contains(body, "3 panes") {
		t.Errorf("the attached session's dialog changed:\n%s", body)
	}
}

// TestKillingAnotherSessionLeavesThisClientAlone is the property that makes the
// action safe to offer at all.
func TestKillingAnotherSessionLeavesThisClientAlone(t *testing.T) {
	m := killSessionOS(t)
	panes, attached := len(m.Windows), m.SessionName

	m.OpenSessionCloseFor("docs")
	// No socket behind the seeded listing, so the kill itself has nowhere to go;
	// what is under test is that this client is not the thing being ended.
	m.DaemonClient = nil
	if cmd := m.SessionCloseActivate(SessionCloseRowClose); cmd != nil {
		t.Error("killing another session asked this client to quit")
	}
	if m.QuitRequested {
		t.Error("killing another session recorded a quit intent for this one")
	}
	if m.SessionName != attached || len(m.Windows) != panes {
		t.Errorf("this client came out as %q with %d panes, want %q with %d",
			m.SessionName, len(m.Windows), attached, panes)
	}
	if m.ShowSessionClose {
		t.Error("the dialog stayed up after it was answered")
	}
}

// TestKillingTheAttachedSessionStillQuits holds the other half: the path that
// ends this client is unchanged.
func TestKillingTheAttachedSessionStillQuits(t *testing.T) {
	m := killSessionOS(t)
	m.OpenSessionCloseFor("session-1")
	m.DaemonClient = nil // QuitSession would otherwise go to the wire
	if cmd := m.SessionCloseActivate(SessionCloseRowClose); cmd == nil {
		t.Error("closing the attached session did not quit")
	}
	if !m.QuitRequested {
		t.Error("closing the attached session recorded no quit intent")
	}
}

// TestTheRailsKillKeyOpensTheCursorRowsKill: the key and the right-click reach
// the same menu, so the key targets the row the cursor is on too.
func TestTheRailsKillKeyOpensTheCursorRowsKill(t *testing.T) {
	m := killSessionOS(t)
	m.IsDaemonSession = true
	m.DaemonClient = nil // the menu reads the cache; this fixture has no socket
	m.SidebarFocused = true
	m.SidebarNav = []sidebarNavRow{{Kind: sidebarRowSession, SessionID: "docs", WindowIndex: -1}}
	m.SidebarCursor = 0

	m.SidebarOpenCursorMenu(true)
	if m.ContextMenu == nil {
		t.Fatal("the kill key opened no menu")
	}
	if m.ContextMenu.SessionID != "docs" {
		t.Errorf("the menu carries session %q, want the cursor row's", m.ContextMenu.SessionID)
	}
	if got := m.ContextMenuSelectedAction(); got != "kill_session" {
		t.Errorf("the kill key selected %q, want the row's own kill", got)
	}
	if got := m.TakeMenuSession(); got != "docs" {
		t.Errorf("the action was dispatched against %q, want docs", got)
	}
}

// TestTheNextSessionIsAnIdentity: the kill-and-go-next row switches to what it
// is given, and a display name addresses nothing. Switching to a name no session
// answers to does not fail, it makes one.
func TestTheNextSessionIsAnIdentity(t *testing.T) {
	m := killSessionOS(t)
	if got := m.NextSessionName(); got != "docs" {
		t.Errorf("the next session is %q, want the identity docs", got)
	}
	for _, name := range m.otherSessionNames() {
		if name == "Payments API" {
			t.Error("the other sessions are listed by the name on screen, not the one they answer to")
		}
	}
}
