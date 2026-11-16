package session

// altScreenReleaser is an emulator that can drop its alternate screen's cells
// while the main screen is shown. reflowScratchReleaser is one that keeps
// buffers from one reflow to the next. The pure Go emulator is both.
// libghostty-vt manages its own memory and is neither.
type altScreenReleaser interface {
	ReleaseAltScreen()
}

type reflowScratchReleaser interface {
	ReleaseReflowScratch()
}

// releaseEmulatorBuffers drops, for every pane, the alternate screen's cells
// of a pane back on its main screen and the reflow buffers. It runs from the
// cleanup sweep rather than when a program leaves the alternate screen or
// after a resize: a pager launched over and over allocates its grid at most
// once a sweep, and a drag resizes at every motion step and reuses the
// buffers each time.
func (d *Daemon) releaseEmulatorBuffers() {
	for _, sess := range d.manager.AllSessions() {
		for _, id := range sess.ListPTYIDs() {
			p := sess.GetPTY(id)
			if p == nil {
				continue
			}
			p.terminalMu.Lock()
			if r, ok := p.terminal.(altScreenReleaser); ok {
				r.ReleaseAltScreen()
			}
			if r, ok := p.terminal.(reflowScratchReleaser); ok {
				r.ReleaseReflowScratch()
			}
			p.terminalMu.Unlock()
		}
	}
}
