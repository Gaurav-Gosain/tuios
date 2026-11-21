package termio

import (
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/x/term"
)

type writer struct {
	out     io.Writer
	wmu     sync.Mutex
	mu      sync.Mutex
	pending []byte
	queued  bool
	stopped bool
	wake    chan struct{}
	quit    chan struct{}
	done    chan struct{}
}

func newWriter(out io.Writer) *writer {
	w := &writer{out: out, wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	go w.loop()
	return w
}

func (w *writer) enqueue(b []byte) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.pending = append(w.pending, b...)
	w.queued = true
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *writer) loop() {
	defer close(w.done)
	var buf []byte
	for {
		select {
		case <-w.wake:
			buf = w.drain(buf)
		case <-w.quit:
			w.drain(buf)
			return
		}
	}
}

func (w *writer) drain(buf []byte) []byte {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	w.mu.Lock()
	if !w.queued {
		w.mu.Unlock()
		return buf
	}
	buf, w.pending = w.pending, buf[:0]
	w.queued = false
	w.mu.Unlock()
	_, _ = w.out.Write(buf)
	return buf
}

func (w *writer) flush() { w.drain(nil) }

func (w *writer) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.mu.Unlock()
	close(w.quit)
	<-w.done
}

func (w *writer) writeNow(b []byte) {
	if len(b) == 0 {
		return
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	_, _ = w.out.Write(b)
}

func makeRaw(f *os.File) (restore func()) {
	if f == nil || !term.IsTerminal(f.Fd()) {
		return func() {}
	}
	state, err := term.MakeRaw(f.Fd())
	if err != nil {
		return func() {}
	}
	return func() { _ = term.Restore(f.Fd(), state) }
}
