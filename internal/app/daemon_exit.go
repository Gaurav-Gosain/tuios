package app

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// daemonExitQueue is the capacity of DaemonExitChan. Two is enough for every
// case there is: the daemon can report the session gone and then drop the
// connection, and nothing after that matters, because the first of the two
// already quits the program.
const daemonExitQueue = 2

// DaemonExitChan carries the two daemon events that end a client: the attached
// session was destroyed, and the connection to the daemon was lost.
//
// Both arrive on the daemon read-loop goroutine, which must not touch the
// model, so they cross into the Bubble Tea event loop here. The local attach
// client has a *tea.Program at hand and Sends the messages straight in; the SSH
// and web hosts build the model before their program exists, so this channel is
// how they say the same thing. Without it those two clients kept rendering a
// dead session for ever.
//
// It is separate from ClientEventChan because that channel drops events when it
// is full, and a dropped join notice costs a toast while a dropped disconnect
// costs the user their exit.
func (m *OS) daemonExitChan() chan tea.Msg {
	if m.DaemonExitChan == nil {
		m.DaemonExitChan = make(chan tea.Msg, daemonExitQueue)
	}
	return m.DaemonExitChan
}

// ListenForDaemonExit waits for the daemon event that ends this client. It is
// armed once and never re-armed, because every message it can deliver quits.
func ListenForDaemonExit(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
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

	case ExitDaemonLost:
		return "tuios lost the connection to the daemon.\n" +
			"The daemon stopped, or it failed.\n" +
			"Connect again after the daemon starts."

	default:
		return ""
	}
}
