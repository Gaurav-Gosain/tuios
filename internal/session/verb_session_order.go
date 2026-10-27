package session

import (
	"encoding/json"
	"strings"
)

// The session order: one order of the sessions for the whole daemon, so that
// every client lists them the same way. A rail drag or a move up on one client
// sends set-session-order, the daemon ranks the sessions (SessionState.Rank),
// sorts its listing by the ranks, and pushes the listing to every client at
// once. It used to be each client's own preference, kept in its sidebar
// state file: a session moved on the phone over SSH stayed where it was on the
// desktop, and the desktop's next save wrote its own order back.

func (d *Daemon) verbSetSessionOrder(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Order []string `json:"order"`
		// SkipMissing leaves out a name that is no live session rather than
		// refusing the order. The rail sets it: the names it sends are the
		// ones it drew, and another client may have ended one since.
		SkipMissing bool `json:"skip_missing"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Order) == 0 {
		return nil, invalidParam("order", "order is required and is the session names in the order to list them, e.g. [\"work\",\"notes\"]")
	}
	// One order at a time, from the names to the last rank, so the ranks of
	// two clients' orders cannot interleave.
	d.sessionOrderMu.Lock()
	defer d.sessionOrderMu.Unlock()
	// Resolve every name before ranking any, so a typo changes nothing.
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
	// A session already in its place is left alone: each rank set is a
	// state change pushed to the session's clients and saved, so an order
	// that moves one session writes one or two, not every session.
	for i, sess := range ranked {
		if sess.rank() == i+1 {
			continue
		}
		if err := sess.SetRank(i + 1); err != nil {
			return nil, newVerbError(ErrVerbInternal, "could not set the session order: "+err.Error())
		}
	}
	// A session the list leaves out goes after the named ones, in creation
	// order, as one made after the order was set does.
	for _, sess := range d.manager.AllSessions() {
		if !seen[sess] && sess.rank() != 0 {
			if err := sess.SetRank(0); err != nil {
				return nil, newVerbError(ErrVerbInternal, "could not set the session order: "+err.Error())
			}
		}
	}
	// A saved session that is not running is in no order the person set
	// now. Its file keeps the rank it had, which a live session may hold by
	// the time it comes back, and a restore in name order would hand the
	// place to it. Take the rank out of the file.
	d.unrankSavedSessions()
	// Every rail redraws now, not at the listing push's next look: the person
	// who moved a session on one screen may be looking at the other.
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

// unrankSavedSessions takes the rank out of the saved state of every session
// that is not live. A file that cannot be read or written is left as it is:
// restoredRank still keeps it from tying with a live session.
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

// restoredRankLocked is the rank a session restored from saved state takes.
// Its saved rank stands unless a live session holds it, and the session then
// comes back unplaced, after the placed sessions: two sessions with one rank
// would tie. The caller holds sessionOrderMu until the session is made, so an
// order set in between cannot hand the rank out again.
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
