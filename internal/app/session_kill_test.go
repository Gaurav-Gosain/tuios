package app

import (
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

// TestARowOnlyOffersWhatItCanMean: the rest of the session menu, held to the
// same rule as the kill rows. A row that names one session and acts on another
// is the defect whatever the action is.
func TestARowOnlyOffersWhatItCanMean(t *testing.T) {
	m := killSessionOS(t)

	_, items := m.sessionMenu("docs")
	runnable := menuActions(items)
	// The workspace switcher steers the session this client is in, and there is
	// only one of those, so on another session's row it would open the attached
	// session's workspaces under a title naming this one.
	if runnable["prefix_workspace_switcher"] {
		t.Error("another session's menu offers to switch its workspaces, which it cannot do")
	}
	if runnable["prefix_detach"] {
		t.Error("another session's menu offers to detach from a session this client is not in")
	}
	// The two that do mean something on any row: the accent belongs to the row's
	// own session, and the switcher is a chooser rather than an action on the row.
	if !runnable["set_session_accent"] || !runnable["prefix_session_switcher"] {
		t.Errorf("another session's menu lost the rows that work on any session: %v", runnable)
	}

	_, items = m.sessionMenu("session-1")
	runnable = menuActions(items)
	if !runnable["prefix_workspace_switcher"] || !runnable["prefix_detach"] {
		t.Errorf("the attached session's menu lost rows it can run: %v", runnable)
	}
}

// TestAMenuIsAboutThePaneTheFocusReached: the pane menu used to be built from
// whatever was focused after the row was asked for, so a row whose session
// could not be switched to opened the previous pane's menu under the clicked
// row's title, close row included.
func TestAMenuIsAboutThePaneTheFocusReached(t *testing.T) {
	m := killSessionOS(t)
	m.IsDaemonSession = true
	before := m.FocusedWindow

	// A pane of a session this client is not attached to and cannot switch to:
	// the fixture's listing has no socket behind it.
	m.DaemonClient = nil
	m.openSidebarContextMenu(sidebarRowHit{
		Kind: sidebarRowWindow, SessionID: "docs", WindowID: "d1", WindowIndex: -1,
	}, 0, 0)

	if m.ContextMenu != nil {
		t.Errorf("a row the focus never reached still opened a %q menu", m.ContextMenu.Title)
	}
	if m.FocusedWindow != before {
		t.Errorf("focus moved to %d without reaching the row", m.FocusedWindow)
	}

	// And the rail's own key does not then try to land a selection on it.
	m.SidebarFocused = true
	m.SidebarNav = []sidebarNavRow{{Kind: sidebarRowWindow, SessionID: "docs", WindowID: "d1", WindowIndex: -1}}
	m.SidebarCursor = 0
	m.SidebarOpenCursorMenu(true)
	if m.ContextMenu != nil {
		t.Error("the kill key opened a menu for a row it could not reach")
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
