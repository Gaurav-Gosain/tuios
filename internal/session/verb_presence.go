package session

import "encoding/json"

// attach-presence: the person's nonce without a screen.
//
// respond, reply-approval, answer-ask and dismiss-attention act as the person,
// and the proof they take is the human_nonce of a client attached now
// (human_sender.go). A phone that only answers the Inbox has no screen to
// attach: a full attach would size the session to the phone and count as a
// client looking at the focused pane, which makes request-approval answer
// viewed instead of holding the prompt for the Inbox.
//
// A presence is the proof and nothing else. The connection that asks for one
// gets a nonce that verifies by every rule an attach nonce does: only from a
// caller that may act as the person (outside every pane here, and over a link
// only on the link-human socket), only over the same kind of connection, and,
// where the kernel names pids, only from the process that holds it. It is
// never attached, so nothing that measures, focuses or speaks to a session's
// clients counts it, and it ends when its connection closes.

// verbAttachPresence registers a screenless presence on this connection.
func (d *Daemon) verbAttachPresence(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs == nil {
		return nil, newVerbError(ErrVerbForbidden, "attach-presence needs a connection")
	}
	if !d.mayActAsHuman(cs) {
		return nil, hintedVerbError(ErrVerbForbidden, "attach-presence is refused: the caller runs inside a pane of this daemon, or came over a link stream the hub did not vouch for", &VerbHint{
			Detail: "No presence was made. Only the person, from outside every pane, can hold one. Over a link, open the stream with human set to true.",
		})
	}
	sessionID, sessionName := "", ""
	if p.Session != "" {
		sess, verr := d.resolveVerbSession(p.Session)
		if verr != nil {
			return nil, verr
		}
		sessionID, sessionName = sess.ID, sess.Name()
	}
	nonce, err := newHumanNonce()
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not make a nonce: "+err.Error())
	}
	cs.mu.Lock()
	cs.presenceNonce = nonce
	cs.presenceSession = sessionID
	cs.mu.Unlock()
	LogBasic("Client %s holds a presence (session %q)", cs.clientID, sessionName)
	out := map[string]any{
		"type":        "presence",
		"human_nonce": nonce,
		"client_id":   cs.clientID,
	}
	if sessionName != "" {
		out["session"] = sessionName
	}
	return out, nil
}
