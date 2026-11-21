package session

import (
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

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

const focusOutReport = "\x1b[O"

func (d *Daemon) handleClientFocus(cs *connState, msg *Message) error {
	var p ClientFocusPayload
	if err := msg.ParsePayload(&p); err != nil {
		return errfmt.Errorf("invalid client focus payload: %w", err)
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
