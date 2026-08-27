package federation

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Transport is one open pipe to a remote proxy. Diagnostic carries whatever the
// transport captured that explains a failure, bounded; for the ssh transport it
// is ssh's own stderr, which is the only place the real reason ever appears
// ("Permission denied", "Host key verification failed", "command not found").
type Transport interface {
	io.ReadWriteCloser
	Diagnostic() string
}

// exitReporter is implemented by a transport that runs a child process. It
// separates two failures that look identical from the pipe: the program gave up
// (ssh could not reach the machine, or the remote tuios is missing), and the
// program is running but is not a tuios link. Both end the read with no
// preamble; only one of them is the machine's fault.
type exitReporter interface {
	// Exited reports whether the child has already finished, and its wait
	// error when it has.
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
func SSHDialer(sshBinary string) Dialer {
	if sshBinary == "" {
		sshBinary = "ssh"
	}
	return func(ctx context.Context, h Host) (Transport, error) {
		secs := int(h.connectTimeout().Seconds())
		if secs < 1 {
			secs = 1
		}
		args := []string{
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=" + strconv.Itoa(secs),
			// No pseudo-terminal: the pipe carries frames, and a pty would
			// translate them.
			"-T",
		}
		args = append(args, h.SSHOptions...)
		args = append(args, h.Addr, h.command(), "stdio-proxy")
		return CommandDialer(sshBinary, args...)(ctx, h)
	}
}

// CommandDialer runs any command and speaks the link over its stdio. SSHDialer
// is built on it, and a test uses it to run the real `tuios stdio-proxy`
// directly, which exercises the framing, the proxy and the daemon socket
// without needing an ssh server or touching the user's ssh configuration.
func CommandDialer(name string, args ...string) Dialer {
	return func(ctx context.Context, _ Host) (Transport, error) {
		// The context is not attached to the command: a dial context expires
		// once the handshake is done, and CommandContext would kill the child
		// at that moment. Close ends the process.
		cmd := exec.Command(name, args...)
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
			return nil, fmt.Errorf("could not run %s: %w", name, err)
		}
		go func() {
			t.exitErr = cmd.Wait()
			close(t.done)
		}()
		return t, nil
	}
}

// diagnosticLimit bounds captured stderr. The far side is untrusted and stderr
// is unframed, so it gets a ceiling like everything else that crosses.
const diagnosticLimit = 4 << 10

type cmdTransport struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out io.ReadCloser

	// done closes when the child has been reaped. exitErr is its wait error,
	// read only after done is closed, so Exited never blocks and Close never
	// calls Wait twice.
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
		// Wait is owned by the goroutine started at dial, so Close waits on it
		// rather than calling Wait a second time.
		select {
		case <-t.done:
		case <-time.After(2 * time.Second):
		}
	})
	return nil
}

// Exited reports whether the child process has already finished. It never
// blocks: a still-running child is reported as running.
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
		return b.String()
	}
	return ""
}

// boundedBuffer keeps at most limit bytes of what is written to it and drops
// the rest. It is what stops a remote that writes forever on stderr from
// growing the hub's memory.
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
