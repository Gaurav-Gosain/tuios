package app

import (
	"bytes"
	"io"
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
	// frameHook returns output that must follow each frame: the sixel
	// images drawn on it. It runs only for the renderer's writes, not for
	// WriteHost, because it answers for the frame being written. See
	// SixelPassthrough.FrameBytes.
	frameHook func(frame []byte) []byte
}

// SetFrameHook installs the function whose output is written after each
// frame. See PostRenderWriter.frameHook.
func (w *PostRenderWriter) SetFrameHook(fn func(frame []byte) []byte) {
	w.mu.Lock()
	w.frameHook = fn
	w.mu.Unlock()
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
	return w.write(p, true)
}

// write is Write, with the frame hook run only for a frame.
func (w *PostRenderWriter) write(p []byte, frame bool) (n int, err error) {
	// Held across both file writes: releasing it before them would let another
	// writer in between the frame and the data that has to follow it, and
	// would leave every other host writer unordered against this one.
	w.mu.Lock()
	defer w.mu.Unlock()

	pending := w.pending
	w.pending = nil
	if frame && w.frameHook != nil {
		if after := w.frameHook(p); len(after) > 0 {
			pending = append(pending, after...)
		}
	}

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
		buf = append(buf, p...)
		buf = append(buf, pending...)
		buf = append(buf, frameSyncEnd...)
	} else {
		buf = appendInsideSync(buf, p, pending)
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
		_, _ = m.PostRenderWriter.write(buf, false)
		return
	}
	_, _ = os.Stdout.Write(buf)
}

// appendInsideSync appends frame and then after, putting after inside the
// frame's own synchronized update when the frame ends with one, so the host
// presents the images in the same step as the text they sit on.
func appendInsideSync(buf, frame, after []byte) []byte {
	if len(after) > 0 && bytes.HasSuffix(frame, frameSyncEnd) {
		buf = append(buf, frame[:len(frame)-len(frameSyncEnd)]...)
		buf = append(buf, after...)
		return append(buf, frameSyncEnd...)
	}
	buf = append(buf, frame...)
	return append(buf, after...)
}

// FrameHookWriter is the renderer's writer for a session whose terminal is
// not this process's: an SSH client or a browser. It does for them what
// PostRenderWriter does for a local terminal's frames: the output of the
// frame hook goes out after each frame, in the same write.
type FrameHookWriter struct {
	w    io.Writer
	mu   sync.Mutex
	hook func(frame []byte) []byte
}

// NewFrameHookWriter wraps the writer a session's renderer writes to.
func NewFrameHookWriter(w io.Writer) *FrameHookWriter {
	return &FrameHookWriter{w: w}
}

// SetHook installs the function whose output follows each frame.
func (f *FrameHookWriter) SetHook(fn func(frame []byte) []byte) {
	f.mu.Lock()
	f.hook = fn
	f.mu.Unlock()
}

func (f *FrameHookWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	hook := f.hook
	f.mu.Unlock()
	var after []byte
	if hook != nil {
		after = hook(p)
	}
	if len(after) == 0 {
		return f.w.Write(p)
	}
	buf := appendInsideSync(make([]byte, 0, len(p)+len(after)), p, after)
	if _, err := f.w.Write(buf); err != nil {
		return 0, err
	}
	return len(p), nil
}

// FrameHookFile is FrameHookWriter over a file, for a renderer whose output
// has to stay a terminal file: bubbletea reads the size and sets modes through
// it. tuios-web's PTY slave is one.
type FrameHookFile struct {
	*os.File
	hw *FrameHookWriter
}

// NewFrameHookFile wraps a terminal file a session's renderer writes to.
func NewFrameHookFile(f *os.File) *FrameHookFile {
	return &FrameHookFile{File: f, hw: NewFrameHookWriter(f)}
}

// SetHook installs the function whose output follows each frame.
func (f *FrameHookFile) SetHook(fn func(frame []byte) []byte) { f.hw.SetHook(fn) }

func (f *FrameHookFile) Write(p []byte) (int, error) { return f.hw.Write(p) }

// FrameWriter is the renderer's writer seen from the graphics side: it runs a
// hook after each frame, and takes output that is not tied to one.
type FrameWriter interface {
	SetFrameHook(fn func(frame []byte) []byte)
	WriteOutside(p []byte)
}

// WriteOutside writes p to the terminal without running the frame hook.
func (w *PostRenderWriter) WriteOutside(p []byte) { _, _ = w.write(p, false) }

// SetFrameHook is SetHook, for FrameWriter.
func (f *FrameHookWriter) SetFrameHook(fn func(frame []byte) []byte) { f.SetHook(fn) }

// WriteOutside writes p to the session without running the frame hook.
func (f *FrameHookWriter) WriteOutside(p []byte) { _, _ = f.w.Write(p) }

// SetFrameHook is SetHook, for FrameWriter.
func (f *FrameHookFile) SetFrameHook(fn func(frame []byte) []byte) { f.hw.SetHook(fn) }

// WriteOutside writes p to the terminal without running the frame hook.
func (f *FrameHookFile) WriteOutside(p []byte) { f.hw.WriteOutside(p) }

// ConnectFrameWriter makes w the way this session's images reach the
// terminal: after each frame through the hook, and whenever a background
// encode finishes through WriteOutside.
func (m *OS) ConnectFrameWriter(w FrameWriter) {
	w.SetFrameHook(m.GraphicsFrameBytes)
	if m.SixelPassthrough != nil {
		m.SixelPassthrough.mu.Lock()
		m.SixelPassthrough.direct = w.WriteOutside
		m.SixelPassthrough.mu.Unlock()
	}
}
