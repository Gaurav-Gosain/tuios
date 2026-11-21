package session

type altScreenReleaser interface {
	ReleaseAltScreen()
}

type reflowScratchReleaser interface {
	ReleaseReflowScratch()
}

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
