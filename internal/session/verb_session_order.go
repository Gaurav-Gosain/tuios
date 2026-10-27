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
	for _, raw := range p.Order {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, invalidParam("order", "a session name in order is empty")
		}
		sess, verr := d.resolveVerbSession(cs, name)
		if verr != nil {
			return nil, verr
		}
		if seen[sess] {
			continue
		}
		seen[sess] = true
		ranked = append(ranked, sess)
	}
	for i, sess := range ranked {
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
	// Every rail redraws now, not at the listing push's next look: the person
	// who moved a session on one screen may be looking at the other.
	d.broadcastSessionsChanged()
	names := make([]string, 0, len(ranked))
	for _, s := range d.listSessions() {
		names = append(names, s.Name)
	}
	return map[string]any{"type": "session_order_set", "order": names}, nil
}

// restoredRank is the rank a session restored from saved state takes. Its
// saved rank stands unless a live session holds it: the order was set again
// while the session was not running, and two sessions with one rank would
// tie. It then comes back unplaced, after the placed sessions.
func (d *Daemon) restoredRank(rank int) int {
	if rank == 0 {
		return 0
	}
	d.sessionOrderMu.Lock()
	defer d.sessionOrderMu.Unlock()
	for _, s := range d.manager.AllSessions() {
		if s.rank() == rank {
			return 0
		}
	}
	return rank
}
