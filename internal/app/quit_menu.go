package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

// QuitMenuKind names what a quit-menu row does when run.
type QuitMenuKind int

// Quit menu row kinds. The row set depends on the session state (see
// buildQuitMenuItems); the kind is what the input layer and the activation
// switch on, so a row's behavior never depends on its position.
const (
	// QuitDetach leaves the session running and quits this client.
	QuitDetach QuitMenuKind = iota
	// QuitSwitchSession closes the menu and opens go-to on the sessions.
	QuitSwitchSession
	// QuitKillGoNext kills the current session after switching to the next one,
	// so this client keeps running.
	QuitKillGoNext
	// QuitKillAndQuit kills the current session and quits the client.
	QuitKillAndQuit
	// QuitDeleteWorktree removes the current session's worktree, then the
	// session, then this client. Offered only when the session sits in a
	// worktree; the delete fails loud rather than quitting on a failure.
	QuitDeleteWorktree
	// QuitStandalone quits a non-daemon client (there is no session to keep).
	QuitStandalone
	// QuitCancel closes the menu and does nothing.
	QuitCancel
)

// QuitMenuItem is one row of the quit menu. Key is the accelerator shown in
// the hint column and matched by the key handler; Warn draws the label in the
// destructive color when running the row would lose work.
type QuitMenuItem struct {
	Label string
	Key   string
	Kind  QuitMenuKind
	Warn  bool
}

// OpenQuitMenu shows the quit menu, building its rows from the session state
// at this moment: daemon or standalone, and whether other sessions exist. The
// first row is always the safe default (detach in a daemon session).
func (m *OS) OpenQuitMenu() {
	others := m.otherSessionNames()
	m.QuitMenuItems = m.buildQuitMenuItems(others, m.anyForegroundProcess())
	m.QuitMenuOtherSessions = others
	m.quitList.Reset()
	m.ShowQuitMenu = true
}

// CloseQuitMenu dismisses the quit menu without running anything.
func (m *OS) CloseQuitMenu() {
	m.ShowQuitMenu = false
	m.quitList.Reset()
	m.QuitMenuItems = nil
	m.QuitMenuOtherSessions = nil
}

// NextSessionName returns the session the kill-and-go-next actions would land
// on: the first other session in the daemon's list, or "" when this is the
// last session.
func (m *OS) NextSessionName() string {
	others := m.otherSessionNames()
	if len(others) == 0 {
		return ""
	}
	return others[0]
}

func (m *OS) leaveTarget(live []string) string {
	if m.lastSession.host == m.attachedMachine() && slices.Contains(live, m.lastSession.name) {
		return m.lastSession.name
	}
	return live[0]
}

func (m *OS) otherSessionNames() []string {
	if m.DaemonClient == nil {
		return nil
	}
	current := m.sidebarCurrentSessionID()
	var out []string
	for _, name := range m.DaemonClient.AvailableSessionNames() {
		if name != current {
			out = append(out, name)
		}
	}
	return out
}

func (m *OS) anyForegroundProcess() bool {
	for _, w := range m.Windows {
		if w != nil && w.HasForegroundProcess() {
			return true
		}
	}
	return false
}

func (m *OS) buildQuitMenuItems(others []string, busy bool) []QuitMenuItem {
	if !m.IsDaemonSession {
		return []QuitMenuItem{
			{Label: "Quit", Key: "q", Kind: QuitStandalone, Warn: busy},
			{Label: "Cancel", Key: "esc", Kind: QuitCancel},
		}
	}
	items := []QuitMenuItem{{Label: "Detach", Key: "d", Kind: QuitDetach}}
	if len(others) > 0 {
		items = append(items,
			QuitMenuItem{Label: "Switch session...", Key: "s", Kind: QuitSwitchSession},
			QuitMenuItem{Label: "Kill session, go to next", Key: "x", Kind: QuitKillGoNext, Warn: busy},
			QuitMenuItem{Label: "Kill session and quit", Kind: QuitKillAndQuit, Warn: busy},
		)
	} else {
		items = append(items, QuitMenuItem{Label: "Kill and quit", Key: "x", Kind: QuitKillAndQuit, Warn: busy})
	}
	if m.sessionDeletableWorktree(m.SessionName) != nil {
		items = append(items, QuitMenuItem{Label: "Delete worktree and quit", Key: "w", Kind: QuitDeleteWorktree, Warn: true})
	}
	return items
}

// QuitMenuMove moves the quit menu selection by delta under the list rule.
func (m *OS) QuitMenuMove(delta int) {
	m.quitList.Move(delta, len(m.QuitMenuItems), listOverlayRows, m.listWraps(), nil)
}

// QuitMenuCursor is the quit menu's row under the cursor.
func (m *OS) QuitMenuCursor() int { return m.quitList.Cursor }

// QuitMenuIndexOfKind returns the first row matching any of the given kinds,
// or -1. Used by the accelerator keys, which address rows by what they do.
func (m *OS) QuitMenuIndexOfKind(kinds ...QuitMenuKind) int {
	for i, item := range m.QuitMenuItems {
		if slices.Contains(kinds, item.Kind) {
			return i
		}
	}
	return -1
}

// QuitMenuActivate runs the quit menu row at idx and closes the menu. It
// returns the command to hand back to Bubble Tea (tea.Quit for the rows that
// end this client, nil for the ones that keep it running).
func (m *OS) QuitMenuActivate(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.QuitMenuItems) {
		return nil
	}
	item := m.QuitMenuItems[idx]
	next := ""
	if len(m.QuitMenuOtherSessions) > 0 {
		next = m.QuitMenuOtherSessions[0]
	}
	m.CloseQuitMenu()

	switch item.Kind {
	case QuitDetach:
		return m.DetachClient()
	case QuitSwitchSession:
		m.OpenSessionSwitcher()
		return nil
	case QuitKillGoNext:
		return m.KillSessionGoNext(next)
	case QuitDeleteWorktree:
		return m.OpenSessionClose()
	case QuitKillAndQuit, QuitStandalone:
		m.QuitSession()
		return m.QuitCmd()
	}
	return nil
}

// DetachClient leaves the session running and quits this client, pushing state
// first so the session the user comes back to is the one they left. Outside a
// daemon session there is nothing to detach from and it returns nil; the
// caller decides what that means. This is the one detach implementation; the
// input layer's detach keybinding routes through it.
func (m *OS) DetachClient() tea.Cmd {
	if !m.IsDaemonSession {
		return nil
	}
	m.cancelHostOpen()
	m.SyncStateToDaemon()
	m.FireDetached()
	return m.QuitCmd()
}

// KillSessionGoNext switches this client to next and only then kills the
// session it left. The order matters: killing first would make the daemon
// announce the session ending back to this still-attached client, and that
// announcement races the switch (SessionEndedMsg quits the program). With no
// next session, or a switch that fails, it falls back to killing the current
// session outright, which quits.
func (m *OS) KillSessionGoNext(next string) tea.Cmd {
	old := m.SessionName
	oldLabel := m.SessionLabel(old)
	if next == "" || m.SwitchToSession(next) != nil {
		m.QuitSession()
		return m.QuitCmd()
	}
	if m.DaemonClient != nil {
		if err := m.DaemonClient.KillSessionByName(old); err != nil {
			m.ShowNotification("Kill failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
			return nil
		}
	}
	m.ShowNotification("Killed session: "+oldLabel, "success", m.Settings.NotificationDuration)
	return nil
}
