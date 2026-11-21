package app

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

var stateSyncTestHold = sync.OnceValue(func() time.Duration {
	return min(testknob.Millis("state_sync_hold_ms"), time.Minute)
})

const heldRemoteLimit = 2 * time.Second

type heldRemoteTimeoutMsg struct{ gen uint64 }

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

func (m *OS) noteStateSyncApplied(arrival uint64) tea.Cmd {
	m.stateSyncsApplied = max(m.stateSyncsApplied, arrival)
	return m.releaseHeldRemote(false)
}

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
