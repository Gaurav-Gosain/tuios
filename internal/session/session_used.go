package session

import "time"

// Which session the person used last.
//
// A bare tuios attach lands on the session the person used last. Activity
// (Session.LastActive) cannot say which one that is: a window an agent or a
// script opens bumps it, and so do keys a script sends through a client, a
// tape and the focus reports a terminal answers with. Every one of those
// reaches the pane as MsgInput, so the daemon cannot tell them from typing.
//
// The client can. It knows which input came from the terminal it runs in,
// and it reports that input, and only that, with MsgSessionUsed. The daemon
// takes the report from a client that may act as the person (see
// human_origin.go) and records it with Session.TouchUsed.

// usedInterval is the shortest gap between two MsgSessionUsed sends for the
// same session. A person typing sends one small message a second. A report
// for another session goes at once, so a switch is never late.
const usedInterval = time.Second

// usedSaveGap spaces the save marks TouchUsed sets. The saver writes every
// session on its own every 30 seconds whatever changed, so a use reaches disk
// within that anyway. What the mark adds is speed for the first use after an
// idle spell: it marks the session dirty, and the saver writes it within
// about two seconds. Uses that follow within the gap wait for the next
// regular write.
const usedSaveGap = 30 * time.Second

// ReportUsed tells the daemon the person at this client used its session. It
// sends nothing to a daemon that did not offer SessionUsed, and for one
// session at most one message per usedInterval.
func (c *TUIClient) ReportUsed(now time.Time) {
	// A view-only client's input reaches no pane, so it is not use either.
	if c == nil || !c.sessionUsed.Load() || c.viewOnly() {
		return
	}
	name := c.SessionName()
	c.usedMu.Lock()
	if name == c.usedSession && now.Sub(c.usedAt) < usedInterval {
		c.usedMu.Unlock()
		return
	}
	c.usedAt, c.usedSession = now, name
	c.usedMu.Unlock()
	msg, err := NewMessage(MsgSessionUsed, nil)
	if err != nil {
		return
	}
	_ = c.send(msg)
}

// handleSessionUsed records that the person used the client's session. A
// client that is not attached, or that runs inside a pane, is not the person,
// and its report is dropped.
func (d *Daemon) handleSessionUsed(cs *connState) error {
	cs.mu.Lock()
	sessionID, attached := cs.sessionID, cs.isTUIClient
	cs.mu.Unlock()
	if sessionID == "" || !attached || !d.mayActAsHuman(cs) {
		return nil
	}
	if session := d.manager.GetSessionByID(sessionID); session != nil {
		session.TouchUsed()
	}
	return nil
}
