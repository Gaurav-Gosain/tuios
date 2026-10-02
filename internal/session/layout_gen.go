package session

// Which layout a push's rectangles were tiled in.
//
// The daemon settles the panes' box (the session's size and the chrome
// reserve) and numbers each answer with a layout generation (see
// SettleLayout). A client lays its panes out in the box of the generation it
// last took, and its push says which one that was in SessionState.LayoutGen.
//
// Two clients of different widths can make the daemon answer several times
// in a row: a session resize moves the wider client's rail across a
// breakpoint, its new reserve moves the box, and so on until it settles.
// Each client pushes after each answer, so a push tiled in an earlier box
// can land after one tiled in the last box. Taken as sent, it left the
// daemon holding rectangles that no client drew. Its rectangles are now kept
// out: the session keeps the ones it holds, and the client's own push for
// the newer generation brings the right ones.

// SessionLayoutGeneration is the newest layout generation this client has
// taken from the daemon, or zero when it has taken none.
func (c *TUIClient) SessionLayoutGeneration() uint64 {
	c.multiClientMu.RLock()
	defer c.multiClientMu.RUnlock()
	return c.sessionLayoutGen
}

// keepRectsOfStaleLayout puts the session's own rectangles on every window
// of a push that was tiled in a layout generation older than the session's.
// A push with no generation is from a client too old to say, and is taken as
// sent, as every push was before. Windows the session does not hold keep the
// rectangles the push gives them. The generation is cleared either way: it
// describes the push, not the session.
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
