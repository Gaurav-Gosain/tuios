package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// QuitMenuKind names what a quit-menu row does when run.
type QuitMenuKind int

// Quit menu row kinds. The row set depends on the session state (see
// buildQuitMenuItems); the kind is what the input layer and the activation
// switch on, so a row's behavior never depends on its position.
const (
	// QuitDetach leaves the session running and quits this client.
	QuitDetach QuitMenuKind = iota
	// QuitSwitchSession closes the menu and opens the session switcher.
	QuitSwitchSession
	// QuitKillGoNext kills the current session after switching to the next one,
	// so this client keeps running.
	QuitKillGoNext
	// QuitKillAndQuit kills the current session and quits the client.
	QuitKillAndQuit
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
	m.QuitMenuSelected = 0
	m.QuitMenuScroll = 0
	m.ShowQuitMenu = true
}

// CloseQuitMenu dismisses the quit menu without running anything.
func (m *OS) CloseQuitMenu() {
	m.ShowQuitMenu = false
	m.QuitMenuSelected = 0
	m.QuitMenuScroll = 0
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

// otherSessionNames lists the daemon sessions this client is not attached to,
// by identity, from the listing this client already caches.
//
// It used to collect the display title of a freshly fetched listing, and both
// halves of that were wrong. The title is the display name once a session has
// one, so the kill-and-go-next row switched to a name that addresses nothing,
// and switching to a name no session answers to makes one. The fetch was a
// blocking daemon round trip on the Update goroutine, taken to open a menu; the
// cache is what the rail's own rows are drawn from and is refreshed off that
// goroutine, so reading it is both cheaper and in agreement with the screen.
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

// anyForegroundProcess reports whether any window is running something beyond
// its shell, i.e. whether killing the session would lose work.
func (m *OS) anyForegroundProcess() bool {
	for _, w := range m.Windows {
		if w != nil && w.HasForegroundProcess() {
			return true
		}
	}
	return false
}

// buildQuitMenuItems assembles the rows for the current session state. Kill
// rows carry the warn color only when a pane is running a foreground process,
// so the color says "this loses work", not merely "this is a kill".
func (m *OS) buildQuitMenuItems(others []string, busy bool) []QuitMenuItem {
	if !m.IsDaemonSession {
		// Standalone: there is no session to detach from, so detach is not
		// offered. Quit is the default so pressing q twice still quits.
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
	return items
}

// QuitMenuMove moves the quit menu selection by delta, clamped to the rows.
func (m *OS) QuitMenuMove(delta int) {
	if len(m.QuitMenuItems) == 0 {
		return
	}
	m.QuitMenuSelected = clampInt(m.QuitMenuSelected+delta, 0, len(m.QuitMenuItems)-1)
}

// QuitMenuIndexOfKind returns the first row matching any of the given kinds,
// or -1. Used by the accelerator keys, which address rows by what they do.
func (m *OS) QuitMenuIndexOfKind(kinds ...QuitMenuKind) int {
	for i, item := range m.QuitMenuItems {
		for _, k := range kinds {
			if item.Kind == k {
				return i
			}
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
	case QuitKillAndQuit, QuitStandalone:
		m.QuitSession()
		return tea.Quit
	}
	return nil // QuitCancel
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
	m.SyncStateToDaemon()
	m.FireDetached()
	// Deliberately no Cleanup: the session outlives this client.
	return tea.Quit
}

// KillSessionGoNext switches this client to next and only then kills the
// session it left. The order matters: killing first would make the daemon
// announce the session ending back to this still-attached client, and that
// announcement races the switch (SessionEndedMsg quits the program). With no
// next session, or a switch that fails, it falls back to killing the current
// session outright, which quits.
func (m *OS) KillSessionGoNext(next string) tea.Cmd {
	old := m.SessionName
	// Read while it is still this client's own session: afterwards the label
	// belongs to the session switched to.
	oldLabel := m.SessionLabel(old)
	if next == "" || m.SwitchToSession(next) != nil {
		m.QuitSession()
		return tea.Quit
	}
	if m.DaemonClient != nil {
		if err := m.DaemonClient.KillSessionByName(old); err != nil {
			m.ShowNotification("Kill failed: "+err.Error(), "error", config.NotificationDuration*2)
			return nil
		}
	}
	m.ShowNotification("Killed session: "+oldLabel, "success", config.NotificationDuration)
	return nil
}
