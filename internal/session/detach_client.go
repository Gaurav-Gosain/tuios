package session

import (
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
)

// Detaching other clients.
//
// tmux has attach -d and detach-client, and people who run one client per
// session (discussion #549) want the same here. Three paths take a client off
// its session while the session keeps running:
//
//   - tuios attach -d: the attach asks for it (AttachPayload.DetachOthers),
//     and every other client of the session is detached as this one joins.
//   - [daemon] single_client: the daemon does the same for every attach, so
//     the newest attach always wins.
//   - the detach-client verb: detach a client by id, every client of a
//     session, or every client of a session but the one used last.
//
// A detached client is told with MsgSessionEnded and Detached set, the way a
// nested client is ejected, and it exits with the reason as its last words
// rather than as an error. The connection stays open and the client closes it.
//
// Taking another person's client away is an admin action. From a pane the
// verb needs the admin grant (scopeDeny in verbScopes), and an attach from a
// pane already needs it for the binary protocol (checkGrantMessage).
//
// Ordering. Two clients that attach -d at the same moment must end with one
// of them attached and the other told why, never with both attached or with
// a notice that reaches a client before its own attach reply:
//
//   - Attaches to one session hold Session.attachMu from the moment the
//     client is placed through its reply, so attachSeq follows the order
//     they took it.
//   - A sweep takes off only clients fully attached to the session (their
//     reply sent, repliedSession) with a lower attachSeq. A client still
//     waiting for its reply would read the notice as the answer.
//   - After its reply, an attach checks Session.exclusiveSeq and takes
//     itself off when a newer exclusive attach landed meanwhile.
//
// Which attaches are exclusive: -d, or any attach under single_client, but
// not a view-only one (a read-only web viewer must not take the screen from
// the person) and not the attach a client makes on its own to get back a
// session after a host link dropped (AttachPayload.Reconnect).

// DetachedByAttachMessage is what a client detached by a newer attach shows.
const DetachedByAttachMessage = "Another client attached to this session."

// DetachedByCommandMessage is what a client detached by detach-client shows.
const DetachedByCommandMessage = "The tuios detach-client command detached this client."

// detachOthers takes every TUI client fully attached to sess, other than
// keep, off the session and tells each why. It returns their ids. With before
// above zero only clients that attached before that attachSeq are taken.
func (d *Daemon) detachOthers(sess *Session, keep *connState, reason string, before uint64) []string {
	return d.noticeDetached(d.markOthers(sess, keep, before), sess, reason)
}

// markOthers takes the clients detachOthers names off sess, and returns them
// without telling them. An attach calls it under Session.attachMu, and sends
// the notices once the lock is free (noticeDetached), so a client that is slow
// to read does not hold up the next attach. Each victim got its reply before
// it was marked (repliedSession), so the notice still comes after it.
func (d *Daemon) markOthers(sess *Session, keep *connState, before uint64) []*connState {
	var targets []*connState
	d.clientsMu.RLock()
	for _, c := range d.clients {
		if c == keep {
			continue
		}
		c.mu.Lock()
		ok := c.isTUIClient && c.sessionID == sess.ID && c.repliedSession == sess.ID &&
			(before == 0 || c.attachSeq < before)
		c.mu.Unlock()
		if ok {
			targets = append(targets, c)
		}
	}
	d.clientsMu.RUnlock()
	victims := targets[:0]
	for _, c := range targets {
		if d.detachClientFrom(c, sess.ID) {
			victims = append(victims, c)
		}
	}
	return victims
}

// noticeDetached tells each victim why it was taken off sess, and returns
// their ids, sorted.
func (d *Daemon) noticeDetached(victims []*connState, sess *Session, reason string) []string {
	ids := make([]string, 0, len(victims))
	for _, c := range victims {
		d.sendDetachedNotice(c, sess, reason)
		ids = append(ids, c.clientID)
	}
	slices.Sort(ids)
	return ids
}

// exclusiveAttach reports whether an attach detaches the other clients of
// its session. See the top of this file.
func (d *Daemon) exclusiveAttach(p *AttachPayload) bool {
	if p.ViewOnly || p.Reconnect {
		return false
	}
	return p.DetachOthers || d.singleClient.Load()
}

// ejectIfSuperseded takes cs off sess when an exclusive attach newer than its
// own reached the session.
func (d *Daemon) ejectIfSuperseded(cs *connState, sess *Session) {
	cs.mu.Lock()
	seq, on := cs.attachSeq, cs.sessionID == sess.ID
	cs.mu.Unlock()
	if on && sess.exclusiveSeq.Load() > seq {
		d.ejectDetached(cs, sess, DetachedByAttachMessage)
	}
}

// ejectDetached takes a client off sess and tells it why. It reports whether
// the client was fully attached to sess. The check and the detach are one
// step (detachClientFrom), so a client that moves to another session in
// between is left alone.
func (d *Daemon) ejectDetached(cs *connState, sess *Session, reason string) bool {
	if !d.detachClientFrom(cs, sess.ID) {
		return false
	}
	d.sendDetachedNotice(cs, sess, reason)
	return true
}

// sendDetachedNotice tells a client it was taken off sess, and why.
func (d *Daemon) sendDetachedNotice(cs *connState, sess *Session, reason string) {
	LogBasic("Detached client %s (pid %d) from session %s: %s", cs.clientID, cs.peerPID, sess.Name(), reason)
	_ = d.sendMessage(cs, MsgSessionEnded, &SessionEndedPayload{
		SessionName: sess.Name(),
		Reason:      reason,
		Detached:    true,
	})
}

// storeMax raises a to v when v is larger, so a slower attach that writes its
// sequence late never lowers it.
func storeMax(a *atomic.Uint64, v uint64) {
	for {
		cur := a.Load()
		if v <= cur || a.CompareAndSwap(cur, v) {
			return
		}
	}
}

// detachClientParams are the detach-client verb's parameters.
type detachClientParams struct {
	Client   string `json:"client"`
	Session  string `json:"session"`
	AllOther bool   `json:"all_other"`
}

// verbDetachClient detaches clients from their sessions. See the top of this
// file, and the verb's entry in verb_protocol.go for which clients it picks.
func (d *Daemon) verbDetachClient(cs *connState, params json.RawMessage) (any, *verbError) {
	var p detachClientParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	p.Client = strings.TrimSpace(p.Client)
	p.Session = strings.TrimSpace(p.Session)
	if p.Client != "" && p.Session != "" {
		return nil, invalidParam("session", "name a client or a session, not both")
	}

	var detached []string
	switch {
	case p.Client != "":
		target, verr := d.attachedClientByID(p.Client)
		if verr != nil {
			return nil, verr
		}
		sess := d.manager.GetSessionByID(connSessionID(target))
		if sess == nil {
			return nil, newVerbError(ErrVerbNeedsClient, "client "+echoName(p.Client)+" is not attached to a session")
		}
		if p.AllOther {
			detached = d.detachOthers(sess, target, DetachedByCommandMessage, 0)
		} else if d.ejectDetached(target, sess, DetachedByCommandMessage) {
			detached = []string{target.clientID}
		}
	case p.Session != "":
		sess, verr := d.resolveVerbSession(p.Session)
		if verr != nil {
			return nil, verr
		}
		// A session with no client has nothing to detach, which is not an
		// error: the session is already in the state the caller asked for.
		var keep *connState
		if p.AllOther {
			keep = d.findTUIClient(sess.ID)
		}
		if !p.AllOther || keep != nil {
			detached = d.detachOthers(sess, keep, DetachedByCommandMessage, 0)
		}
	default:
		sess, verr := d.detachDefaultSession(cs)
		if verr != nil {
			return nil, verr
		}
		newest := d.findTUIClient(sess.ID)
		if newest == nil {
			return nil, noClientOn(sess)
		}
		if p.AllOther {
			detached = d.detachOthers(sess, newest, DetachedByCommandMessage, 0)
		} else if d.ejectDetached(newest, sess, DetachedByCommandMessage) {
			detached = []string{newest.clientID}
		}
	}
	if detached == nil {
		detached = []string{}
	}
	return map[string]any{
		"type":     "clients_detached",
		"detached": detached,
	}, nil
}

// detachDefaultSession is the session detach-client acts on when the call
// names neither a client nor a session: the caller's pane's session, else the
// only session with a client.
func (d *Daemon) detachDefaultSession(cs *connState) (*Session, *verbError) {
	if fromPane, window := d.peerPane(cs); fromPane && window != "" {
		if sess := d.sessionHoldingWindow(window); sess != nil {
			return sess, nil
		}
	}
	shown := map[string]bool{}
	d.clientsMu.RLock()
	for _, c := range d.clients {
		c.mu.Lock()
		if c.isTUIClient && c.attached && c.sessionID != "" {
			shown[c.sessionID] = true
		}
		c.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	if len(shown) == 1 {
		for id := range shown {
			if sess := d.manager.GetSessionByID(id); sess != nil {
				return sess, nil
			}
		}
	}
	if len(shown) == 0 {
		return nil, newVerbError(ErrVerbNeedsClient, "no client is attached")
	}
	return nil, hintedVerbError(ErrVerbInvalidParams, "clients are attached to several sessions, so the session or the client must be named", &VerbHint{
		Param:   "session",
		Command: "tuios list-clients",
		Detail:  "Name the session with -s, or the client with --client.",
	})
}

// attachedClientByID finds the attached TUI client with the given id.
func (d *Daemon) attachedClientByID(id string) (*connState, *verbError) {
	var ids []string
	var found *connState
	d.clientsMu.RLock()
	for _, c := range d.clients {
		c.mu.Lock()
		ok := c.isTUIClient && c.attached
		c.mu.Unlock()
		if !ok {
			continue
		}
		ids = append(ids, c.clientID)
		if c.clientID == id {
			found = c
		}
	}
	d.clientsMu.RUnlock()
	if found == nil {
		slices.Sort(ids)
		return nil, hintedVerbError(ErrVerbInvalidParams, "no attached client has id "+echoName(id), &VerbHint{
			Param:     "client",
			Command:   "tuios list-clients",
			Available: ids,
		})
	}
	return found, nil
}

// noClientOn is the refusal for a session no client shows.
func noClientOn(sess *Session) *verbError {
	return hintedVerbError(ErrVerbNeedsClient, "no client is attached to session "+sess.Name(), &VerbHint{
		Command: "tuios list-clients",
	})
}
