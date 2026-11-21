package session

import (
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
)

// DetachedByAttachMessage is what a client detached by a newer attach shows.
const DetachedByAttachMessage = "Another client attached to this session."

// DetachedByCommandMessage is what a client detached by detach-client shows.
const DetachedByCommandMessage = "The tuios detach-client command detached this client."

func (d *Daemon) detachOthers(sess *Session, keep *connState, reason string, before uint64) []string {
	return d.noticeDetached(d.markOthers(sess, keep, before), sess, reason)
}

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

func (d *Daemon) noticeDetached(victims []*connState, sess *Session, reason string) []string {
	ids := make([]string, 0, len(victims))
	for _, c := range victims {
		d.sendDetachedNotice(c, sess, reason)
		ids = append(ids, c.clientID)
	}
	slices.Sort(ids)
	return ids
}

func (d *Daemon) exclusiveAttach(p *AttachPayload) bool {
	if p.ViewOnly || p.Reconnect {
		return false
	}
	return p.DetachOthers || d.singleClient.Load()
}

func (d *Daemon) ejectIfSuperseded(cs *connState, sess *Session) {
	cs.mu.Lock()
	seq, on := cs.attachSeq, cs.sessionID == sess.ID
	cs.mu.Unlock()
	if on && sess.exclusiveSeq.Load() > seq {
		d.ejectDetached(cs, sess, DetachedByAttachMessage)
	}
}

func (d *Daemon) ejectDetached(cs *connState, sess *Session, reason string) bool {
	if !d.detachClientFrom(cs, sess.ID) {
		return false
	}
	d.sendDetachedNotice(cs, sess, reason)
	return true
}

func (d *Daemon) sendDetachedNotice(cs *connState, sess *Session, reason string) {
	LogBasic("Detached client %s (pid %d) from session %s: %s", cs.clientID, cs.peerPID, sess.Name(), reason)
	_ = d.sendMessage(cs, MsgSessionEnded, &SessionEndedPayload{
		SessionName: sess.Name(),
		Reason:      reason,
		Detached:    true,
	})
}

func storeMax(a *atomic.Uint64, v uint64) {
	for {
		cur := a.Load()
		if v <= cur || a.CompareAndSwap(cur, v) {
			return
		}
	}
}

type detachClientParams struct {
	Client   string `json:"client"`
	Session  string `json:"session"`
	AllOther bool   `json:"all_other"`
}

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
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
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

func noClientOn(sess *Session) *verbError {
	return hintedVerbError(ErrVerbNeedsClient, "no client is attached to session "+sess.Name(), &VerbHint{
		Command: "tuios list-clients",
	})
}
