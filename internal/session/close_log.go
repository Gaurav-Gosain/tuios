package session

import "strconv"

// The log lines that say where a pane went.
//
// A pane that is gone was closed, or its process ended. Both write a line:
// CloseDaemonWindow names the window and who asked, and noteExit names the
// pane, its window and how its process ended. So a lost pane is one grep for
// its short ID in daemon.log.

// askerOf names the client on cs for a log line: the pane it runs in, by
// short ID and name, or the client by kind and ID when it runs in no pane.
// A client ID reads "client-" and a number.
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

// windowLabel is the name of the window with ID window in any session, or ""
// when no session holds it.
func (d *Daemon) windowLabel(window string) string {
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, window); err == nil {
			return windowLabelOf(st.Windows[idx])
		}
	}
	return ""
}

// logExit writes the line for a pane whose process ended: the pane, its
// window and session, and the exit code or the signal. A process ended by
// its pane's close says so, since the close wrote its own line. It reads no
// session state: an exit can be noted with the state lock held.
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
