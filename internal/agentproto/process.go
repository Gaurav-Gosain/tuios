package agentproto

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
)

// Protocols the package speaks, by the name start-agent --protocol takes.
const (
	ProtocolACP   = "acp"
	ProtocolCodex = "codex"
)

// Protocols lists the protocol names, for validation and help.
var Protocols = []string{ProtocolACP, ProtocolCodex}

// CheckProtocol returns an error naming the protocols unless protocol is one.
func CheckProtocol(protocol string) error {
	if slices.Contains(Protocols, protocol) {
		return nil
	}
	return fmt.Errorf("unknown protocol %q: use %s", protocol, strings.Join(Protocols, " or "))
}

// NewAgent makes the client for protocol over an agent whose stdout is r and
// whose stdin is w.
func NewAgent(protocol string, r io.Reader, w io.Writer, version string, emit func(Event)) (Agent, error) {
	switch protocol {
	case ProtocolACP:
		return NewACP(r, w, version, emit), nil
	case ProtocolCodex:
		return NewCodex(r, w, version, emit), nil
	}
	return nil, CheckProtocol(protocol)
}

// Process is an agent started with pipes for its stdin and stdout.
//
// It starts in a session of its own, with no controlling terminal (on Windows,
// with no console), so it cannot open the pane's terminal and write to it past
// the transcript's cleaning: everything it shows goes through the protocol.
type Process struct {
	cmd    *exec.Cmd
	Stdout io.ReadCloser
	Stdin  io.WriteCloser
	stderr *tailBuffer
	once   sync.Once
}

// StartProcess starts argv in dir with env added to this process's own.
func StartProcess(argv []string, dir string, env []string) (*Process, error) {
	if len(argv) == 0 {
		return nil, errors.New("no agent command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	detach(cmd)
	// Plain pipes rather than StdoutPipe, so reaping the process does not
	// close its stdout before the last message on it has been read.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = inR, outW
	tail := &tailBuffer{max: 4096}
	cmd.Stderr = tail
	err = cmd.Start()
	// The child has its ends now; the parent's copies would keep the pipes
	// open after it exits.
	_ = inR.Close()
	_ = outW.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, err
	}
	p := &Process{cmd: cmd, Stdout: outR, Stdin: inW, stderr: tail}
	go func() { _ = cmd.Wait() }()
	return p, nil
}

// Stderr is the end of what the agent wrote to stderr.
func (p *Process) Stderr() string { return p.stderr.String() }

// Stop closes the agent's stdin, which asks a well-behaved agent to exit, and
// kills it and what it started.
func (p *Process) Stop() {
	p.once.Do(func() {
		_ = p.Stdin.Close()
		kill(p.cmd)
	})
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimRight(string(t.buf), "\n"), "\n")
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	return strings.Join(lines, "\n")
}
