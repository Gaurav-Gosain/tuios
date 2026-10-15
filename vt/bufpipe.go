package vt

import (
	"bytes"
	"io"
	"sync"
)

// bufPipe is an in-memory pipe whose Write never blocks (it appends to an
// internal buffer) and whose Read blocks until data is available or the pipe is
// closed. The emulator writes query responses (mode reports, mouse, colors,
// in-band resize) from inside Terminal.Write, which runs under the window IO
// write lock; a blocking pipe there deadlocks the PTY reader against the
// response drainer, so these writes must never block.
type bufPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

// maxBufPipeBytes bounds the buffer so a guest that spams query-generating
// sequences while the drainer is stalled cannot grow it without limit.
const maxBufPipeBytes = 1 << 22 // 4 MiB

func newBufPipe() *bufPipe {
	p := &bufPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Write appends data and never blocks. It drops the write if the pipe is closed
// or the buffer is already at the cap (the drainer is not keeping up).
func (p *bufPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	if p.buf.Len()+len(b) > maxBufPipeBytes {
		return len(b), nil
	}
	p.buf.Write(b)
	p.cond.Signal()
	return len(b), nil
}

// Read blocks until data is available or the pipe is closed, returning io.EOF
// only once the buffer is drained and closed.
func (p *bufPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.buf.Len() == 0 && !p.closed {
		p.cond.Wait()
	}
	if p.buf.Len() == 0 && p.closed {
		return 0, io.EOF
	}
	return p.buf.Read(b) //nolint:wrapcheck
}

// Close unblocks any waiting Read.
func (p *bufPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.cond.Broadcast()
	}
	return nil
}
