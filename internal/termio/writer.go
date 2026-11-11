package termio

import (
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/x/term"
)

// writer owns the terminal write, on its own goroutine, so a slow terminal
// never holds up Update. Frames queued while a write is in flight are joined
// and go out in the next write. No byte is dropped: the diff assumes that
// every byte it made reached the terminal. An empty frame still makes a
// write, for a writer behind it that clears a debt on each frame write.
type writer struct {
	out io.Writer
	// wmu is held for a whole drain, so two drains never reorder bytes.
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

// drain writes what is queued, reusing buf, and returns buf for the next
// call.
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

// flush writes what is queued now, on the caller's goroutine.
func (w *writer) flush() { w.drain(nil) }

// stop writes what is queued and ends the goroutine.
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

// writeNow writes b in place, after anything queued.
func (w *writer) writeNow(b []byte) {
	if len(b) == 0 {
		return
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	_, _ = w.out.Write(b)
}

// Raw puts the terminal f in raw mode, as Bubble Tea does for its input when
// it runs a renderer, and returns what puts it back. A file that is not a
// terminal is left alone.
func Raw(f *os.File) (restore func(), err error) {
	if f == nil || !term.IsTerminal(f.Fd()) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(f.Fd())
	if err != nil {
		return func() {}, err
	}
	return func() { _ = term.Restore(f.Fd(), state) }, nil
}
