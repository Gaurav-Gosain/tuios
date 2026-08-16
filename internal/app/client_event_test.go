package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestListenForClientEventsMapping verifies that each ClientEvent type is mapped
// to the matching Bubble Tea message so the daemon read-loop goroutine never
// mutates model state directly.
func TestListenForClientEventsMapping(t *testing.T) {
	tests := []struct {
		name  string
		event ClientEvent
		check func(t *testing.T, msg any)
	}{
		{
			name:  "joined",
			event: ClientEvent{Type: "joined", ClientID: "a", ClientCount: 2, Width: 80, Height: 24},
			check: func(t *testing.T, msg any) {
				m, ok := msg.(ClientJoinedMsg)
				if !ok {
					t.Fatalf("expected ClientJoinedMsg, got %T", msg)
				}
				if m.Width != 80 || m.Height != 24 || m.ClientCount != 2 {
					t.Fatalf("unexpected payload: %+v", m)
				}
			},
		},
		{
			name:  "left",
			event: ClientEvent{Type: "left", ClientID: "a", ClientCount: 1},
			check: func(t *testing.T, msg any) {
				if _, ok := msg.(ClientLeftMsg); !ok {
					t.Fatalf("expected ClientLeftMsg, got %T", msg)
				}
			},
		},
		{
			name:  "resize",
			event: ClientEvent{Type: "resize", ClientCount: 3, Width: 120, Height: 40},
			check: func(t *testing.T, msg any) {
				m, ok := msg.(SessionResizeMsg)
				if !ok {
					t.Fatalf("expected SessionResizeMsg, got %T", msg)
				}
				if m.Width != 120 || m.Height != 40 || m.ClientCount != 3 {
					t.Fatalf("unexpected payload: %+v", m)
				}
			},
		},
		{
			name:  "refresh",
			event: ClientEvent{Type: "refresh", Reason: "theme"},
			check: func(t *testing.T, msg any) {
				m, ok := msg.(ForceRefreshMsg)
				if !ok {
					t.Fatalf("expected ForceRefreshMsg, got %T", msg)
				}
				if m.Reason != "theme" {
					t.Fatalf("unexpected reason: %q", m.Reason)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan ClientEvent, 1)
			ch <- tc.event
			msg := ListenForClientEvents(ch)()
			tc.check(t, msg)
		})
	}
}

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
		m := &OS{ClientEventChan: make(chan ClientEvent, 1)}
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
