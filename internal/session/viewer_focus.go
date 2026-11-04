package session

import "slices"

// Viewer focus: a stream that shows a pane in a focused window.
//
// A TUI client tells the daemon whether its host terminal has focus, and the
// daemon reads that as the person looking at the session's focused pane: it
// reports focus to the pane, keeps a hook quiet for it, and marks its finished
// turn seen. A desktop app shows panes over stream-pane and has no client
// push, so its stream says the same with an F frame (verb_stream_pane.go). At
// 2 the stream counts as an attached TUI client whose terminal has focus and
// whose focused pane is the stream's pane. No rule is new: each one that reads
// a TUI client's focus reads the viewers' too.

// setViewerFocus records that holder shows window focused in s, or with an
// empty window that it no longer does, and sends a focus report (CSI I or
// CSI O) to every pane whose focus that changed, when the program in it has
// turned on focus reporting.
func (d *Daemon) setViewerFocus(s *Session, holder, window string) {
	d.hostFocusMu.Lock()
	defer d.hostFocusMu.Unlock()
	s.viewers.mu.Lock()
	old := s.viewers.focus[holder]
	s.viewers.mu.Unlock()
	if old == window {
		return
	}
	focused := ""
	if st := s.GetState(); st != nil {
		focused = st.FocusedWindowID
	}
	var panes []string
	for _, w := range []string{old, window, focused} {
		if w != "" && !slices.Contains(panes, w) {
			panes = append(panes, w)
		}
	}
	before := make([]bool, len(panes))
	for i, w := range panes {
		before[i] = s.paneHasFocus(w)
	}
	s.viewers.mu.Lock()
	if window == "" {
		delete(s.viewers.focus, holder)
	} else {
		if s.viewers.focus == nil {
			s.viewers.focus = make(map[string]string)
		}
		s.viewers.focus[holder] = window
	}
	s.viewers.mu.Unlock()
	for i, w := range panes {
		now := s.paneHasFocus(w)
		if now == before[i] {
			continue
		}
		pty, err := d.resolvePTYForTarget(s, w)
		if err != nil || !pty.FocusReportingOn() {
			continue
		}
		report := focusOutReport
		if now {
			report = focusInReport
		}
		_, _ = pty.Write([]byte(report))
	}
}

// viewerFocusOn reports whether a stream shows window focused, or, with an
// empty window, any pane of the session.
func (s *Session) viewerFocusOn(window string) bool {
	s.viewers.mu.Lock()
	defer s.viewers.mu.Unlock()
	for _, w := range s.viewers.focus {
		if window == "" || w == window {
			return true
		}
	}
	return false
}

// noteViewerLooked is a client's push with window focused, for a stream: the
// window's finished turn is seen, and a move to it ends an approval hold.
func (s *Session) noteViewerLooked(window string, moved bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state == nil {
		return
	}
	s.markCompletionSeenLocked(window)
	if moved {
		s.emit(SessionEvent{Type: eventPaneFocused, Window: window})
	}
}
