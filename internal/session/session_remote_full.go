//go:build !slim

package session

// hostLinkFact is one remote pane's link state for a snapshot.
type hostLinkFact struct {
	state string
	until int64
}

// liveHostLinks is the link state of every window on another machine whose
// link is not up, by PTY id.
func (s *Session) liveHostLinks() map[string]hostLinkFact {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	var out map[string]hostLinkFact
	for id, pty := range s.ptys {
		rp, ok := pty.pty.(*remotePane)
		if !ok {
			continue
		}
		state, until := rp.linkState()
		if state == "" {
			continue
		}
		if out == nil {
			out = make(map[string]hostLinkFact)
		}
		out[id] = hostLinkFact{state: state, until: until.Unix()}
	}
	return out
}
