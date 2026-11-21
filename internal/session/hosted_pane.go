package session

import (
	"bufio"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/google/uuid"

	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/panescope"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

type hostedPane struct {
	id        string
	pty       xpty.Pty
	cmd       *exec.Cmd
	procStart uint64

	closeOnce sync.Once

	window     string
	callsToken string
	calls      hostedCalls

	grace       time.Duration
	resumeToken string

	startOnce sync.Once

	out    sync.Mutex
	ring   []byte
	outSeq int64

	connMu sync.Mutex
	conn   net.Conn
	orphan *time.Timer
	ending bool
	closed bool

	onEnd func()

	stop      chan struct{}
	inMu      sync.Mutex
	kittyLink *kittyLinkFar
}

func (hp *hostedPane) writeInput(p []byte) {
	hp.inMu.Lock()
	defer hp.inMu.Unlock()
	_, _ = hp.pty.Write(p)
}

type lockedInput struct{ hp *hostedPane }

func (w lockedInput) Write(p []byte) (int, error) {
	w.hp.inMu.Lock()
	defer w.hp.inMu.Unlock()
	return w.hp.pty.Write(p)
}

type hostedPaneSpec struct {
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	Cwd        string        `json:"cwd,omitempty"`
	Command    []string      `json:"command,omitempty"`
	Term       string        `json:"term,omitempty"`
	ColorTerm  string        `json:"color_term,omitempty"`
	Shell      string        `json:"shell,omitempty"`
	Session    string        `json:"session,omitempty"`
	Window     string        `json:"window,omitempty"`
	Resumable  bool          `json:"resumable,omitempty"`
	Resume     *hostedResume `json:"resume,omitempty"`
	KittyMedia bool          `json:"kitty_media,omitempty"`
}

type hostedResume struct {
	Pane   string `json:"pane"`
	Token  string `json:"token"`
	Offset int64  `json:"offset"`
}

const (
	hostedPaneMinDim = 1
	hostedPaneMaxDim = 10000
)

func (d *Daemon) registerHostedPane(spec hostedPaneSpec, grace time.Duration) (*hostedPane, error) {
	width, height := clampHostedDim(spec.Width), clampHostedDim(spec.Height)

	shell := spec.Shell
	if shell == "" {
		shell = (&Session{config: &SessionConfig{PreferredShell: d.manager.PreferredShell}}).getShell()
	}
	hp := &hostedPane{id: uuid.New().String(), stop: make(chan struct{})}
	if grace > 0 {
		hp.grace = grace
		hp.resumeToken = newHostedCallsToken()
	}
	if hostedWindowIDPattern().MatchString(spec.Window) {
		hp.window = spec.Window
		hp.callsToken = newHostedCallsToken()
	}
	env := hostedPaneEnv(d, spec, hp)

	pty, cmd, err := ptyspawn.Spawn(width, height, func() *exec.Cmd {
		var cmd *exec.Cmd
		if len(spec.Command) > 0 {
			//tuios:allow-exec ptyspawn starts the pane program on a pseudo console
			cmd = exec.Command(spec.Command[0], spec.Command[1:]...)
		} else {
			//tuios:allow-exec ptyspawn starts the pane program on a pseudo console
			cmd = exec.Command(shell)
		}
		cmd.Env = env
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
	panescope.Place(cmd.Process.Pid, log.Printf)

	releaseSlave(pty)

	hp.pty, hp.cmd = pty, cmd
	hp.procStart = processStart(cmd.Process)
	id := hp.id
	hp.onEnd = func() {
		d.hostedPanesMu.Lock()
		if d.hostedPanes[id] == hp {
			delete(d.hostedPanes, id)
		}
		d.hostedPanesMu.Unlock()
	}

	d.hostedPanesMu.Lock()
	if d.hostedPanesShut {
		d.hostedPanesMu.Unlock()
		_ = cmd.Process.Kill()
		_ = pty.Close()
		_ = cmd.Wait()
		return nil, errHandoffHostedOpen
	}
	if d.hostedPanes == nil {
		d.hostedPanes = make(map[string]*hostedPane)
	}
	d.hostedPanes[hp.id] = hp
	d.hostedPanesMu.Unlock()

	return hp, nil
}

func releaseSlave(pty xpty.Pty) {
	slaved, ok := pty.(interface{ Slave() *os.File })
	if !ok {
		return
	}
	if f := slaved.Slave(); f != nil {
		_ = f.Close()
	}
}

func clampHostedDim(v int) int {
	if v < hostedPaneMinDim {
		return 80
	}
	if v > hostedPaneMaxDim {
		return hostedPaneMaxDim
	}
	return v
}

func hostedPaneEnv(d *Daemon, spec hostedPaneSpec, hp *hostedPane) []string {
	env := guestenv.WithoutHostMultiplexer(os.Environ())

	term := guestenv.PaneTerm(spec.Term)
	colorTerm := spec.ColorTerm
	if colorTerm == "" {
		colorTerm = "truecolor"
	}
	env = append(env, "TERM="+term, "COLORTERM="+colorTerm)
	env = append(env, "TERM_PROGRAM="+guestenv.TermProgramFor(spec.Command, false, false))
	env = append(env, "TERM_PROGRAM_VERSION="+guestenv.TermProgramVersion())
	if spec.Session != "" {
		env = append(env, "TUIOS_SESSION_REMOTE="+spec.Session)
	}
	if host := d.hostedPaneHostName(); host != "" {
		env = append(env, "TUIOS_HOST="+host)
	}
	env = append(env, "TUIOS_PANE_HOSTED=1")
	if hp != nil && hp.window != "" {
		env = append(env, "TUIOS_PANE_ID="+hp.window)
	}
	return env
}

func (d *Daemon) hostedPaneHostName() string {
	if d.manager != nil {
		return d.manager.HostName()
	}
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

func (d *Daemon) lookupHostedPane(id string) *hostedPane {
	d.hostedPanesMu.Lock()
	defer d.hostedPanesMu.Unlock()
	return d.hostedPanes[id]
}

func (d *Daemon) forgetHostedPane(id string) {
	d.hostedPanesMu.Lock()
	hp := d.hostedPanes[id]
	delete(d.hostedPanes, id)
	d.hostedPanesMu.Unlock()
	if hp != nil {
		hp.close()
	}
}

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

func (hp *hostedPane) resize(width, height, xpixel, ypixel int) error {
	clampPx := func(v int) int { return min(max(v, 0), 65535) }
	return ptyspawn.SetWinsize(hp.pty, clampHostedDim(width), clampHostedDim(height), clampPx(xpixel), clampPx(ypixel))
}

func (hp *hostedPane) close() {
	hp.closeOnce.Do(func() {
		if hp.stop != nil {
			close(hp.stop)
		}
		hp.calls.close()
		if hp.cmd != nil && hp.cmd.Process != nil {
			_ = hp.cmd.Process.Kill()
		}
		_ = hp.pty.Close()
		hp.connMu.Lock()
		hp.closed = true
		c := hp.conn
		hp.conn = nil
		if hp.orphan != nil {
			hp.orphan.Stop()
			hp.orphan = nil
		}
		hp.connMu.Unlock()
		if c != nil {
			_ = c.Close()
		}
		if hp.onEnd != nil {
			hp.onEnd()
		}
	})
}

func (d *Daemon) relayHostedPane(cs *connState, br *bufio.Reader, hp *hostedPane, from int64) {
	conn := cs.conn
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	if !hp.attach(conn, from) {
		_ = conn.Close()
		return
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-d.ctx.Done():
			hp.close()
		case <-cs.done:
		case <-done:
			return
		}
		_ = conn.Close()
	}()

	_, _ = io.Copy(lockedInput{hp}, br)
	close(done)
	hp.detach(conn)
}

func (hp *hostedPane) foreground() (foregroundInfo, bool) {
	if hp.cmd == nil || hp.cmd.Process == nil {
		return foregroundInfo{}, false
	}
	shellPID := hp.cmd.Process.Pid
	info, running := foregroundProcess(shellPID)
	info.shellPID = shellPID
	return info, running
}

func (hp *hostedPane) processCwd() (string, bool) {
	if hp.cmd == nil || hp.cmd.Process == nil {
		return "", false
	}
	return ptyspawn.ProcessCwd(hp.cmd.Process.Pid)
}
