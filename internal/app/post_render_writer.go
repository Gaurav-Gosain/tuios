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
// It is also the one writer the host terminal is reached through. The
// renderer, the kitty and sixel passthroughs, and the text-sizing and
// cursor-shape paths all write to the terminal from different goroutines,
// and what keeps one from landing inside another's escape sequence is that
// they now share this *os.File: the runtime takes a per-file write lock, so
// each Write is delivered whole even when the terminal takes it in pieces.
// Graphics used to go out through a second, private open of /dev/tty, a
// different file with a different lock and therefore no ordering at all
// against a frame. Callers must hand a whole sequence to one Write; a
// sequence emitted in parts can still be split at the seams between them.
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
	// Held across both file writes: releasing it before them would let another
	// writer in between the frame and the data that has to follow it, and
	// would leave every other host writer unordered against this one.
	w.mu.Lock()
	defer w.mu.Unlock()

	pending := w.pending
	w.pending = nil

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

// WriteHost writes one sequence to the host terminal through the single
// serialized writer, or straight to stdout when there is none (the tape
// player, which builds no passthroughs). Parts are joined so the sequence
// reaches the terminal as one Write and nothing can be written inside it.
func (m *OS) WriteHost(parts ...[]byte) {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	if total == 0 {
		return
	}
	buf := parts[0]
	if len(parts) > 1 {
		buf = make([]byte, 0, total)
		for _, part := range parts {
			buf = append(buf, part...)
		}
	}
	if m.PostRenderWriter != nil {
		_, _ = m.PostRenderWriter.Write(buf)
		return
	}
	_, _ = os.Stdout.Write(buf)
}
