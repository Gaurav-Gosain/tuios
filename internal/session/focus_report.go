package session

// Focus report on enable.
//
// A program that turns on focus event reporting (DECSET 1004) expects to be
// told at once whether it has focus: xterm, kitty and ghostty all send the
// current state on the set, so the program does not have to guess until the
// focus next changes. The daemon's emulator answers for the pane, as it does
// every other query, and client emulators drain their responses, so the report
// goes out once.
//
// Only the focused state is reported on the set. A pane that is not told
// assumes it has focus, which is what it did before. Later focus changes are
// reported by the client as they happen (reportFocusChange in internal/app),
// so this covers only the moment the mode goes on.

// SetHostShownProbe installs what reports whether the session is on a screen
// someone could be looking at: an attached TUI client whose host terminal has
// not reported losing focus. The daemon installs it on every session.
func (s *Session) SetHostShownProbe(fn func() bool) {
	s.hostShown.Store(&fn)
}

// paneHasFocus reports whether windowID is the pane keys go to on a screen
// someone could be looking at.
func (s *Session) paneHasFocus(windowID string) bool {
	probe := s.hostShown.Load()
	if probe == nil || !(*probe)() {
		return false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state != nil && s.state.FocusedWindowID == windowID
}

// noteFocusReporting is called by vtWriter after each write to the emulator,
// with the terminal lock released, with whether the guest has focus reporting
// on now. On the write that turns it on it sends the focus-in report when the
// pane has focus.
//
// The focus check takes the session's state lock and the daemon's client lock,
// so it runs off the writer goroutine: a holder of either that waits for the
// emulator to drain must not wait on this goroutine in turn.
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

// sessionShown reports whether a session has an attached TUI client whose host
// terminal has not reported losing focus. A terminal that never reports focus
// counts as shown, as it does for sessionHostFocus.
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
