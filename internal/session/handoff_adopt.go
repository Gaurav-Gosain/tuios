package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"tuios.dev/vt"
)

// The new image's half of a live handoff: reading the manifest its
// predecessor wrote and taking over the panes, the sockets and the start lock
// it handed down. See handoff.go.
//
// None of the start-time sweeps run here. Each of them clears what a dead
// daemon left, and nothing here is dead: the saved state and its leftovers,
// the stash, the ssh agent and display links the panes' environment names,
// the history files, the grants record, and the resume offers for agents that
// are still running.

// RunAdopted runs the daemon from the panes its previous image handed over on
// the descriptor fd, and blocks until shutdown.
func (d *Daemon) RunAdopted(fd int) error {
	InstallDaemonLogging(d.foreground, d.logFile)
	LogBasic("Daemon %s adopting its predecessor's panes (pid %d)", d.version, os.Getpid())
	if err := d.adopt(fd); err != nil {
		return fmt.Errorf("live upgrade: adopting the panes: %w", err)
	}
	<-d.ctx.Done()
	return d.shutdown()
}

// adopt reads the manifest on fd and takes over what it names. Every check
// runs before anything is started, so an adoption that fails leaves every
// inherited descriptor as it found it.
func (d *Daemon) adopt(fd int) error {
	mf := adoptedFile(fd, "tuios-handoff")
	if mf == nil {
		return ErrHandoffUnsupported
	}
	if _, err := mf.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewinding the manifest: %w", err)
	}
	m, err := readHandoffManifest(mf)
	if err != nil {
		return err
	}
	if err := checkAdoptable(m); err != nil {
		return err
	}

	socketPath := m.Daemon.SocketPath
	d.manager.SetSocketPath(socketPath)
	// Before any pane is entered: a pane's token is checked against the key,
	// and its events go on from the carried numbering.
	if len(m.Daemon.PaneTokenKey) > 0 {
		d.manager.paneTokenKey = m.Daemon.PaneTokenKey
	}
	d.events.restore(m.Daemon.Events)
	d.agents.restore(m.Daemon.Mail)
	listeners := map[string]net.Listener{}
	// FileListener works on a copy of each descriptor. The inherited ones are
	// closed only once the adoption has gone through.
	var inherited []*os.File
	for name, lfd := range m.Daemon.Listeners {
		f := adoptedFile(lfd, name)
		l, err := net.FileListener(f)
		if err != nil {
			return fmt.Errorf("the %s socket: %w", name, err)
		}
		inherited = append(inherited, f)
		if ul, ok := l.(*net.UnixListener); ok {
			ul.SetUnlinkOnClose(false)
		}
		listeners[name] = l
	}
	d.listener = listeners["main"]
	d.linkListener = listeners["link"]
	d.linkHumanListener = listeners["link-human"]
	if m.Daemon.LockFD >= 0 {
		d.startLock = adoptedFile(m.Daemon.LockFD, startLockPath(socketPath))
	}
	if err := d.writePidFile(); err != nil {
		LogError("Live upgrade: writing the pid file: %v", err)
	}
	if l := listeners["herdr"]; l != nil {
		d.serveHerdrOn(l, socketPath)
	}
	d.initSSHAgent()
	d.initDisplayFollow()

	bySession := map[string][]handoffPane{}
	for _, p := range m.Panes {
		bySession[p.Session] = append(bySession[p.Session], p)
	}
	d.adopting.Store(true)
	var started []*PTY
	for _, rec := range m.Sessions {
		ptys, err := d.adoptSession(rec, bySession[rec.ID])
		if err != nil {
			d.adopting.Store(false)
			return fmt.Errorf("session %q: %w", rec.State.Name, err)
		}
		started = append(started, ptys...)
	}
	d.adopting.Store(false)
	for _, p := range started {
		p.startPTY()
	}

	d.attention.load(attentionPath(), d.attentionLive)
	d.noteHostProblems(d.configProblems())
	d.outbox.load(outboxPath())
	d.reviewNotes.load(reviewNotesPath(), func(window string) bool { return d.sessionOfWindow(window) != "" })
	d.serve()
	_ = mf.Close()
	for _, f := range inherited {
		_ = f.Close()
	}
	LogBasic("Live upgrade: adopted %d sessions and %d panes", len(m.Sessions), len(m.Panes))
	return nil
}

// checkAdoptable checks the manifest names what this image can take: a main
// socket, and a session for every pane.
func checkAdoptable(m *handoffManifest) error {
	if _, ok := m.Daemon.Listeners["main"]; !ok {
		return errors.New("the manifest names no main socket")
	}
	sessions := map[string]bool{}
	for _, s := range m.Sessions {
		if s.State.Name == "" {
			return errors.New("a session in the manifest has no name")
		}
		sessions[s.ID] = true
	}
	for _, p := range m.Panes {
		if !sessions[p.Session] {
			return fmt.Errorf("pane %s names a session the manifest does not hold", shortID(p.ID))
		}
		if p.FD < 0 || p.Width <= 0 || p.Height <= 0 {
			return fmt.Errorf("pane %s has no descriptor or size", shortID(p.ID))
		}
	}
	return nil
}

// adoptSession rebuilds one session around its adopted panes, and returns
// the panes for the caller to start.
func (d *Daemon) adoptSession(rec handoffSession, panes []handoffPane) ([]*PTY, error) {
	st := rec.State
	width, height := st.Width, st.Height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	sess, err := d.manager.CreateSession(st.Name, &SessionConfig{restoreID: rec.ID, Space: st.Space}, width, height)
	if err != nil {
		return nil, err
	}
	sess.SetStartDir(rec.StartDir)
	for id, dir := range rec.WindowStartDirs {
		sess.noteStartDir(id, dir)
	}
	if rec.LastUsed > 0 {
		sess.setLastUsed(time.Unix(0, rec.LastUsed))
	}

	windows := make(map[string]WindowState, len(st.Windows))
	for _, w := range st.Windows {
		windows[w.ID] = w
	}
	sessionID := sess.ID
	var out []*PTY
	for _, p := range panes {
		w := windows[p.Window]
		onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
		switch {
		case p.KeepOpen:
			onExit = nil
		case w.Popup && !w.Scratch:
			// A popup is closed by the daemon when its command exits, as
			// new-popup's own exit does.
			onExit = func(ptyID string) {
				d.notifyPTYClosed(sessionID, ptyID)
				d.closeWindowOfPTY(sess, ptyID, true)
			}
		}
		pty, err := sess.adoptPTY(p, w, onExit)
		if err != nil {
			return nil, fmt.Errorf("pane %s: %w", shortID(p.ID), err)
		}
		out = append(out, pty)
	}
	sess.adoptState(st)
	return out, nil
}

// adoptPTY enters a pane handed over by the previous image, around its
// inherited master and its running process. It does not start it.
func (s *Session) adoptPTY(rec handoffPane, w WindowState, onExit func(string)) (*PTY, error) {
	master, err := adoptMaster(rec.FD, rec.TTY)
	if err != nil {
		return nil, err
	}
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	if s.config != nil && s.config.grants != nil && w.ID != "" {
		table := s.config.grants
		table.add(w.ID, rec.ID, s.Name(), savedGrants(w.Grants))
		exit := onExit
		onExit = func(ptyID string) {
			table.remove(w.ID, ptyID)
			if exit != nil {
				exit(ptyID)
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	// The snapshot goes in before the emulator has callbacks, so nothing it
	// sets is raised as an event: none of it is new.
	terminal := vt.NewWithScrollback(rec.Width, rec.Height, s.scrollbackLines())
	if rec.State != nil {
		ApplyTerminalState(terminal, rec.State)
	}
	sp := ptySpawn{windowID: w.ID, onExit: onExit, workspace: w.Workspace, keepOpen: rec.KeepOpen && !rec.Exited}
	pty := s.installPTY(ctx, cancel, rec.ID, master, nil, rec.Width, rec.Height, sp, terminal)
	pty.spawnW, pty.spawnH = rec.SpawnW, rec.SpawnH
	pty.restoreLive(rec.State, rec.Live)
	if rec.Exited {
		pty.exitedMu.Lock()
		pty.exited, pty.exitCode = true, rec.ExitCode
		pty.exitedMu.Unlock()
		pty.exitSignal.Store(int32(rec.ExitSignal)) //nolint:gosec // a signal number
	} else if rec.PID > 0 {
		// On unix FindProcess always succeeds. The process is still this
		// pid's child, so it can be waited on: see monitorExit.
		pty.proc, _ = os.FindProcess(rec.PID)
	}
	return pty, nil
}

// adoptState installs a session's state as its previous image held it. It
// is not a client's push, so nothing is merged and no window event is raised:
// every window in it was already there.
func (s *Session) adoptState(st *SessionState) {
	s.stateMu.Lock()
	st.Name = s.Name()
	s.state = st
	s.noteStateChangeLocked()
	s.stateMu.Unlock()
}
