package session

import "sync"

// outputTapMax bounds what a tap holds between two reads by its waiter. The
// waiter drains it on every output event, at most waitOutputMinGap apart, so
// only a program writing megabytes inside one gap reaches it. Past it the
// oldest bytes go: a waiter that far behind has lost them either way.
const outputTapMax = 4 << 20

// outputTap is a copy of a pane's output from the moment it was added: what
// wait-for window-output with new matches against. It is added under the
// same lock the reader appends to the ring under, so the cut is exact: every
// chunk read after it was added is in it, and none read before.
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

// take returns what the tap holds and empties it.
func (t *outputTap) take() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.buf
	t.buf = nil
	return b
}

// pending says whether anything arrived since the last take.
func (t *outputTap) pending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buf) > 0
}

// addOutputTap starts copying the pane's output into a new tap. The caller
// removes it with removeOutputTap.
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
