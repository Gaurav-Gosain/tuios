// Package webshell is an in-memory terminal and a small fake shell for the
// browser build of tuios, where there is no kernel pty and no process to exec.
//
// A Pty stands in for xpty.Pty: the window writes keystrokes to it and reads
// the guest's output back, exactly as it would with a real pseudo-terminal.
// Start runs a Go program in place of the command the window asked for. The
// package is pure Go and has no build tags, so it is tested natively.
package webshell

import (
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// pipe is an unbounded byte queue. Writes never block, reads block until there
// is data or the pipe is closed.
type pipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newPipe() *pipe {
	p := &pipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	p.buf = append(p.buf, b...)
	p.cond.Broadcast()
	return len(b), nil
}

func (p *pipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.closed {
		p.cond.Wait()
	}
	if len(p.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return n, nil
}

func (p *pipe) Close() error {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	return nil
}

// Pty is an in-memory pseudo-terminal. It satisfies xpty.Pty.
type Pty struct {
	in, out *pipe // in: window to guest, out: guest to window
	cols    atomic.Int32
	rows    atomic.Int32
	resized chan struct{}

	started   atomic.Bool
	done      chan struct{}
	closeOnce sync.Once
	exitErr   error
}

// NewPty makes a pty of the given size with nothing running on it.
func NewPty(cols, rows int) *Pty {
	p := &Pty{
		in:      newPipe(),
		out:     newPipe(),
		resized: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	p.cols.Store(int32(max(cols, 1)))
	p.rows.Store(int32(max(rows, 1)))
	return p
}

// Read returns what the guest wrote.
func (p *Pty) Read(b []byte) (int, error) { return p.out.Read(b) }

// Write delivers input to the guest.
func (p *Pty) Write(b []byte) (int, error) { return p.in.Write(b) }

// Close ends the guest and unblocks both directions.
func (p *Pty) Close() error {
	p.closeOnce.Do(func() {
		_ = p.in.Close()
		_ = p.out.Close()
	})
	return nil
}

// Fd has no descriptor to return. term.IsTerminal says no to it.
func (p *Pty) Fd() uintptr { return ^uintptr(0) }

// Name is what a tty(1) would print.
func (p *Pty) Name() string { return "/dev/pts/web" }

// Resize changes the size and tells the guest, the way SIGWINCH would.
func (p *Pty) Resize(cols, rows int) error {
	p.cols.Store(int32(max(cols, 1)))
	p.rows.Store(int32(max(rows, 1)))
	select {
	case p.resized <- struct{}{}:
	default:
	}
	return nil
}

// Size reports the current size.
func (p *Pty) Size() (int, int, error) {
	return int(p.cols.Load()), int(p.rows.Load()), nil
}

// Start runs the program cmd names in place of cmd. Anything unknown runs the
// shell, since the window asked for the user's shell.
func (p *Pty) Start(cmd *exec.Cmd) error {
	if !p.started.CompareAndSwap(false, true) {
		return errors.New("webshell: already started")
	}
	name := ""
	var args []string
	if cmd != nil {
		name = filepath.Base(cmd.Path)
		if len(cmd.Args) > 1 {
			args = cmd.Args[1:]
		}
	}
	env := map[string]string{}
	if cmd != nil {
		for _, kv := range cmd.Env {
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					env[kv[:i]] = kv[i+1:]
					break
				}
			}
		}
	}
	t := newTTY(p, env)
	go func() {
		defer close(p.done)
		defer func() { _ = p.Close() }()
		defer t.stop()
		if prog, ok := programs[name]; ok && name != "sh" {
			p.exitErr = exitError(prog(t, args))
			return
		}
		p.exitErr = exitError(runShell(t))
	}()
	return nil
}

// Wait blocks until the guest exits, and returns nil for a zero status.
func (p *Pty) Wait() error {
	<-p.done
	return p.exitErr
}

func exitError(code int) error {
	if code == 0 {
		return nil
	}
	return errors.New("exit status " + strconv.Itoa(code))
}
