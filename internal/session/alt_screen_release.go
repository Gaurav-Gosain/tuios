package session

// altScreenReleaser is an emulator that can drop its alternate screen's cells
// while the main screen is shown. The pure Go emulator is one. libghostty-vt
// manages its own memory and is not.
type altScreenReleaser interface {
	ReleaseAltScreen()
}

// releaseAltScreens drops the alternate screen's cells of every pane back on
// its main screen. It runs from the cleanup sweep rather than when a program
// leaves the alternate screen, so a pager launched over and over allocates
// its grid at most once a sweep.
func (d *Daemon) releaseAltScreens() {
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
			p.terminalMu.Unlock()
		}
	}
}
