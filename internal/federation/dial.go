package federation

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// Transport is one open pipe to a remote proxy. Diagnostic carries whatever the
// transport captured that explains a failure, bounded; for the ssh transport it
// is ssh's own stderr, which is the only place the real reason ever appears
// ("Permission denied", "Host key verification failed", "command not found").
type Transport interface {
	io.ReadWriteCloser
	Diagnostic() string
}

type exitReporter interface {
	Exited() (bool, error)
}

// Dialer opens a transport to one host. It is an interface point so tests drive
// the whole link, framing, proxy and all, over a subprocess that is not ssh.
type Dialer func(ctx context.Context, h Host) (Transport, error)

// SSHDialer is the production transport: `ssh <opts> <addr> <command>
// stdio-proxy`, per section 4.
//
// Two options are forced and they are worth stating.
//
// BatchMode=yes is a deliberate departure from the design document, which lists
// known_hosts prompting among the things running the ssh binary inherits for
// free. A hub daemon has no terminal. A prompt it cannot answer does not ask
// the user anything, it hangs the link forever, which is the exact failure
// section 7 forbids. So the daemon's links never prompt: an unknown host key or
// a missing key fails immediately and `tuios hosts` reports it with ssh's own
// words. The user resolves it once by running ssh themselves.
//
// ConnectTimeout makes the child give up on a dead machine on its own, so a
// powered-off host is reported rather than leaving a process parked until the
// context expires.
//
// The keepalive options are defaults rather than forced settings, which is why
// they are appended after the host's own SSHOptions: ssh takes the first value
// it obtains for a keyword, so a user who sets ServerAliveInterval themselves
// still wins.
func SSHDialer(sshBinary string) Dialer {
	return SSHDialerEnv(sshBinary, nil)
}

// SSHDialerEnv is SSHDialer with the ssh's environment read from env for the
// host at each dial. A nil env, or one that returns nil, runs ssh with this
// process's environment. The daemon uses it to point SSH_AUTH_SOCK at the
// agent link it keeps for a host whose link forwards the agent.
func SSHDialerEnv(sshBinary string, env func(Host) []string) Dialer {
	if sshBinary == "" {
		sshBinary = "ssh"
	}
	return func(ctx context.Context, h Host) (Transport, error) {
		var e []string
		if env != nil {
			e = env(h)
		}
		return commandDialerEnv(e, sshBinary, linkArgs(h)...)(ctx, h)
	}
}

func linkArgs(h Host) []string {
	return SSHArgs(h, h.remoteCommand(true, "stdio-proxy"))
}

// SSHArgs is the ssh argv, without the program name, that runs remoteCmd on
// h with the options every link uses: BatchMode, the host's connect timeout,
// no pseudo-terminal, the host's own SSHOptions and the keepalives. tuios
// hosts sync runs its own commands through it, so they reach the host exactly
// the way the link does.
func SSHArgs(h Host, remoteCmd string) []string {
	secs := max(int(h.connectTimeout().Seconds()), 1)
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(secs),
		"-T",
	}
	args = append(args, h.SSHOptions...)
	args = append(args, sharingOptions(h)...)
	args = append(args, keepaliveOptions()...)
	return append(args, "--", h.Addr, remoteCmd)
}

const sharingPersist = "10m"

func sharingOptions(h Host) []string {
	if h.ControlPath == "" && h.fresh {
		return freshOptions()
	}
	if h.ControlPath == "" {
		return reuseOptions(h)
	}
	return []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + h.ControlPath,
		"-o", "ControlPersist=" + sharingPersist,
	}
}

// StopSharingArgs is the ssh argv, without the program name, that stops the
// master connection at h.ControlPath.
func StopSharingArgs(h Host) []string {
	return []string{"-o", "ControlPath=" + h.ControlPath, "-O", "exit", "--", h.Addr}
}

const (
	sshServerAliveInterval = 15
	sshServerAliveCountMax = 3
)

func keepaliveOptions() []string {
	return []string{
		"-o", "ServerAliveInterval=" + strconv.Itoa(sshServerAliveInterval),
		"-o", "ServerAliveCountMax=" + strconv.Itoa(sshServerAliveCountMax),
		"-o", "TCPKeepAlive=yes",
	}
}

// KeepaliveWindow is how long a dead path can go unnoticed by ssh. The client's
// reconnect budget is measured against it: a budget shorter than this would
// give up before ssh had even reported the link gone.
const KeepaliveWindow = sshServerAliveInterval * sshServerAliveCountMax * time.Second

// CommandDialer runs any command and speaks the link over its stdio. SSHDialer
// is built on it, and a test uses it to run the real `tuios stdio-proxy`
// directly, which exercises the framing, the proxy and the daemon socket
// without needing an ssh server or touching the user's ssh configuration.
func CommandDialer(name string, args ...string) Dialer {
	return commandDialerEnv(nil, name, args...)
}

func commandDialerEnv(env []string, name string, args ...string) Dialer {
	return func(ctx context.Context, _ Host) (Transport, error) {
		cmd := childproc.Command(name, args...)
		cmd.Env = env
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		t := &cmdTransport{cmd: cmd, in: stdin, out: stdout, done: make(chan struct{})}
		cmd.Stderr = &boundedBuffer{limit: diagnosticLimit}
		if err := cmd.Start(); err != nil {
			return nil, errfmt.Errorf("could not run %s: %w", name, err)
		}
		go func() {
			t.exitErr = cmd.Wait()
			close(t.done)
		}()
		return t, nil
	}
}

const diagnosticLimit = 4 << 10

type cmdTransport struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out io.ReadCloser

	done    chan struct{}
	exitErr error
	once    sync.Once
}

func (t *cmdTransport) Read(p []byte) (int, error)  { return t.out.Read(p) }
func (t *cmdTransport) Write(p []byte) (int, error) { return t.in.Write(p) }

func (t *cmdTransport) Close() error {
	t.once.Do(func() {
		_ = t.in.Close()
		_ = t.out.Close()
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		select {
		case <-t.done:
		case <-time.After(2 * time.Second):
		}
	})
	return nil
}

func (t *cmdTransport) Exited() (bool, error) {
	select {
	case <-t.done:
		return true, t.exitErr
	default:
		return false, nil
	}
}

func (t *cmdTransport) Diagnostic() string {
	if b, ok := t.cmd.Stderr.(*boundedBuffer); ok {
		return withoutStaleMasterLines(WithoutApprovedBanner(b.String()))
	}
	return ""
}

type boundedBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - len(b.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}
