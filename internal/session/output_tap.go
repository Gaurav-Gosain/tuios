package session

import "sync"

const outputTapMax = 4 << 20

type outputTap struct {
	mu  sync.Mutex
	buf []byte
}

func (t *outputTap) add(data []byte) {
	t.mu.Lock()
	t.buf = append(t.buf, data...)
	if over := len(t.buf) - outputTapMax; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	t.mu.Unlock()
}

func (t *outputTap) take() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.buf
	t.buf = nil
	return b
}

func (t *outputTap) pending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buf) > 0
}

func (p *PTY) addOutputTap() *outputTap {
	t := &outputTap{}
	p.outputMu.Lock()
	p.outputTaps = append(p.outputTaps, t)
	p.outputMu.Unlock()
	return t
}

func (p *PTY) removeOutputTap(t *outputTap) {
	p.outputMu.Lock()
	for i, have := range p.outputTaps {
		if have == t {
			p.outputTaps = append(p.outputTaps[:i:i], p.outputTaps[i+1:]...)
			break
		}
	}
	p.outputMu.Unlock()
}
