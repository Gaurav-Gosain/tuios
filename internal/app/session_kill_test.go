package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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
	if runnable["prefix_workspace_switcher"] {
		t.Error("another session's menu offers to switch its workspaces, which it cannot do")
	}
	if runnable["prefix_detach"] {
		t.Error("another session's menu offers to detach from a session this client is not in")
	}
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
