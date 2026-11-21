package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

const humanNonceBytes = 16

func newHumanNonce() (string, error) {
	var b [humanNonceBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (d *Daemon) humanNonceFor(nonce, sessionID string, sender *connState) (string, bool) {
	h, ok := d.humanNonceHolder(nonce, "", sender)
	if !ok || !h.covers(sessionID) {
		return "", false
	}
	return h.clientID, true
}

func (d *Daemon) humanNonceHeld(nonce string, sender *connState) bool {
	_, ok := d.humanNonceHolder(nonce, "", sender)
	return ok
}

func (d *Daemon) verifyHumanNonce(nonce, sessionID string, sender *connState) bool {
	if sessionID == "" {
		return false
	}
	h, ok := d.humanNonceHolder(nonce, sessionID, sender)
	return ok && h.covers(sessionID)
}

func (d *Daemon) sessionIDOf(name string) string {
	if name == "" {
		return ""
	}
	if s, _ := d.manager.ResolveSession(name); s != nil {
		return s.ID
	}
	return ""
}

type humanHolder struct {
	clientID        string
	presence        bool
	presenceSession string
}

func (h humanHolder) covers(sessionID string) bool {
	if !h.presence || h.presenceSession == "" {
		return true
	}
	return sessionID != "" && sessionID == h.presenceSession
}

func (d *Daemon) humanNonceHolder(nonce, attachSession string, sender *connState) (humanHolder, bool) {
	if nonce == "" {
		return humanHolder{}, false
	}
	viaLink, linkHuman, pid := false, false, 0
	if sender != nil {
		viaLink, linkHuman, pid = sender.viaLink, sender.linkHuman, sender.peerPID
	}
	if !d.mayActAsHuman(sender) {
		return humanHolder{}, false
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		same := cs.viaLink == viaLink && cs.linkHuman == linkHuman &&
			(pid <= 0 || cs.peerPID <= 0 || cs.peerPID == pid)
		attach := cs.attached && cs.isTUIClient && (attachSession == "" || cs.sessionID == attachSession) &&
			cs.humanNonce != "" && subtle.ConstantTimeCompare([]byte(cs.humanNonce), []byte(nonce)) == 1
		presence := cs.presenceNonce != "" &&
			subtle.ConstantTimeCompare([]byte(cs.presenceNonce), []byte(nonce)) == 1
		h := humanHolder{clientID: cs.clientID, presence: presence && !attach, presenceSession: cs.presenceSession}
		cs.mu.Unlock()
		if same && (attach || presence) {
			return h, true
		}
	}
	return humanHolder{}, false
}
