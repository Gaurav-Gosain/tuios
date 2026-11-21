package session

import (
	"cmp"

	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

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
	sortx.SortFunc(clients, func(a, b ClientInfo) int { return cmp.Compare(a.ClientID, b.ClientID) })
	return clients
}

func (d *Daemon) listSessions() []SessionInfo {
	sessions := d.manager.ListSessions()

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
