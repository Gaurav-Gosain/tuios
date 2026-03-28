package app

import (
	"os"
	"sync"
)

// PostRenderWriter wraps *os.File (stdout) to intercept bubbletea's
// frame output and append queued graphics data (OSC 66 text sizing)
// after each write. This ensures OSC 66 multicell characters are written
// AFTER bubbletea's cell-based rendering, preventing overwrites.
//
// It fully satisfies the term.File interface (io.ReadWriteCloser + Fd)
// by embedding *os.File and only overriding Write.
type PostRenderWriter struct {
	*os.File
	mu      sync.Mutex
	pending []byte
}

func NewPostRenderWriter(f *os.File) *PostRenderWriter {
	return &PostRenderWriter{File: f}
}

// Write passes through to the underlying file, then appends any pending
// post-render data. This way, queued OSC 66 data is written immediately
// after bubbletea's frame content.
func (w *PostRenderWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	pending := w.pending
	w.pending = nil
	w.mu.Unlock()

	n, err = w.File.Write(p)

	if len(pending) > 0 {
		_, _ = w.File.Write(pending)
	}

	return
}

// QueuePostRender queues data to be written after bubbletea's next Write.
func (w *PostRenderWriter) QueuePostRender(data []byte) {
	if len(data) == 0 {
		return
	}
	w.mu.Lock()
	w.pending = append(w.pending, data...)
	w.mu.Unlock()
}

// ClearPending discards all pending data. Used when screen is cleared
// to prevent stale OSC 66 data from being re-emitted.
func (w *PostRenderWriter) ClearPending() {
	w.mu.Lock()
	w.pending = nil
	w.mu.Unlock()
}
