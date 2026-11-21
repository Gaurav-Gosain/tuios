package session

// SetHostShownProbe installs what reports whether the session is on a screen
// someone could be looking at: an attached TUI client whose host terminal has
// not reported losing focus. The daemon installs it on every session.
func (s *Session) SetHostShownProbe(fn func() bool) {
	s.hostShown.Store(&fn)
}

func (s *Session) paneHasFocus(windowID string) bool {
	probe := s.hostShown.Load()
	if probe == nil || !(*probe)() {
		return false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state != nil && s.state.FocusedWindowID == windowID
}

func (s *Session) focusedPTY() *PTY {
	s.stateMu.RLock()
	id := ""
	if s.state != nil {
		if w, ok := findWindowState(s.state, s.state.FocusedWindowID); ok {
			id = w.PTYID
		}
	}
	s.stateMu.RUnlock()
	if id == "" {
		return nil
	}
	return s.GetPTY(id)
}

func (p *PTY) noteFocusReporting(on bool) {
	was := p.focusReporting
	p.focusReporting = on
	if !on || was || p.hasFocus == nil {
		return
	}
	go func() {
		if p.hasFocus() {
			_, _ = p.Write([]byte(focusInReport))
		}
	}()
}

func (d *Daemon) sessionShown(sessionID string) bool {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && cs.attached
		f := cs.hostFocus
		cs.mu.Unlock()
		if match && f != focusOut {
			return true
		}
	}
	return false
}
