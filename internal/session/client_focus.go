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

// focusOutReport is what a terminal sends when its window loses focus and the
// program in it has turned on focus reporting (DECSET 1004).
const focusOutReport = "\x1b[O"

// handleClientFocus records a client's host terminal focus and, when that
// changes whether the session is on a screen someone could be looking at,
// tells the session's focused pane (see reportHostFocus).
func (d *Daemon) handleClientFocus(cs *connState, msg *Message) error {
	var p ClientFocusPayload
	if err := msg.ParsePayload(&p); err != nil {
		return fmt.Errorf("invalid client focus payload: %w", err)
	}
	f := focusOut
	if p.Focused {
		f = focusIn
	}
	d.hostFocusMu.Lock()
	defer d.hostFocusMu.Unlock()
	cs.mu.Lock()
	sessionID := cs.sessionID
	cs.mu.Unlock()
	was := sessionID != "" && d.sessionShown(sessionID)
	cs.mu.Lock()
	cs.hostFocus = f
	cs.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	if now := d.sessionShown(sessionID); now != was {
		d.reportHostFocus(sessionID, now)
	}
	return nil
}

// reportHostFocus sends the focused pane of a session a focus report (CSI I
// or CSI O) for a change of the host terminals' focus, when the program in it
// has turned on focus reporting. A person who moves from the terminal to
// another application leaves the pane as surely as one who moves to another
// pane, and an editor that saves on FocusLost counts on hearing it.
//
// The change is the session's, not one client's: the pane keeps focus while
// any host terminal showing the session has it, so a report goes out only when
// the last one loses focus and when the first one gets it back. A move from
// one pane to another is reported by the client that made it
// (reportFocusChange in internal/app).
func (d *Daemon) reportHostFocus(sessionID string, focused bool) {
	sess := d.manager.GetSessionByID(sessionID)
	if sess == nil {
		return
	}
	pty := sess.focusedPTY()
	if pty == nil || !pty.FocusReportingOn() {
		return
	}
	report := focusOutReport
	if focused {
		report = focusInReport
	}
	_, _ = pty.Write([]byte(report))
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
