package session

import "fmt"

// Host terminal focus, per client.
//
// A terminal that supports focus events (DECSET 1004) tells the program in it
// when its window gains and loses focus. Each TUI client asks for them and
// reports every change here, so the daemon knows, per attached client, whether
// the person could be looking at it. A terminal that never reports focus
// leaves its client at focusUnknown, which is read as looking: that is what
// every rule did before the signal existed, so a terminal without focus events
// behaves as it always has.

// hostFocus is one client's host terminal focus.
type hostFocus uint8

const (
	focusUnknown hostFocus = iota
	focusIn
	focusOut
)

// Host focus as session-info reports it.
const (
	HostFocusFocused   = "focused"
	HostFocusUnfocused = "unfocused"
	HostFocusUnknown   = "unknown"
)

// ClientFocusPayload is the body of MsgClientFocus.
type ClientFocusPayload struct {
	Focused bool `json:"focused"`
}

// handleClientFocus records a client's host terminal focus.
func (d *Daemon) handleClientFocus(cs *connState, msg *Message) error {
	var p ClientFocusPayload
	if err := msg.ParsePayload(&p); err != nil {
		return fmt.Errorf("invalid client focus payload: %w", err)
	}
	f := focusOut
	if p.Focused {
		f = focusIn
	}
	cs.mu.Lock()
	cs.hostFocus = f
	cs.mu.Unlock()
	return nil
}

// sessionHostFocus sums up the host focus of the TUI clients attached to a
// session: focused when any of them has focus, unfocused when every one of
// them reported losing it, and unknown otherwise, which includes a session
// with no client and a client whose terminal never said.
func (d *Daemon) sessionHostFocus(sessionID string) string {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	clients, out := 0, 0
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && cs.attached
		f := cs.hostFocus
		cs.mu.Unlock()
		if !match {
			continue
		}
		clients++
		switch f {
		case focusIn:
			return HostFocusFocused
		case focusOut:
			out++
		}
	}
	if clients > 0 && out == clients {
		return HostFocusUnfocused
	}
	return HostFocusUnknown
}
