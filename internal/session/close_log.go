package session

import "strconv"

func (d *Daemon) askerOf(cs *connState) string {
	if cs == nil {
		return "the daemon"
	}
	cs.mu.Lock()
	id, kind, tui := cs.clientID, cs.kind, cs.isTUIClient
	cs.mu.Unlock()
	if inPane, window := d.peerPane(cs); inPane {
		if window == "" {
			return "a process in a pane that cannot be placed (client " + id + ")"
		}
		if name := d.windowLabel(window); name != "" {
			return "pane " + shortID(window) + " \"" + name + "\""
		}
		return "pane " + shortID(window)
	}
	switch {
	case tui:
		return "the TUI " + id
	case kind != "":
		return kind + " " + id
	}
	return id
}

func (d *Daemon) windowLabel(window string) string {
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, window); err == nil {
			return windowLabelOf(st.Windows[idx])
		}
	}
	return ""
}

func (p *PTY) logExit(code int) {
	how := "exited with code " + strconv.Itoa(code)
	if sig := p.ExitSignal(); sig > 0 {
		how = "was ended by signal " + strconv.Itoa(sig)
	}
	if p.ctx != nil && p.ctx.Err() != nil {
		how += ", after its pane was closed"
	}
	session := p.sessionID
	if home := p.home.Load(); home != nil {
		session = home.Name()
	}
	window := "no window"
	if p.windowID != "" {
		window = "window " + shortID(p.windowID)
	}
	LogBasic("Pane %s (%s) in session %s: its process %s", shortID(p.ID), window, session, how)
}
