package session

import "time"

const usedInterval = time.Second

const usedSaveGap = 30 * time.Second

// ReportUsed tells the daemon the person at this client used its session,
// for one session at most one message per usedInterval.
func (c *TUIClient) ReportUsed(now time.Time) {
	if c == nil || c.viewOnly() {
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
	d.agentNoteUse(cs, sessionID)
	d.displayNoteUse(cs, sessionID)
	return nil
}
