package session

import (
	"cmp"
	"slices"
)

// listClients snapshots daemon connections and their current sessions. The
// session names are resolved after clientsMu is released.
func (d *Daemon) listClients() []ClientInfo {
	d.clientsMu.RLock()
	clients := make([]ClientInfo, 0, len(d.clients))
	sessionIDs := make([]string, 0, len(d.clients))
	for _, cs := range d.clients {
		cs.mu.Lock()
		sessionIDs = append(sessionIDs, cs.sessionID)
		cs.mu.Unlock()
		clients = append(clients, ClientInfo{ClientID: cs.clientID, PID: cs.peerPID})
	}
	d.clientsMu.RUnlock()
	for i := range clients {
		clients[i].Session = d.sessionNameByID(sessionIDs[i])
	}
	slices.SortFunc(clients, func(a, b ClientInfo) int { return cmp.Compare(a.ClientID, b.ClientID) })
	return clients
}

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
