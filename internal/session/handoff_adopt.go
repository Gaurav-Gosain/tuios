package session

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
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
//
// An adoption that fails, panics or hangs re-execs the binary the previous
// image ran, which adopts the panes as it wrote them. See adoptFallback.
func (d *Daemon) RunAdopted(fd int) error {
	InstallDaemonLogging(d.foreground, d.logFile)
	LogBasic("Daemon %s adopting its predecessor's panes (pid %d)", d.version, os.Getpid())
	handoffTestCrash(crashAtStart)
	// An image a keeper started finds every descriptor where the keeper
	// held it, the manifest's included.
	kept, err := keptDescriptors()
	if err == nil {
		fd, err = keptAt(kept, fd)
	}
	if err != nil {
		LogError("Live upgrade: %v", err)
		return errfmt.Errorf("live upgrade: %w", err)
	}
	fb := &adoptFallback{manifestFD: fd}
	stopWatch := fb.watch(adoptWatchdogTimeout())
	err = d.adoptGuarded(fd, fb, kept)
	stopWatch()
	if err != nil {
		LogError("Live upgrade: adopting the panes failed: %v", err)
		if !fb.reexec(err) {
			// The watchdog's fallback is under way, and it execs or ends
			// the process itself.
			select {}
		}
		return errfmt.Errorf("live upgrade: adopting the panes: %w", err)
	}
	<-d.ctx.Done()
	return d.shutdown()
}

// adoptGuarded is adopt with a panic turned into an error, so the fallback
// still runs. A recover covers only its own goroutine, which is why nothing
// adopt starts runs before the point the fallback is disarmed.
func (d *Daemon) adoptGuarded(fd int, fb *adoptFallback, kept map[int]int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errfmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return d.adopt(fd, fb, kept)
}

// adopt reads the manifest on fd and takes over what it names. Until the
// fallback is disarmed nothing reads a pane or accepts a call, so an adoption
// that fails before then leaves every inherited descriptor as it found it.
// kept is where a keeper holds each descriptor, nil for an image the handoff
// or its fallback execed.
func (d *Daemon) adopt(fd int, fb *adoptFallback, kept map[int]int) error {
	mf := adoptedFile(fd, "tuios-handoff")
	if mf == nil {
		return ErrHandoffUnsupported
	}
	fb.keep(mf)
	if _, err := mf.Seek(0, io.SeekStart); err != nil {
		return errfmt.Errorf("rewinding the manifest: %w", err)
	}
	m, err := readHandoffManifest(mf)
	if err != nil {
		return err
	}
	if err := m.remapKept(kept); err != nil {
		return err
	}
	fb.prepare(m)
	d.adoptKeeper(m.Daemon.Keeper, fb)
	if err := checkAdoptable(m); err != nil {
		return err
	}
	if err := handoffTestFailure(); err != nil {
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
	d.restoreWindowRecords(m.Daemon)
	listeners := map[string]net.Listener{}
	// FileListener works on a copy of each descriptor. The inherited ones
	// stay open for the fallback, and are closed once the adoption has gone
	// through.
	var inherited []*os.File
	for name, lfd := range m.Daemon.Listeners {
		f := adoptedFile(lfd, name)
		fb.keep(f)
		l, err := net.FileListener(f)
		if err != nil {
			return errfmt.Errorf("the %s socket: %w", name, err)
		}
		inherited = append(inherited, f)
		if ul, ok := l.(*net.UnixListener); ok {
			ul.SetUnlinkOnClose(false)
		}
		listeners[name] = l
	}
	// The previous binary's descriptor stays open for the fallback, and is
	// closed with the listeners' once the adoption has gone through.
	if m.Daemon.FallbackFD >= 0 {
		f := adoptedFile(m.Daemon.FallbackFD, "tuios-prev")
		fb.keep(f)
		inherited = append(inherited, f)
	}
	if m.Daemon.LockFD >= 0 {
		lock := adoptedFile(m.Daemon.LockFD, startLockPath(socketPath))
		fb.keep(lock)
		d.startLock = lock
	}

	d.listener = listeners["main"]
	d.linkListener = listeners["link"]
	d.linkHumanListener = listeners["link-human"]
	if err := d.writePidFile(); err != nil {
		LogError("Live upgrade: writing the pid file: %v", err)
	}
	if l := listeners["herdr"]; l != nil {
		d.serveHerdrOn(l, socketPath)
	}
	d.initSSHAgent()
	d.initDisplayFollow()

	stats := m.Daemon.Stats
	stats.ManifestBytes = m.Size
	lines := d.manager.scrollbackLines
	if lines <= 0 {
		lines = vt.DefaultScrollbackSize
	}
	colors := make(map[string]handoffReportColors, len(m.Sessions))
	for _, s := range m.Sessions {
		colors[s.ID] = s.ReportColors
	}
	terms, err := applyHandoffPanes(m.Panes, colors, lines, &stats)
	if err != nil {
		return err
	}
	bySession := map[string][]adoptedPane{}
	for i, p := range m.Panes {
		bySession[p.Session] = append(bySession[p.Session], adoptedPane{rec: p, terminal: terms[i]})
	}
	d.adopting.Store(true)
	var (
		started []*PTY
		closes  []func()
	)
	for _, rec := range m.Sessions {
		ptys, exited, err := d.adoptSession(rec, bySession[rec.ID], fb)
		if err != nil {
			d.adopting.Store(false)
			return errfmt.Errorf("session %q: %w", rec.State.Name, err)
		}
		started = append(started, ptys...)
		closes = append(closes, exited...)
	}
	d.adopting.Store(false)
	handoffTestCrash(crashWhileAdopting)

	// The point of no return: from here the panes are read and the sockets
	// served, and the previous binary could not take them back.
	if !fb.disarm() {
		select {}
	}
	// Run through /proc/self/fd/N, this image is named N: name it before
	// it starts the panes and serves its main socket, so ps and pidof find
	// the daemon from the first reply there.
	setProcessName(filepath.Base(os.Args[0]))
	for _, p := range started {
		p.startPTY()
	}
	// A pane the previous image reaped and wrote down as exited, in a window
	// that closes on exit, is one whose close that image queued and never
	// ran: the close runs on a goroutine after the reap, and it waited on
	// the handoff until the exec ended it. It runs here.
	for _, closeWindow := range closes {
		closeWindow()
	}
	if stats.StoppedAt > 0 {
		stats.PausedMS = float64(time.Now().UnixNano()-stats.StoppedAt) / 1e6
	}
	stats.Fallback = os.Getenv(handoffFallbackEnv) != ""
	stats.Recovered = kept != nil
	_ = os.Unsetenv(handoffFallbackEnv)
	_ = os.Unsetenv(handoffKeptEnv)
	d.lastHandoff.Store(&stats)

	d.attention.loadHandoff(attentionPath(), d.attentionLive, d.attentionWaiting)
	d.noteHostProblems(d.configProblems())
	d.outbox.load(outboxPath())
	d.reviewNotes.load(reviewNotesPath(), func(window string) bool { return d.sessionOfWindow(window) != "" })
	d.serve()
	// A delegated child that finished while the daemon was handed over is
	// still answered for.
	d.resumeDelegations()
	state := "upgraded"
	if stats.Fallback {
		state = "fallback"
	}
	if stats.Recovered {
		state = "recovered"
	}
	d.events.publish(streamEvent{Type: EventDaemonUpgraded, Title: stats.From, Body: d.version, State: state})
	// This image is up: the keeper stands down and exits, and is reaped.
	handoffTestCrash(crashServing)
	d.releaseKeeper()
	_ = mf.Close()
	for _, f := range inherited {
		_ = f.Close()
	}
	// This image is the one a later handoff falls back to.
	d.keepPrevBinary()
	// The copies the images before this one ran from are no longer needed.
	removeOldCopies()
	LogBasic("Live upgrade: adopted %d sessions and %d panes from %s (paused %.0f ms, apply %.0f ms, peak heap in use %d MB)",
		len(m.Sessions), len(m.Panes), stats.From, stats.PausedMS, stats.ApplyMS, stats.ApplyPeakHeapInuse>>20)
	return nil
}

// adoptedPane is a pane record with its emulator rebuilt.
type adoptedPane struct {
	rec      handoffPane
	terminal vt.Terminal
}

// handoffGCPercent is the GC target while the panes' emulators are rebuilt.
// Applying a snapshot with history allocates several times what it keeps, and
// at the default target the heap peaks at about twice the rebuilt live heap
// (docs/design/live-handoff.md, section 3.2).
const handoffGCPercent = 25

// applyHandoffPanes rebuilds every pane's emulator from its snapshot, a few
// at a time, under a lowered GC target. It drops each snapshot once applied.
//
// Each emulator is given its session's report colours before the snapshot
// goes in. After it, a program that turned on mode 2031 would be sent a
// change of scheme from the emulator's own black to the colours it was
// already told.
func applyHandoffPanes(panes []handoffPane, colors map[string]handoffReportColors, lines int, stats *HandoffStats) ([]vt.Terminal, error) {
	t0 := time.Now()
	stats.ApplyHeapStart = heapInuse()
	prevGC := debug.SetGCPercent(handoffGCPercent)
	stopSampling := sampleHeapInuse(&stats.ApplyPeakHeapInuse)
	terms := make([]vt.Terminal, len(panes))
	err := forEachBounded(len(panes), min(runtime.GOMAXPROCS(0), 4), func(i int) error {
		t := vt.NewWithScrollback(panes[i].Width, panes[i].Height, lines)
		colors[panes[i].Session].applyTo(t)
		if st := panes[i].State; st != nil {
			ApplyTerminalState(t, st)
			panes[i].Live.vtSeq = st.Seq
			panes[i].State = nil
		}
		terms[i] = t
		return nil
	})
	stopSampling()
	stats.ApplyHeapEnd = heapInuse()
	debug.SetGCPercent(prevGC)
	stats.ApplyMS = msSince(t0)
	memtrim.Request()
	return terms, err
}

// heapInuseSamples are the runtime metrics that add up to MemStats'
// HeapInuse, which they read without stopping the world.
var heapInuseSamples = []string{"/memory/classes/heap/objects:bytes", "/memory/classes/heap/unused:bytes"}

// heapInuse is the heap in use now.
func heapInuse() uint64 {
	samples := make([]metrics.Sample, len(heapInuseSamples))
	for i, name := range heapInuseSamples {
		samples[i].Name = name
	}
	metrics.Read(samples)
	return samples[0].Value.Uint64() + samples[1].Value.Uint64()
}

// sampleHeapInuse records the most heap in use, every 2 ms, until stopped.
func sampleHeapInuse(peak *uint64) (stop func()) {
	read := func() {
		if n := heapInuse(); n > *peak {
			*peak = n
		}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		for {
			read()
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	return func() {
		close(done)
		<-finished
		read()
	}
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
			return errfmt.Errorf("pane %s names a session the manifest does not hold", shortID(p.ID))
		}
		if p.FD < 0 || p.Width <= 0 || p.Height <= 0 {
			return errfmt.Errorf("pane %s has no descriptor or size", shortID(p.ID))
		}
	}
	return nil
}

// adoptSession rebuilds one session around its adopted panes, and returns
// the panes for the caller to start, and the closes of the windows whose
// process had exited and that close on exit, for the caller to run once the
// panes have started.
func (d *Daemon) adoptSession(rec handoffSession, panes []adoptedPane, fb *adoptFallback) ([]*PTY, []func(), error) {
	st := rec.State
	width, height := st.Width, st.Height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	cfg := &SessionConfig{restoreID: rec.ID, Space: st.Space, Rank: st.Rank}
	if rec.Created > 0 {
		cfg.restoreCreated = time.Unix(0, rec.Created)
	}
	if rec.LastActive > 0 {
		cfg.restoreActive = time.Unix(0, rec.LastActive)
	}
	sess, err := d.manager.CreateSession(st.Name, cfg, width, height)
	if err != nil {
		return nil, nil, err
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
	// Before any pane: installPTY gives each pane the session's colours,
	// which applyHandoffPanes already gave its emulator, so nothing changes.
	sess.applyReportColors(rec.ReportColors.Bg, rec.ReportColors.Fg, rec.ReportColors.Palette)
	sessionID := sess.ID
	var (
		out    []*PTY
		closes []func()
	)
	for _, ap := range panes {
		p := ap.rec
		w := windows[p.Window]
		onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
		// closeWindow is the window's own close when its process exits, as
		// the verb that made it built it: new-window with close_on_exit and a
		// plugin's pane close any window, a popup only a popup.
		var closeWindow func(ptyID string)
		switch {
		case p.KeepOpen:
			onExit = nil
		case p.CloseOnExit:
			closeWindow = func(ptyID string) { d.closeWindowOfPTY(sess, ptyID, false) }
		case w.Popup && !w.Scratch:
			closeWindow = func(ptyID string) { d.closeWindowOfPTY(sess, ptyID, true) }
		}
		if closeWindow != nil {
			onExit = func(ptyID string) {
				d.notifyPTYClosed(sessionID, ptyID)
				closeWindow(ptyID)
			}
		}
		// start-agent --protocol forgets its mark when the agent exits.
		if d.paneProtocol(w.ID) != "" {
			exit, window := onExit, w.ID
			onExit = func(ptyID string) {
				d.unmarkProtocolPane(window)
				if exit != nil {
					exit(ptyID)
				}
			}
		}
		pty, err := sess.adoptPTY(p, w, onExit, ap.terminal)
		if err != nil {
			return nil, nil, errfmt.Errorf("pane %s: %w", shortID(p.ID), err)
		}
		fb.keep(pty.pty)
		out = append(out, pty)
		if p.Exited && closeWindow != nil {
			ptyID := p.ID
			closes = append(closes, func() { closeWindow(ptyID) })
		}
	}
	sess.adoptState(st, rec)
	return out, closes, nil
}

// adoptPTY enters a pane handed over by the previous image, around its
// inherited master and its running process. It does not start it.
func (s *Session) adoptPTY(rec handoffPane, w WindowState, onExit func(string), terminal vt.Terminal) (*PTY, error) {
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

	// The snapshot went into the emulator before it had callbacks (see
	// applyHandoffPanes), so nothing it set is raised as an event: none of
	// it is new.
	ctx, cancel := context.WithCancel(context.Background())
	sp := ptySpawn{windowID: w.ID, onExit: onExit, workspace: w.Workspace, keepOpen: rec.KeepOpen && !rec.Exited, closeOnExit: rec.CloseOnExit}
	pty := s.installPTY(ctx, cancel, rec.ID, master, nil, rec.Width, rec.Height, sp, terminal)
	pty.spawnW, pty.spawnH = rec.SpawnW, rec.SpawnH
	pty.restoreLive(rec.Live)
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

// adoptState installs a session's state as its previous image held it, with
// the workspace return the record carries. It is not a client's push, so
// nothing is merged and no window event is raised: every window in it was
// already there.
func (s *Session) adoptState(st *SessionState, rec handoffSession) {
	s.stateMu.Lock()
	st.Name = s.Name()
	s.state = st
	s.returnTo = maps.Clone(rec.ReturnTo)
	s.workspaceTrail = slices.Clone(rec.WorkspaceTrail)
	s.noteStateChangeLocked()
	s.stateMu.Unlock()
}

// applyTo gives an emulator the colours, before anything else is written to
// it. Empty leaves the emulator's own.
func (c handoffReportColors) applyTo(t vt.Terminal) {
	fg, bg := parseReportColor(c.Fg), parseReportColor(c.Bg)
	if fg != nil || bg != nil || c.Palette != "" {
		t.SetReportColors(fg, bg, parseReportPalette(c.Palette))
	}
}
