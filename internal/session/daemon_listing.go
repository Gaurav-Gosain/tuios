package session

// listSessions is the manager's listing plus the one fact only the daemon
// knows: whether a client is looking at each session.
//
// SessionInfo.Attached has been on the wire since the beginning and nothing
// ever set it, so every listing reported every session as detached. That made
// 'tuios ls' unable to say which session the user is already in, and the attach
// path's warning about sharing a screen with another client could never fire.
func (d *Daemon) listSessions() []SessionInfo {
	sessions := d.manager.ListSessions()

	// Lock order is d.clientsMu then cs.mu, which is the order every other
	// reader of both uses.
	attached := make(map[string]bool)
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		id := cs.sessionID
		cs.mu.Unlock()
		if id != "" {
			attached[id] = true
		}
	}
	d.clientsMu.RUnlock()

	for i := range sessions {
		sessions[i].Attached = attached[sessions[i].ID]
	}
	return sessions
}
