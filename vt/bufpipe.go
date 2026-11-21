package vt

import (
	"bytes"
	"io"
	"sync"
)

type bufPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
	notify func()
}

func (p *bufPipe) setNotify(fn func()) {
	p.mu.Lock()
	p.notify = fn
	p.mu.Unlock()
}

const maxBufPipeBytes = 1 << 22

func newBufPipe() *bufPipe {
	p := &bufPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *bufPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if p.buf.Len()+len(b) > maxBufPipeBytes {
		p.mu.Unlock()
		return len(b), nil
	}
	p.buf.Write(b)
	p.cond.Signal()
	notify := p.notify
	p.mu.Unlock()
	if notify != nil {
		notify()
	}
	return len(b), nil
}

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

func (p *bufPipe) take() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf.Len() == 0 {
		return nil
	}
	out := make([]byte, p.buf.Len())
	_, _ = p.buf.Read(out)
	return out
}

func (p *bufPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.cond.Broadcast()
	}
	return nil
}
