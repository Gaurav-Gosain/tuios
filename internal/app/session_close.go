package app

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The confirmation in front of closing a session.
//
// It is raised every time, whatever the session is holding. A dialog that only
// appears when something is running is a dialog people learn to dismiss without
// reading, and the one time it carries news is the one time they have already
// stopped looking at it.
//
// What makes it worth the keystroke is the second line: the panes and the
// agents are counted off live state as the dialog draws, so it says what is
// about to be lost rather than warning in general. An agent mid-task or waiting
// on an answer is called out by name, because that is the case where the user
// either genuinely means it or has just made the mistake this dialog exists to
// catch.

// Session-close rows, in drawn order. Cancel is first and is what the dialog
// opens on: the destructive row is never the default.
const (
	// SessionCloseRowCancel dismisses without doing anything.
	SessionCloseRowCancel = iota
	// SessionCloseRowClose ends the session.
	SessionCloseRowClose
	sessionCloseRowCount
)

// sessionToll is what closing the session would take down.
type sessionToll struct {
	Panes   int
	Working int // agents mid-task
	Blocked int // agents waiting on the user
}

// sessionToll counts the live session. Panes are every window this client
// holds, across workspaces, since the session ends for all of them at once.
func (m *OS) sessionToll() sessionToll {
	t := sessionToll{Panes: len(m.Windows)}
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		switch w.AgentState {
		case string(session.AgentStateWorking):
			t.Working++
		case string(session.AgentStateNeedsInput):
			t.Blocked++
		}
	}
	return t
}

// SessionTollFor counts any session by identity. Another session's panes are
// read from the daemon listing this client already caches, which is the same
// read the rail's rows for that session are drawn from: a dialog that has to
// ask the daemon what it is about would block the Update goroutine to say what
// it is destroying.
//
// A session whose windows have not reached the cache yet counts zero panes,
// and the line the dialog turns on says so rather than inventing a number.
func (m *OS) SessionTollFor(sessionID string) sessionToll {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		return m.sessionToll()
	}
	var t sessionToll
	if m.DaemonClient == nil {
		return t
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		t.Panes++
		switch w.AgentState {
		case string(session.AgentStateWorking):
			t.Working++
		case string(session.AgentStateNeedsInput):
			t.Blocked++
		}
	}
	return t
}

// Line reads the toll back as the sentence the dialog turns on.
func (t sessionToll) Line() string {
	parts := []string{countOf(t.Panes, "pane")}
	if t.Working > 0 {
		parts = append(parts, countOf(t.Working, "agent")+" still working")
	}
	if t.Blocked > 0 {
		// Named apart from working: one is a task that would be thrown away, the
		// other is a task already stopped and asking for the user. It drops the
		// noun where the clause in front of it already said "agents", which is
		// what keeps both call-outs on the line instead of the second one being
		// the part that gets cut.
		blocked := countOf(t.Blocked, "agent")
		if t.Working > 0 {
			blocked = strconv.Itoa(t.Blocked)
		}
		parts = append(parts, blocked+" waiting on you")
	}
	if t.Working == 0 && t.Blocked == 0 {
		parts = append(parts, "no agent working")
	}
	return strings.Join(parts, ", ")
}

// countOf renders a count with its noun, pluralised by adding an s.
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// sessionCloseQuestion is the dialog's first line, naming the session where
// there is a name to use. It is the session's label rather than its identity,
// because the question is being asked of a person: a session renamed to
// "Payments API" is not recognisable by the name it is addressed by.
func (m *OS) sessionCloseQuestion() string {
	name := m.sessionCloseTarget()
	if name == "" {
		return "Close this session?"
	}
	return "Close " + printableTitle(m.SessionLabel(name)) + "?"
}

// sessionCloseTarget is the session the open dialog is about, resolved: the one
// it was raised on, or the attached one when it was raised without a target.
func (m *OS) sessionCloseTarget() string {
	if m.SessionCloseTarget != "" {
		return m.SessionCloseTarget
	}
	return m.SessionName
}

// OpenSessionClose raises the close confirmation for the attached session.
func (m *OS) OpenSessionClose() { m.OpenSessionCloseFor("") }

// OpenSessionCloseFor raises it for any session, named by identity. One dialog
// answers for all of them: it is the only place that counts what a close would
// take down, and a second one built for other sessions would be the same
// question asked with less information.
//
// The attached session resolves back to the empty target, so the path that
// quits this client is chosen by what the session IS rather than by which
// surface raised the dialog.
func (m *OS) OpenSessionCloseFor(sessionID string) {
	if sessionID == m.sidebarCurrentSessionID() {
		sessionID = ""
	}
	m.SessionCloseTarget = sessionID
	m.SessionCloseSelected = SessionCloseRowCancel
	m.ShowSessionClose = true
}

// CloseSessionClose dismisses the confirmation, changing nothing.
func (m *OS) CloseSessionClose() {
	m.ShowSessionClose = false
	m.SessionCloseTarget = ""
	m.SessionCloseSelected = SessionCloseRowCancel
}

// SessionCloseMove moves the selection by delta, clamped to the rows.
func (m *OS) SessionCloseMove(delta int) {
	m.SessionCloseSelected = clampInt(m.SessionCloseSelected+delta, 0, sessionCloseRowCount-1)
}

// SessionCloseActivate runs the row at idx and dismisses the dialog. Closing
// the attached session goes through QuitSession, which is the one
// implementation of ending the session this client is in: it kills the
// daemon-side session where there is one and cleans up where there is not.
//
// Any other session is killed where it stands. Nothing about this client
// changes: it stays attached to what it was attached to, and the rail loses a
// row when the daemon's next listing says so.
func (m *OS) SessionCloseActivate(idx int) tea.Cmd {
	target := m.SessionCloseTarget
	m.CloseSessionClose()
	if idx != SessionCloseRowClose {
		return nil
	}
	if target != "" {
		m.KillOtherSession(target)
		return nil
	}
	m.QuitSession()
	return tea.Quit
}

// KillOtherSession ends a session this client is not attached to, by identity.
//
// The kill is a daemon round trip that waits for the post-kill listing, so it
// runs off the Update goroutine: doing it inline parks input, rendering and
// socket draining for as long as the daemon takes. The outcome comes back as a
// SessionKilledMsg, which is where it is reported, since that is the goroutine
// that owns notifications.
func (m *OS) KillOtherSession(sessionID string) {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() || m.DaemonClient == nil {
		return
	}
	label, client, ch := m.SessionLabel(sessionID), m.DaemonClient, m.sessionKillChan()
	go func() {
		ch <- SessionKilledMsg{Label: label, Err: client.KillSessionByName(sessionID)}
	}()
}

// sessionKillChan is the buffered channel carrying kill results back to Update,
// made on first use so a client that never kills another session pays nothing.
func (m *OS) sessionKillChan() chan SessionKilledMsg {
	if m.PendingSessionKill == nil {
		m.PendingSessionKill = make(chan SessionKilledMsg, 4)
	}
	return m.PendingSessionKill
}
