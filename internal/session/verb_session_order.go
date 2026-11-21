package session

import (
	"encoding/json"
	"strings"
)

func (d *Daemon) verbSetSessionOrder(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Order       []string `json:"order"`
		SkipMissing bool     `json:"skip_missing"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Order) == 0 {
		return nil, invalidParam("order", "order is required and is the session names in the order to list them, e.g. [\"work\",\"notes\"]")
	}
	d.sessionOrderMu.Lock()
	defer d.sessionOrderMu.Unlock()
	ranked := make([]*Session, 0, len(p.Order))
	seen := make(map[*Session]bool, len(p.Order))
	var missing []string
	for _, raw := range p.Order {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, invalidParam("order", "a session name in order is empty")
		}
		sess, verr := d.resolveVerbSession(cs, name)
		if verr != nil {
			if p.SkipMissing && verr.Code == ErrVerbSessionNotFound {
				missing = append(missing, name)
				continue
			}
			return nil, verr
		}
		if seen[sess] {
			continue
		}
		seen[sess] = true
		ranked = append(ranked, sess)
	}
	for i, sess := range ranked {
		if sess.rank() == i+1 {
			continue
		}
		if err := sess.SetRank(i + 1); err != nil {
			return nil, newVerbError(ErrVerbInternal, "could not set the session order: "+err.Error())
		}
	}
	for _, sess := range d.manager.AllSessions() {
		if !seen[sess] && sess.rank() != 0 {
			if err := sess.SetRank(0); err != nil {
				return nil, newVerbError(ErrVerbInternal, "could not set the session order: "+err.Error())
			}
		}
	}
	d.unrankSavedSessions()
	d.broadcastSessionsChanged()
	names := make([]string, 0, len(ranked))
	for _, s := range d.listSessions() {
		names = append(names, s.Name)
	}
	res := map[string]any{"type": "session_order_set", "order": names}
	if len(missing) > 0 {
		res["missing"] = missing
	}
	return res, nil
}

func (d *Daemon) unrankSavedSessions() {
	names, err := ListResurrectableSessions()
	if err != nil {
		return
	}
	for _, name := range names {
		if d.manager.GetSession(name) != nil {
			continue
		}
		state, err := LoadResurrectionState(name)
		if err != nil || state.Rank == 0 {
			continue
		}
		state.Rank = 0
		if err := SaveSessionForResurrection(state); err != nil {
			LogError("Failed to take the rank out of saved session %q: %v", name, err)
		}
	}
}

func (d *Daemon) restoredRankLocked(rank int) int {
	if rank == 0 {
		return 0
	}
	for _, s := range d.manager.AllSessions() {
		if s.rank() == rank {
			return 0
		}
	}
	return rank
}
