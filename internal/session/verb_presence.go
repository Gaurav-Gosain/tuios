package session

import "encoding/json"

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
		sess, verr := d.resolveVerbSession(cs, p.Session)
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
