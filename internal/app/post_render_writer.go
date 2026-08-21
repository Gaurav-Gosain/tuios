package app

import (
	"bytes"
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

// frameSyncBegin and frameSyncEnd bracket a synchronized update (DEC private
// mode 2026): the host buffers everything between them and presents it in one
// step. A terminal without the mode ignores both.
var (
	frameSyncBegin = []byte("\x1b[?2026h")
	frameSyncEnd   = []byte("\x1b[?2026l")
)

// Write passes bubbletea's frame through to the underlying file, appends any
// pending post-render data, and brackets the whole thing in a synchronized
// update so the host presents the frame whole.
//
// Without the bracket a frame is presented at whatever point the host happens
// to have read it to. The renderer writes only the cells that changed, jumping
// the cursor over the ones that did not, so a line a full-screen guest is
// rewriting in place is presented with some of its changed cells carrying the
// new text and the rest still carrying the old: the two strings interleaved on
// one line, with the untouched lines around it perfectly correct.
//
// bubbletea brackets frames itself, but only after the host answers a DECRQM
// query for mode 2026, and it does not even ask over SSH or on Apple Terminal
// (shouldQuerySynchronizedOutput). Anywhere the answer does not come back,
// every frame is presented torn. Doing it here does not depend on an answer.
// When bubbletea has already bracketed the frame its sequences are left alone,
// so the modes are never nested.
func (w *PostRenderWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	pending := w.pending
	w.pending = nil
	w.mu.Unlock()

	if len(p) == 0 && len(pending) == 0 {
		return 0, nil
	}

	// One Write for the whole frame: the bracket is worth nothing if this
	// code is itself what splits it across two syscalls.
	buf := make([]byte, 0, len(frameSyncBegin)+len(p)+len(pending)+len(frameSyncEnd))
	// Contains, not HasPrefix: bubbletea writes an alt-screen mode change
	// ahead of its own bracket, so the frame that enters or leaves the alt
	// screen does not start with one.
	wrap := !bytes.Contains(p, frameSyncBegin)
	if wrap {
		buf = append(buf, frameSyncBegin...)
	}
	buf = append(buf, p...)
	buf = append(buf, pending...)
	if wrap {
		buf = append(buf, frameSyncEnd...)
	}

	if _, err = w.File.Write(buf); err != nil {
		return 0, err
	}
	return len(p), nil
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
