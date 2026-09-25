package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The listener reads one event and stops, and all four messages come down the
// same channel, so every handler has to re-arm it. Two did not, and the first
// session resize or force refresh then stopped joins, leaves and every later
// resize with it: a phone turned sideways kept the columns it had in portrait,
// because the size the daemon had already recalculated was sitting in a channel
// with no reader.
func TestEveryClientEventKeepsTheListenerArmed(t *testing.T) {
	msgs := []tea.Msg{
		ClientJoinedMsg{ClientCount: 2},
		ClientLeftMsg{ClientCount: 1},
		SessionResizeMsg{Width: 120, Height: 40, ClientCount: 1},
		ForceRefreshMsg{Reason: "theme"},
	}

	for _, msg := range msgs {
		m := &OS{Settings: config.Global, ClientEventChan: make(chan ClientEvent, 1)}
		_, cmd := m.Update(msg)
		if cmd == nil {
			t.Errorf("%T returned no command, so nothing is listening for the next event", msg)
			continue
		}
		// The command it returned has to be the listener itself: put an event in
		// the channel and see it come back out as a message.
		m.ClientEventChan <- ClientEvent{Type: "resize", Width: 1, Height: 2, ClientCount: 3}
		if _, ok := cmd().(SessionResizeMsg); !ok {
			t.Errorf("%T returned a command that is not the client-event listener", msg)
		}
	}
}
