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
// there is a name to use.
func (m *OS) sessionCloseQuestion() string {
	if m.SessionName == "" {
		return "Close this session?"
	}
	return "Close " + printableTitle(m.SessionName) + "?"
}

// OpenSessionClose raises the close confirmation.
func (m *OS) OpenSessionClose() {
	m.SessionCloseSelected = SessionCloseRowCancel
	m.ShowSessionClose = true
}

// CloseSessionClose dismisses the confirmation, changing nothing.
func (m *OS) CloseSessionClose() {
	m.ShowSessionClose = false
	m.SessionCloseSelected = SessionCloseRowCancel
}

// SessionCloseMove moves the selection by delta, clamped to the rows.
func (m *OS) SessionCloseMove(delta int) {
	m.SessionCloseSelected = clampInt(m.SessionCloseSelected+delta, 0, sessionCloseRowCount-1)
}

// SessionCloseActivate runs the row at idx and dismisses the dialog. Closing
// goes through QuitSession, which is the one implementation of ending a
// session: it kills the daemon-side session where there is one and cleans up
// where there is not.
func (m *OS) SessionCloseActivate(idx int) tea.Cmd {
	m.CloseSessionClose()
	if idx != SessionCloseRowClose {
		return nil
	}
	m.QuitSession()
	return tea.Quit
}
