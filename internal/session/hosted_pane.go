package session

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/google/uuid"

	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

// A pane this machine runs on another machine's behalf: the far half of a
// global session.
//
// A global session is not a new kind of session. It is an ordinary session
// that happens to hold a window whose process runs somewhere else, which is
// what keeps `tuios ls`, the verbs, the mailbox, hooks and rehydration working
// on it with no special case. The near half is remotePane in remote_pane.go.
//
// The division of labour is the whole design, and it is deliberately lopsided.
// This machine supplies a process and a pty and nothing else: it runs no
// emulator for the pane, keeps no scrollback for it, and does not know which
// session or window it belongs to. The daemon that asked owns all of that, the
// same way it does for a pane of its own, because a hosted pane reaches it
// through PTY's paneIO seam and every path above that seam is already
// indifferent to where the bytes came from.
//
// That is why there is exactly one emulator per pane rather than two. The
// alternative, attaching a session here and borrowing a window from it, would
// have run a second emulator over the same bytes and, worse, enrolled the pane
// in this machine's size negotiation: a session's size is the minimum over its
// attached clients, so a pane sized by a layout on another machine would have
// shrunk the session of anyone working here directly. A hosted pane has one
// reader and one writer and takes its size from them alone.
//
// This grants no authority that the link did not already carry. A host in the
// [hosts] table can already be asked to attach or create a session, and a
// session is a shell. Spawning a process here is the same permission stated
// more directly, and it is refused on the same link that refuses everything
// else.
type hostedPane struct {
	id  string
	pty xpty.Pty
	cmd *exec.Cmd

	// closeOnce makes teardown idempotent. Three separate events end a hosted
	// pane and any of them can be first: the process exits, the owning daemon
	// closes the connection, or this daemon shuts down.
	closeOnce sync.Once
}

// hostedPaneSpec is what the owning daemon asks for. The terminal type and the
// shell come from the asking side rather than from this machine, because the
// pane belongs to that session: it is drawn by that session's emulator and
// sized by that session's layout, so it has to agree with that session about
// what it is talking to.
type hostedPaneSpec struct {
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Cwd       string   `json:"cwd,omitempty"`
	Command   []string `json:"command,omitempty"`
	Term      string   `json:"term,omitempty"`
	ColorTerm string   `json:"color_term,omitempty"`
	Shell     string   `json:"shell,omitempty"`
	Session   string   `json:"session,omitempty"`
}

// hostedPaneBounds are the sizes a spawn is clamped to. A pane arrives sized by
// another machine's layout and the number is not checked by anything between
// there and here, so it is treated as input rather than as fact.
const (
	hostedPaneMinDim = 1
	hostedPaneMaxDim = 10000
)

// registerHostedPane spawns the process and records it under a fresh id. The
// id is what resize-pane addresses, because a resize arrives on the link's
// control stream rather than on the pane's own connection: the pane's
// connection is raw bytes from the reply onward and has no room left to say
// anything out of band.
func (d *Daemon) registerHostedPane(spec hostedPaneSpec) (*hostedPane, error) {
	width, height := clampHostedDim(spec.Width), clampHostedDim(spec.Height)

	shell := spec.Shell
	if shell == "" {
		shell = (&Session{config: &SessionConfig{PreferredShell: d.manager.PreferredShell}}).getShell()
	}
	env := hostedPaneEnv(d, spec)

	pty, cmd, err := ptyspawn.Spawn(width, height, func() *exec.Cmd {
		var cmd *exec.Cmd
		if len(spec.Command) > 0 {
			cmd = exec.Command(spec.Command[0], spec.Command[1:]...)
		} else {
			cmd = exec.Command(shell)
		}
		cmd.Env = env
		// A directory that is not there is not a failure. The asking side sent
		// the directory its own pane was in, and that path means nothing on
		// this machine unless the two happen to share it, so an unusable one
		// falls back to the shell's default rather than refusing the pane.
		if spec.Cwd != "" {
			if info, statErr := os.Stat(spec.Cwd); statErr == nil && info.IsDir() {
				cmd.Dir = spec.Cwd
			}
		}
		return cmd
	}, debugLog)
	if err != nil {
		return nil, err
	}

	releaseSlave(pty)

	hp := &hostedPane{id: uuid.New().String(), pty: pty, cmd: cmd}

	d.hostedPanesMu.Lock()
	if d.hostedPanes == nil {
		d.hostedPanes = make(map[string]*hostedPane)
	}
	d.hostedPanes[hp.id] = hp
	d.hostedPanesMu.Unlock()

	return hp, nil
}

// releaseSlave closes this process's own copy of the pty's slave end, now that
// the child has inherited its own.
//
// A pty master reports end of file only once every slave descriptor is shut,
// and xpty leaves one open in the parent after starting the command. A pane
// here notices its process exiting through the master going quiet, so with that
// descriptor held the relay's read waits for a byte from a process that is
// already gone. On Linux it waits forever, and the window on the other machine
// stays up around nothing.
//
// A pane of this daemon's own does not need it and does not do it: its exit is
// heard from cmd.Wait, which is a fact about a process rather than about a
// descriptor. A hosted pane has no waiter on this machine that anyone is
// listening to, because its owner is on the other side of a link and the
// stream ending is the only notice that crosses.
func releaseSlave(pty xpty.Pty) {
	// Named rather than asserted against a concrete type, so a pty
	// implementation without a separate slave end simply does not match.
	slaved, ok := pty.(interface{ Slave() *os.File })
	if !ok {
		return
	}
	if f := slaved.Slave(); f != nil {
		_ = f.Close()
	}
}

// clampHostedDim keeps a size sent from another machine inside what a pty can
// be asked for. Zero is the common case rather than an error: it is what an
// older peer and an omitted field both look like.
func clampHostedDim(v int) int {
	if v < hostedPaneMinDim {
		return 80
	}
	if v > hostedPaneMaxDim {
		return hostedPaneMaxDim
	}
	return v
}

// hostedPaneEnv builds the environment the hosted process runs in. It mirrors
// Session.buildEnv in what it sets and differs in where two of the values come
// from: TERM and COLORTERM are the asking session's, because that session's
// emulator is what the program is really talking to, while TUIOS_HOST stays
// this machine's, because that is where the process actually is.
//
// It is not the same set Session.buildEnv exports, and the difference matters.
// TUIOS_SOCKET, TUIOS_WINDOW_ID, TUIOS_PANE_ID and TUIOS_ENV are all absent,
// because each of them addresses something on the machine the session is on
// and there is no way to reach that from here: the link is dialled one way and
// the far side cannot open a connection back. An agent in a pane like this
// therefore cannot report its own state, and is detected instead by the daemon
// that owns the window asking this one what is running; see pane-agent.
func hostedPaneEnv(d *Daemon, spec hostedPaneSpec) []string {
	env := os.Environ()

	term := spec.Term
	if term == "" {
		term = "xterm-256color"
	}
	colorTerm := spec.ColorTerm
	if colorTerm == "" {
		colorTerm = "truecolor"
	}
	env = append(env, "TERM="+term, "COLORTERM="+colorTerm)
	env = append(env, "TERM_PROGRAM="+guestenv.TermProgram(false, false))
	env = append(env, "TERM_PROGRAM_VERSION=0.1.0")
	// The session the pane belongs to is deliberately not exported.
	//
	// It is a session on the other machine, and every tool that reads
	// TUIOS_SESSION uses it to address a session on the machine it is running
	// on. Exporting it here meant a shim on this side resolving that name
	// against this daemon's sessions, where it names a different session or
	// none at all. TUIOS_SESSION_REMOTE carries it for anything that wants to
	// know where the pane came from, under a name nothing addresses with.
	if spec.Session != "" {
		env = append(env, "TUIOS_SESSION_REMOTE="+spec.Session)
	}
	if host := d.hostedPaneHostName(); host != "" {
		env = append(env, "TUIOS_HOST="+host)
	}
	// TUIOS_PANE_HOSTED tells a program in the pane that its terminal is on
	// another machine, and that the usual per-pane variables are therefore
	// absent: there is no TUIOS_SOCKET here that reaches the daemon holding
	// the window, and no window or pane id that means anything on this side.
	//
	// Nothing in tuios reads it. It is for a person who is lost and for a
	// shell profile that wants to hold back the parts of its setup that only
	// mean something on the machine the session is on.
	env = append(env, "TUIOS_PANE_HOSTED=1")
	return env
}

// hostedPaneHostName is the name this machine gives itself, taken from the
// session manager so a hosted pane and a local one agree on it.
func (d *Daemon) hostedPaneHostName() string {
	if d.manager != nil && d.manager.hostName != "" {
		return d.manager.hostName
	}
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

// lookupHostedPane finds a pane by the id open-pane returned.
func (d *Daemon) lookupHostedPane(id string) *hostedPane {
	d.hostedPanesMu.Lock()
	defer d.hostedPanesMu.Unlock()
	return d.hostedPanes[id]
}

// forgetHostedPane drops a pane from the registry and tears it down.
func (d *Daemon) forgetHostedPane(id string) {
	d.hostedPanesMu.Lock()
	hp := d.hostedPanes[id]
	delete(d.hostedPanes, id)
	d.hostedPanesMu.Unlock()
	if hp != nil {
		hp.close()
	}
}

// closeHostedPanes ends every hosted pane. The daemon calls it on shutdown, so
// a process running here on another machine's behalf does not outlive the
// daemon that was relaying it and become unreachable.
func (d *Daemon) closeHostedPanes() {
	d.hostedPanesMu.Lock()
	panes := make([]*hostedPane, 0, len(d.hostedPanes))
	for id, hp := range d.hostedPanes {
		panes = append(panes, hp)
		delete(d.hostedPanes, id)
	}
	d.hostedPanesMu.Unlock()
	for _, hp := range panes {
		hp.close()
	}
}

// resize changes the pty's size. It is driven by the owning layout on the
// other machine, and nothing here has an opinion about it.
func (hp *hostedPane) resize(width, height int) error {
	return hp.pty.Resize(clampHostedDim(width), clampHostedDim(height))
}

// close kills the process and releases the pty.
func (hp *hostedPane) close() {
	hp.closeOnce.Do(func() {
		if hp.cmd != nil && hp.cmd.Process != nil {
			_ = hp.cmd.Process.Kill()
		}
		_ = hp.pty.Close()
	})
}

// relayHostedPane copies bytes between the owning daemon's connection and the
// pty until either end stops, then ends the other.
//
// Both deadlines are cleared first, for the reason relayHostConnection clears
// them: the verb reply was written under a write deadline, a deadline on a
// net.Conn is an absolute time rather than a per-write budget, and a relay that
// inherits one starts failing its writes a fixed number of seconds later. A
// pane can be silent for hours and that is not a failure, so the relay runs
// with no deadline in either direction.
func (d *Daemon) relayHostedPane(cs *connState, br *bufio.Reader, hp *hostedPane) {
	conn := cs.conn
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	defer d.forgetHostedPane(hp.id)

	go func() {
		// Reap the process. Nothing else on this machine waits for a hosted
		// pane's command: the local path waits in monitorExit, which belongs
		// to a PTY this daemon owns, and a hosted pane has no PTY here. An
		// unwaited child is a zombie for as long as the daemon runs, and a
		// daemon that hosts panes for another machine collects one per pane.
		//
		// It doubles as the backstop for the pty going quiet: whichever of the
		// two notices first ends the pane, and close is idempotent.
		if hp.cmd != nil {
			_ = hp.cmd.Wait()
		}
		hp.close()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The owning daemon's keystrokes. br is used rather than the bare
		// connection so a byte it buffered ahead of the reply is not lost.
		_, _ = io.Copy(hp.pty, br)
		// The owning side hung up. The process goes with it: there is no
		// second reader that could still be served, and a shell left running
		// on a pty nobody holds is a leak nobody can see.
		hp.close()
	}()
	go func() {
		// The daemon stopping or the connection dropping ends the relay from
		// outside; without this the copy below waits for a byte that no longer
		// has anywhere to go.
		select {
		case <-d.ctx.Done():
		case <-cs.done:
		case <-done:
			return
		}
		hp.close()
		_ = conn.Close()
	}()

	// The process's output. This returns when the pty closes, which is what
	// the process exiting looks like from here, and closing the connection is
	// how the owning daemon is told: its own read ends, and it closes the pane
	// the way it closes a local pane whose shell exited.
	_, _ = io.Copy(conn, hp.pty)
	hp.close()
	_ = conn.Close()
	<-done
}

// foreground is the process running in the hosted pane's terminal right now.
//
// It is the far half of agent detection. The daemon that owns the window holds
// the emulator and the scrollback and can read a pane's output all it likes,
// but the pid it would read to find out what is running means nothing on its
// own machine: there is no process there. Only this side can look, which is
// the same reason pane-cwd exists.
func (hp *hostedPane) foreground() (foregroundInfo, bool) {
	if hp.cmd == nil || hp.cmd.Process == nil {
		return foregroundInfo{}, false
	}
	shellPID := hp.cmd.Process.Pid
	info, running := foregroundProcess(shellPID)
	// Stamped outside the running check, for the reason foregroundResolver
	// gives: the shell pid is a fact about the pane rather than about what the
	// pane is running.
	info.shellPID = shellPID
	return info, running
}

// processCwd is the directory the hosted pane's process is in.
func (hp *hostedPane) processCwd() (string, bool) {
	if hp.cmd == nil || hp.cmd.Process == nil {
		return "", false
	}
	return ptyspawn.ProcessCwd(hp.cmd.Process.Pid)
}
