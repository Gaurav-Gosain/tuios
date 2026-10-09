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

// Nonce scope: the one rule for every verb that takes human_nonce.
//
// A session is a boundary. A presence made with a session (attach-presence
// with {"session": X}) proves the person for acts in session X only. A
// presence made with no session proves the person for every act its
// connection could already reach, which for an unrestricted connection is
// every session; a connection that restricts itself loses its presence
// (verbRestrictConnection). An attach nonce keeps its own rule: it proves
// the person for an act in any session, because the Inbox of one attached
// client answers for every session, and send-agent-message and paste-image
// also need the attach to be to the session they act in.
//
// A verb names the session its act lands in by ID. An act that lands in no
// one session (a host's item, or an item the verb cannot place) names "",
// and only an attach nonce or a presence with no session proves the person
// for it.

// humanNonceFor is the check every verb that takes human_nonce runs once it
// knows the session the act lands in: sessionID is that session's ID, "" for
// an act in no one session. It returns the id of the client that holds the
// nonce, for answered_by, and whether the nonce proves the person for this
// act from sender. See "Nonce scope" above, and docs/protocol.md, "Nonce
// scope".
func (d *Daemon) humanNonceFor(nonce, sessionID string, sender *connState) (string, bool) {
	h, ok := d.humanNonceHolder(nonce, "", sender)
	if !ok || !h.covers(sessionID) {
		return "", false
	}
	return h.clientID, true
}

// humanNonceHeld reports whether nonce belongs to a live attach or presence
// that sender may use, before the verb knows the session its act lands in.
// It lets a verb refuse a caller with no proof at all before it looks
// anything up. It is never the whole check: the verb calls humanNonceFor
// once it knows the session.
func (d *Daemon) humanNonceHeld(nonce string, sender *connState) bool {
	_, ok := d.humanNonceHolder(nonce, "", sender)
	return ok
}

// verifyHumanNonce is humanNonceFor for a verb that also needs an attach
// nonce to come from an attach to sessionID itself: a reply in a session's
// mail (send-agent-message) and paste-image. sessionID "" proves nothing.
func (d *Daemon) verifyHumanNonce(nonce, sessionID string, sender *connState) bool {
	if sessionID == "" {
		return false
	}
	h, ok := d.humanNonceHolder(nonce, sessionID, sender)
	return ok && h.covers(sessionID)
}

// sessionIDOf is the ID of the session named name, "" when there is none.
// A verb whose record names its session by name uses it for humanNonceFor.
func (d *Daemon) sessionIDOf(name string) string {
	if name == "" {
		return ""
	}
	if s, _ := d.manager.ResolveSession(name); s != nil {
		return s.ID
	}
	return ""
}

// humanHolder is the attach or presence a nonce belongs to.
type humanHolder struct {
	clientID string
	// presence marks a nonce from attach-presence, and presenceSession the
	// session ID it was made for, "" for every session.
	presence        bool
	presenceSession string
}

// covers reports whether the holder proves the person for an act in session
// sessionID ("" for an act in no one session).
func (h humanHolder) covers(sessionID string) bool {
	if !h.presence || h.presenceSession == "" {
		return true
	}
	return sessionID != "" && sessionID == h.presenceSession
}

// humanNonceHolder finds the attach or presence nonce belongs to, under the
// sender rules: the sender may act as the person, comes over the same kind
// of connection, and, where the kernel gave both pids, is the process that
// holds it. A nil sender is the daemon itself. attachSession, when set,
// also needs an attach nonce to come from an attach to that session.
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
		// A presence from attach-presence is held to the same sender rules
		// as an attach. Which sessions it covers is humanHolder.covers.
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
