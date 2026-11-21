package session

// SessionLayoutGeneration is the newest layout generation this client has
// taken from the daemon, or zero when it has taken none.
func (c *TUIClient) SessionLayoutGeneration() uint64 {
	c.multiClientMu.RLock()
	defer c.multiClientMu.RUnlock()
	return c.sessionLayoutGen
}

func (s *Session) keepRectsOfStaleLayout(state *SessionState) bool {
	gen := state.LayoutGen
	state.LayoutGen = 0
	if gen == 0 || gen >= s.LayoutGeneration() {
		return false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	kept := false
	for i := range state.Windows {
		w := &state.Windows[i]
		cur, ok := findWindowState(s.state, w.ID)
		if !ok {
			continue
		}
		if w.X != cur.X || w.Y != cur.Y || w.Width != cur.Width || w.Height != cur.Height {
			w.X, w.Y, w.Width, w.Height = cur.X, cur.Y, cur.Width, cur.Height
			kept = true
		}
	}
	return kept
}
