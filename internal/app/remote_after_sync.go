package app

import (
	"os"
	"strconv"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// A routed command runs after every state the daemon sent before it.
//
// The daemon writes a client its state pushes and its routed commands on one
// connection, in order (session.Daemon.sendInBroadcastOrder). The read loop
// hands them on through two channels, StateSyncChan and RemoteCommandChan,
// each with its own listener, so Update could take the command first. A verb
// that changes the session and then routes a command, as focus-window does
// when it reveals the pane, then showed the new focus on a client that still
// held the version before it. The person's next push from there, such as a
// focus key, carried that older version, and the daemon refused its focus as
// stale: the key did nothing.
//
// QueueStateSync numbers each state as it arrives, and QueueRemoteCommand
// gives each command the number of states that arrived before it. A command
// whose states are not applied yet waits in heldRemote, and runs once they
// are, in the order the commands came. heldRemoteLimit lets a held command go
// in any case, so a state that never reaches Update cannot keep a command
// back for ever.

// stateSyncTestHold is how long the listener keeps each state before Update
// takes it: TUIOS_TEST_STATE_SYNC_HOLD_MS, at most a minute. It lets an e2e
// test deliver a routed command to Update while the state the daemon sent
// before it is still on its way, which a loaded machine does now and then.
var stateSyncTestHold = sync.OnceValue(func() time.Duration {
	ms, _ := strconv.Atoi(os.Getenv("TUIOS_TEST_STATE_SYNC_HOLD_MS"))
	return time.Duration(min(max(ms, 0), 60000)) * time.Millisecond
})

// heldRemoteLimit is the longest a routed command waits for the states the
// daemon sent before it.
const heldRemoteLimit = 2 * time.Second

// heldRemoteTimeoutMsg lets every held command go. gen is the heldRemoteGen
// it was armed for, so a timer from a hold that has ended is dropped.
type heldRemoteTimeoutMsg struct{ gen uint64 }

// holdRemote reports whether msg must wait for a state that arrived before
// it, and keeps it if so. The first command held arms the timeout.
func (m *OS) holdRemote(msg RemoteCommandMsg) (bool, tea.Cmd) {
	if msg.released || (len(m.heldRemote) == 0 && msg.afterSync <= m.stateSyncsApplied) {
		return false, nil
	}
	m.heldRemote = append(m.heldRemote, msg)
	if len(m.heldRemote) > 1 {
		return true, nil
	}
	m.heldRemoteGen++
	gen := m.heldRemoteGen
	return true, tea.Tick(heldRemoteLimit, func(time.Time) tea.Msg { return heldRemoteTimeoutMsg{gen: gen} })
}

// noteStateSyncApplied records that the state that arrived as number arrival
// has been taken by Update, applied or not, and runs the held commands that
// waited for no later state.
func (m *OS) noteStateSyncApplied(arrival uint64) tea.Cmd {
	m.stateSyncsApplied = max(m.stateSyncsApplied, arrival)
	return m.releaseHeldRemote(false)
}

// releaseHeldRemote runs the held commands, oldest first, here in Update so
// that no other message comes between them. It stops at the first one still
// waiting, unless all is set.
func (m *OS) releaseHeldRemote(all bool) tea.Cmd {
	var cmds []tea.Cmd
	for len(m.heldRemote) > 0 && (all || m.heldRemote[0].afterSync <= m.stateSyncsApplied) {
		msg := m.heldRemote[0]
		m.heldRemote = m.heldRemote[1:]
		msg.released = true
		_, cmd := m.Update(msg)
		cmds = append(cmds, cmd)
	}
	if len(m.heldRemote) == 0 {
		m.heldRemote = nil
	}
	return tea.Sequence(cmds...)
}
