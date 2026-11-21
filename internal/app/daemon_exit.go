package app

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

const daemonExitQueue = 2

func (m *OS) daemonExitChan() chan tea.Msg {
	if m.DaemonExitChan == nil {
		m.DaemonExitChan = make(chan tea.Msg, daemonExitQueue)
	}
	return m.DaemonExitChan
}

// ListenForDaemonExit waits for the daemon event that ends this client. The
// DaemonDisconnectedMsg handler arms it again whenever the client survives a
// drop, such as a host link that reconnects, so the next drop is heard too.
func ListenForDaemonExit(ch chan tea.Msg) tea.Cmd {
	return listenDaemonExitUntil(nil, ch)
}

func listenDaemonExitUntil(stop <-chan struct{}, ch chan tea.Msg) tea.Cmd {
	return listenOnce(stop, ch, func(msg tea.Msg) tea.Msg { return msg })
}

// QueueDaemonDisconnect reports that the daemon connection is gone. It is safe
// to call from the daemon read-loop goroutine and never blocks it. It reports
// whether the queue was full and the event had to be dropped.
func (m *OS) QueueDaemonDisconnect(err error) bool {
	return m.queueDaemonExit(DaemonDisconnectedMsg{Err: err})
}

// QueueSessionEnded reports that the attached session was destroyed. Same
// goroutine rules as QueueDaemonDisconnect.
func (m *OS) QueueSessionEnded(name, reason string) bool {
	return m.queueDaemonExit(SessionEndedMsg{SessionName: name, Reason: reason})
}

func (m *OS) queueDaemonExit(msg tea.Msg) bool {
	ch := m.DaemonExitChan
	if ch == nil {
		return true
	}
	select {
	case ch <- msg:
		return false
	default:
		return true
	}
}

// ExitNotice is the message a remote client leaves on screen when it stops
// because its session or its daemon went away. It is "" for a normal exit.
//
// The local client prints its reason to the shell after the program returns.
// An SSH client and a browser client have no shell to print to: the SSH session
// closes and the browser tab shows a closed socket. So the reason has to be the
// last frame instead, which is why View drops the alternate screen for it.
func (m *OS) ExitNotice() string {
	if !m.RemoteClient {
		return ""
	}
	switch m.ExitReason {
	case ExitSessionKilled:
		name := m.SessionName
		if name == "" {
			name = "this session"
		} else {
			name = fmt.Sprintf("%q", name)
		}
		return "The session " + name + " stopped.\n" +
			"A different client or a command removed it.\n" +
			"Connect again to use one of the other sessions."

	case ExitNestedRefused:
		if m.DaemonClient != nil {
			return m.DaemonClient.NestedRefusal()
		}
		return ""

	case ExitDetached:
		if m.DaemonClient != nil {
			return m.DaemonClient.DetachedReason() + "\n" +
				"The session continues to run.\n" +
				"Connect again to attach to it."
		}
		return ""

	case ExitDaemonLost:
		return "tuios lost the connection to the daemon.\n" +
			"The daemon stopped, or it failed.\n" +
			"Connect again after the daemon starts."

	case ExitDaemonUpgraded:
		return "The daemon is being upgraded to " + m.upgradedTo() + ".\n" +
			"Your programs are still running.\n" +
			"Connect again to continue."

	case ExitHostLost:
		reason := m.hostReconnectReason
		if reason == "" {
			reason = "The link to " + m.AttachedHost + " closed."
		}
		return reason + "\n" +
			"tuios tried to connect again and stopped.\n" +
			"Connect again when the link is back."

	default:
		return ""
	}
}

func (m *OS) upgradedTo() string {
	if m.DaemonClient != nil {
		if v, ok := m.DaemonClient.UpgradedTo(); ok && v != "" {
			return v
		}
	}
	return "a new version"
}
