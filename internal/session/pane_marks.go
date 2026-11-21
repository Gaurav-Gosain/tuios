package session

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

type paneMarkFunc func(ptyID, windowID string, mark uint32)

func paneMarkBit(eventType string) uint32 {
	switch eventType {
	case EventOutput:
		return PaneMarkOutput
	case EventBell:
		return PaneMarkBell
	}
	return 0
}

func (p *PTY) raisePaneMark(mark uint32) bool {
	if p.paneMarks.Load()&mark != 0 {
		return false
	}
	return p.paneMarks.Or(mark)&mark == 0
}

func (p *PTY) rearmPaneMarks() { p.paneMarks.Store(0) }

// SetPaneMarkSink installs the receiver of the session's pane marks.
func (s *Session) SetPaneMarkSink(fn paneMarkFunc) {
	if fn == nil {
		s.paneMarkSink.Store(nil)
		return
	}
	s.paneMarkSink.Store(&fn)
}

func (s *Session) notePaneMark(ptyID, windowID string, mark uint32) {
	if s == nil {
		return
	}
	if fn := s.paneMarkSink.Load(); fn != nil {
		(*fn)(ptyID, windowID, mark)
	}
}

func (s *Session) rearmPaneMarks() {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	for _, p := range s.ptys {
		p.rearmPaneMarks()
	}
}

func paneMarkReader(cs *connState, sessionID, ptyID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if !cs.isTUIClient || !cs.attached || !cs.paneMarks || cs.passive || cs.sessionID != sessionID {
		return false
	}
	_, streams := cs.ptySubscriptions[ptyID]
	return !streams
}

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
