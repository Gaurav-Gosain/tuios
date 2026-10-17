package session

// Pane marks: the dock marks the tab of a workspace out of view when a pane on
// it rings the bell or prints. A client streams only the panes it shows
// (app.OS.SubscribeWorkspaceWindows), so it never sees that bell or output
// itself. The daemon does, and tells it.
//
// It tells it once per edge. Each pane keeps one bit per mark: clear means
// "not reported since the pane's subscriptions last changed". The first output
// or bell after that sets the bit, and the daemon sends MsgPaneMark to every
// client in the session that reads it and does not stream the pane. Anything
// after that costs the pane one atomic load per chunk and sends nothing, until
// a client subscribes to the pane or drops it, which is a client starting or
// stopping to look at it. A client that attaches clears the bits of every pane
// in its session, so it hears the first output after it arrives.
//
// A client that streams the pane sees its output and bell for itself, so it is
// skipped. Two clients looking at different workspaces may each be told about
// a pane once more than strictly needed after the other subscribes; the client
// keeps a mark as a set bit, so a repeat changes nothing.
//
// The hello's PaneMarks asks for it and the welcome's offers it. A daemon that
// predates it sends none, and its clients show no marks for panes they do not
// stream. A client that predates it is sent none.

// The pane mark bits, as PaneMarkPayload.Marks carries them.
const (
	PaneMarkBell   uint32 = 1 << iota // the pane rang the bell
	PaneMarkOutput                    // the pane printed
)

// PaneMarkPayload is the body of MsgPaneMark: the pane, and the marks it just
// raised.
type PaneMarkPayload struct {
	WindowID string
	PTYID    string
	Marks    uint32
}

// paneMarkFunc receives a pane's first mark since its subscriptions changed.
type paneMarkFunc func(ptyID, windowID string, mark uint32)

// paneMarkBit is the mark an event raises, or 0.
func paneMarkBit(eventType string) uint32 {
	switch eventType {
	case EventOutput:
		return PaneMarkOutput
	case EventBell:
		return PaneMarkBell
	}
	return 0
}

// raisePaneMark sets mark on the pane and reports whether it was clear. The
// load first keeps a flooding pane off the read-modify-write once the bit is
// set.
func (p *PTY) raisePaneMark(mark uint32) bool {
	if p.paneMarks.Load()&mark != 0 {
		return false
	}
	return p.paneMarks.Or(mark)&mark == 0
}

// rearmPaneMarks clears the pane's marks, so its next output and bell are
// reported again.
func (p *PTY) rearmPaneMarks() { p.paneMarks.Store(0) }

// SetPaneMarkSink installs the receiver of the session's pane marks.
func (s *Session) SetPaneMarkSink(fn paneMarkFunc) {
	if fn == nil {
		s.paneMarkSink.Store(nil)
		return
	}
	s.paneMarkSink.Store(&fn)
}

// notePaneMark hands a pane's new mark to the sink, if there is one.
func (s *Session) notePaneMark(ptyID, windowID string, mark uint32) {
	if s == nil {
		return
	}
	if fn := s.paneMarkSink.Load(); fn != nil {
		(*fn)(ptyID, windowID, mark)
	}
}

// rearmPaneMarks clears the marks of every pane in the session.
func (s *Session) rearmPaneMarks() {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	for _, p := range s.ptys {
		p.rearmPaneMarks()
	}
}

// paneMarkReader reports whether cs is told about a mark on ptyID of session
// sessionID: an attached TUI client of that session that asked for marks and
// does not stream the pane.
func paneMarkReader(cs *connState, sessionID, ptyID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if !cs.isTUIClient || !cs.attached || !cs.paneMarks || cs.passive || cs.sessionID != sessionID {
		return false
	}
	_, streams := cs.ptySubscriptions[ptyID]
	return !streams
}

// sendPaneMark tells the clients of the session that do not stream the pane
// that it raised mark. It runs on its own goroutine: the bell is raised under
// the pane's terminal lock, and this takes the client locks.
func (d *Daemon) sendPaneMark(sessionID, ptyID, windowID string, mark uint32) {
	var msg *Message
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		if !paneMarkReader(cs, sessionID, ptyID) {
			continue
		}
		if msg == nil {
			var err error
			msg, err = NewMessage(MsgPaneMark, &PaneMarkPayload{WindowID: windowID, PTYID: ptyID, Marks: mark})
			if err != nil {
				return
			}
		}
		d.queueBroadcast(cs, msg, "sendPaneMark")
	}
}
