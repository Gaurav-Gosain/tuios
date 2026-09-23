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
// The nonce alone does not say the sender is not an agent, since an agent can
// attach too. human_origin.go adds that: a process inside a pane of this
// daemon is issued no nonce, cannot use one, and is refused from=human outright.
//
// The link path follows the same rule and adds nothing to trust. A client on
// another machine attached through a link got its nonce from this daemon, in
// an attach that arrived on a link socket, so its reply over the link verifies
// against that attach. The hub's own daemon relays the stream without reading
// it, so no flag in the request can speak for a check the hub made. What does
// is the socket the stream arrives on: the hub marks the stream open when it
// checked the process that asked for it is not inside one of the hub's panes,
// and the proxy then dials the link-human socket rather than the plain one. An
// attach through the plain link socket is issued no nonce. A from=human send
// over a link with no nonce, or with one issued to a local attach, is
// claimed_human.

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
//
// Two more conditions come from human_origin.go. The sender itself must be
// allowed to act as the person, so a nonce that leaked into a pane is no use
// there. And where the kernel gave both pids, the sender must be the process
// that attached: the tuios client sends its reply on a fresh connection, but
// from the same process that holds the attach, so a nonce copied to another
// process does not verify. A nil sender is the daemon itself.
func (d *Daemon) verifyHumanNonce(nonce, sessionID string, sender *connState) bool {
	if sessionID == "" {
		return false
	}
	return d.matchHumanNonce(nonce, sessionID, sender)
}

// verifyAnyHumanNonce is verifyHumanNonce for a call that spans sessions, such
// as dismiss-attention: the attach the nonce came from may be to any session,
// and every other condition holds as it does for a reply.
func (d *Daemon) verifyAnyHumanNonce(nonce string, sender *connState) bool {
	return d.matchHumanNonce(nonce, "", sender)
}

// matchHumanNonce is the check behind both: sessionID empty matches an attach
// to any session.
func (d *Daemon) matchHumanNonce(nonce, sessionID string, sender *connState) bool {
	if nonce == "" {
		return false
	}
	viaLink, linkHuman, pid := false, false, 0
	if sender != nil {
		viaLink, linkHuman, pid = sender.viaLink, sender.linkHuman, sender.peerPID
	}
	if !d.mayActAsHuman(sender) {
		return false
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.attached && cs.isTUIClient && (sessionID == "" || cs.sessionID == sessionID) &&
			cs.viaLink == viaLink && cs.linkHuman == linkHuman && cs.humanNonce != "" &&
			(pid <= 0 || cs.peerPID <= 0 || cs.peerPID == pid) &&
			subtle.ConstantTimeCompare([]byte(cs.humanNonce), []byte(nonce)) == 1
		cs.mu.Unlock()
		if match {
			return true
		}
	}
	return false
}
