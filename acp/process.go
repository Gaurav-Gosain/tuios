package acp

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Process is the stdio transport: each Dial starts the agent program with
// pipes for its stdin and stdout, after it stops the program an earlier Dial
// started. Use it as a pointer; it must not be copied after the first Dial.
type Process struct {
	// Path is the program, found in PATH when it holds no separator.
	Path string
	// Args are the arguments after the program name.
	Args []string
	// Dir is the working directory, this process's when empty.
	Dir string
	// Env is added to this process's environment.
	Env []string
	// Stderr receives what the agent writes to its stderr. Nil discards it.
	Stderr io.Writer
	// StopTimeout is how long Close waits for the agent to exit after its
	// stdin closes before it kills the agent. Five seconds when 0.
	StopTimeout time.Duration

	mu  sync.Mutex
	cur *processConn
}

// Dial starts the agent.
func (p *Process) Dial(context.Context) (io.ReadWriteCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != nil {
		_ = p.cur.Close()
		p.cur = nil
	}
	if p.Path == "" {
		return nil, errors.New("acp: Process has no Path")
	}
	cmd := exec.Command(p.Path, p.Args...)
	cmd.Dir = p.Dir
	if len(p.Env) > 0 {
		cmd.Env = append(os.Environ(), p.Env...)
	}
	cmd.Stderr = p.Stderr
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
	err = cmd.Start()
	// The child holds its ends now. The parent's copies would keep the pipes
	// open after it exits.
	_ = inR.Close()
	_ = outW.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, err
	}
	pc := &processConn{cmd: cmd, in: inW, out: outR, exited: make(chan struct{}), timeout: p.StopTimeout}
	go func() {
		_ = cmd.Wait()
		close(pc.exited)
	}()
	p.cur = pc
	return pc, nil
}

// processConn is a started agent as one connection.
type processConn struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     io.ReadCloser
	exited  chan struct{}
	timeout time.Duration
	once    sync.Once
}

func (c *processConn) Read(b []byte) (int, error)  { return c.out.Read(b) }
func (c *processConn) Write(b []byte) (int, error) { return c.in.Write(b) }

// Close closes the agent's stdin, which the spec says ends every session of
// the connection and asks the agent to exit, and kills the agent when it has
// not exited by the timeout.
func (c *processConn) Close() error {
	c.once.Do(func() {
		_ = c.in.Close()
		timeout := c.timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-c.exited:
		case <-timer.C:
			_ = c.cmd.Process.Kill()
			<-c.exited
		}
		_ = c.out.Close()
	})
	return nil
}
