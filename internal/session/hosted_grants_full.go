//go:build !slim

package session

import "encoding/json"

// addressesHostedPane reports whether a call is one forwardHostedCall sends on
// to the machine that owns a hosted pane: a report verb whose address names a
// pane this machine runs for another machine.
func (d *Daemon) addressesHostedPane(verb string, params json.RawMessage) bool {
	field, ok := hostedCallVerbs[verb]
	if !ok || len(params) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(params, &fields) != nil {
		return false
	}
	var addr string
	if raw, ok := fields[field]; !ok || json.Unmarshal(raw, &addr) != nil {
		return false
	}
	return d.hostedPaneByAddress(addr) != nil
}

// hostedPaneOfPeer names the hosted pane the caller on cs runs in, by its
// process ancestry, or "" for none. It is asked only for a caller the pane
// tests place inside this daemon's panes but in none of its sessions' panes.
func (d *Daemon) hostedPaneOfPeer(cs *connState) string {
	if d.hostedPeer != nil {
		return d.hostedPeer(cs)
	}
	if d.peerPlaceOverride() != nil || cs.peerPID <= 0 || !d.connFromPane(cs) {
		return ""
	}
	pids := make(map[int]string)
	d.hostedPanesMu.Lock()
	for id, hp := range d.hostedPanes {
		if hp.cmd != nil && hp.cmd.Process != nil {
			pids[hp.cmd.Process.Pid] = id
		}
	}
	d.hostedPanesMu.Unlock()
	if len(pids) == 0 {
		return ""
	}
	for cur, depth := cs.peerPID, 0; depth < paneOriginMaxDepth && cur > 1; depth++ {
		if id, ok := pids[cur]; ok {
			return id
		}
		ppid, _, ok := readProcLineage(cur)
		if !ok {
			break
		}
		cur = ppid
	}
	return ""
}
