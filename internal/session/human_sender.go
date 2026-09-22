package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// Telling a real reply from the person apart from a claimed one.
//
// Any process that can open the daemon's socket can call send-agent-message
// with from=human, and the message was stored exactly like the person's own
// reply from the mail overlay. An agent reading its inbox could not tell the
// two apart, which makes "the human said yes" something any other agent can
// forge.
//
// The interim answer is a nonce per attach. The daemon puts a fresh secret in
// every attach reply. The TUI keeps it and passes it as human_nonce when it
// sends a reply from the mail overlay, which travels on a fresh verb
// connection rather than on the attach itself. The daemon stores a from=human
// message as verified_human only when the nonce matches a client that is
// attached, to the same session, over the same kind of connection, right now.
// Anything else from=human is stored as claimed_human.
//
// This is not an identity. Every process of the same user can attach, and an
// attached client could hand its nonce to anything. What it does establish is
// that the sender holds a live attach, which an agent calling the CLI from a
// pane does not, and that is what separates "the person at the keyboard
// answered" from "something typed from=human".
//
// The link path follows the same rule and adds nothing to trust. A client on
// another machine attached through a link got its nonce from this daemon, in
// an attach that arrived on the link socket, so its reply over the link
// verifies against that attach. A from=human send over the link with no nonce,
// or with one issued to a local attach, is claimed_human: the hub's own daemon
// relays the stream without reading it, so no flag in the request can speak for
// a check the hub did not make.

// humanNonceBytes is the nonce's length before hex encoding.
const humanNonceBytes = 16

// newHumanNonce returns a fresh random nonce, hex encoded.
func newHumanNonce() (string, error) {
	var b [humanNonceBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// verifyHumanNonce reports whether nonce belongs to a client attached to the
// session sessionID right now, over a connection of the same kind as the
// sender's: a nonce issued to an attach on the link socket verifies only a
// send on the link socket, and a local one only a local send.
func (d *Daemon) verifyHumanNonce(nonce, sessionID string, viaLink bool) bool {
	if nonce == "" || sessionID == "" {
		return false
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.attached && cs.isTUIClient && cs.sessionID == sessionID &&
			cs.viaLink == viaLink && cs.humanNonce != "" &&
			subtle.ConstantTimeCompare([]byte(cs.humanNonce), []byte(nonce)) == 1
		cs.mu.Unlock()
		if match {
			return true
		}
	}
	return false
}
