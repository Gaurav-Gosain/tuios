package session

// reflowScratchReleaser is an emulator that keeps buffers from one reflow to
// the next. The pure Go emulator is one. libghostty-vt manages its own memory
// and is not.
type reflowScratchReleaser interface {
	ReleaseReflowScratch()
}

// releaseReflowScratch drops every pane's reflow buffers. It runs from the
// cleanup sweep rather than after a resize: a drag resizes at every motion
// step and reuses the buffers each time, and a pane resized again after the
// sweep pays one allocation.
func (d *Daemon) releaseReflowScratch() {
	for _, sess := range d.manager.AllSessions() {
		for _, id := range sess.ListPTYIDs() {
			p := sess.GetPTY(id)
			if p == nil {
				continue
			}
			p.terminalMu.Lock()
			if r, ok := p.terminal.(reflowScratchReleaser); ok {
				r.ReleaseReflowScratch()
			}
			p.terminalMu.Unlock()
		}
	}
}
