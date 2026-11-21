package app

import (
	"bytes"
	"io"
	"os"
	"sync"
	"time"
)

// PostRenderWriter wraps the client's terminal (stdout) to intercept bubbletea's
// frame output and append queued graphics data (OSC 66 text sizing)
// after each write. This ensures OSC 66 multicell characters are written
// AFTER bubbletea's cell-based rendering, preventing overwrites.
//
// It is also the one writer the host terminal is reached through. The
// renderer, the kitty and sixel passthroughs, and the text-sizing and
// cursor-shape paths all write to the terminal from different goroutines,
// and what keeps one from landing inside another's escape sequence is that
// they share this writer: its lock is held across each write, and on a
// file the runtime also takes a per-file write lock, so each Write is
// delivered whole even when the terminal takes it in pieces. A second,
// private open of /dev/tty would be a different file with a different lock
// and therefore no ordering at all against a frame. Callers must hand a
// whole sequence to one Write; a sequence emitted in parts can still be
// split at the seams between them.
//
// The terminal is any io.Writer: a test hands it a pipe. Output is what the
// program writes to; on a real file it is also a term.File, so Bubble Tea
// sees a terminal.
type PostRenderWriter struct {
	out       io.Writer
	file      *os.File
	mu        sync.Mutex
	pending   []byte
	frameHook FrameHook
	frameOwed bool
	written   func()
	erased    []func(kept bool) []byte
	repaint   pictureRepaint
}

// FrameHook runs on each frame the renderer writes. out is the frame to send
// in its place, which is frame itself unless the hook took something out of
// it (see cursorPark.frame), and after is the output that has to follow it.
type FrameHook func(frame []byte) (out, after []byte)

// SetFrameHook installs the function whose output is written after each
// frame. See PostRenderWriter.frameHook.
func (w *PostRenderWriter) SetFrameHook(fn FrameHook) {
	w.mu.Lock()
	w.frameHook = fn
	w.mu.Unlock()
}

// AddEraseHook adds a function that runs for each frame that erases the host's
// screen, and whose output goes out behind that frame. kept says the frame was
// rewritten by keepPicturesFrame, so the host still holds the pictures. A resize
// can still have moved or dropped their placements: a host that loses rows
// scrolls its screen, and a placement goes with it. When kept is false the host
// dropped the pictures too.
//
// Bubble Tea erases on each resize and whenever it restarts its renderer, so
// whatever put a picture on the host has to hear about it, and this writer is
// the one place that sees every frame. A client with no such hook pays nothing.
func (w *PostRenderWriter) AddEraseHook(fn func(kept bool) []byte) {
	w.mu.Lock()
	w.erased = append(w.erased, fn)
	w.mu.Unlock()
}

// NewPostRenderWriter wraps out, the terminal the client draws on.
func NewPostRenderWriter(out io.Writer) *PostRenderWriter {
	f, _ := out.(*os.File)
	return &PostRenderWriter{out: out, file: f}
}

// File is the terminal file under the writer, or nil when it is not a file.
func (w *PostRenderWriter) File() *os.File { return w.file }

// Output is the writer a program renders to. On a file it is a term.File
// whose Write is this writer's, so Bubble Tea reads the size and sets the
// modes through it.
func (w *PostRenderWriter) Output() io.Writer {
	if w.file == nil {
		return w
	}
	return &postRenderFile{f: w.file, w: w}
}

type postRenderFile struct {
	f *os.File
	w *PostRenderWriter
}

func (f *postRenderFile) Read(p []byte) (int, error)  { return f.f.Read(p) }
func (f *postRenderFile) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f *postRenderFile) Close() error                { return f.f.Close() }
func (f *postRenderFile) Fd() uintptr                 { return f.f.Fd() }

func (w *PostRenderWriter) writeOut(buf []byte) (int, error) {
	if w.file != nil {
		return w.file.Write(buf)
	}
	return w.out.Write(buf)
}

var (
	frameSyncBegin = []byte("\x1b[?2026h")
	frameSyncEnd   = []byte("\x1b[?2026l")
)

var kittyGraphicsStart = []byte("\x1b_G")

const pictureRepaintDelay = 20 * time.Millisecond

var emptySyncUpdate = []byte("\x1b[?2026h\x1b[?2026l")

type pictureRepaint struct {
	mu    sync.Mutex
	timer *time.Timer
}

func (r *pictureRepaint) note(write func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer == nil {
		r.timer = time.AfterFunc(pictureRepaintDelay, write)
		return
	}
	r.timer.Reset(pictureRepaintDelay)
}

var hostEraseScreen = []byte("\x1b[2J")

var (
	rendererClear     = []byte("\x1b[H\x1b[2J")
	rendererClearKeep = []byte("\x1b[H\x1b[J")
)

func keepPicturesFrame(frame []byte) ([]byte, bool) {
	if !bytes.Contains(frame, rendererClear) {
		return frame, false
	}
	return bytes.ReplaceAll(frame, rendererClear, rendererClearKeep), true
}

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

func (w *PostRenderWriter) write(p []byte, frame bool) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	pending := w.pending
	w.pending = nil
	out := p
	if frame {
		w.frameOwed = false
	}
	if frame && w.frameHook != nil {
		var after []byte
		if out, after = w.frameHook(p); len(after) > 0 {
			pending = append(pending, after...)
		}
	}
	if frame && len(w.erased) > 0 && bytes.Contains(p, hostEraseScreen) {
		kept := !bytes.Contains(out, hostEraseScreen)
		for _, fn := range w.erased {
			pending = append(pending, fn(kept)...)
		}
	}

	if len(out) == 0 && len(pending) == 0 {
		return 0, nil
	}

	buf := make([]byte, 0, len(frameSyncBegin)+len(out)+len(pending)+len(frameSyncEnd))
	wrap := !bytes.Contains(out, frameSyncBegin)
	if wrap {
		buf = append(buf, frameSyncBegin...)
		buf = append(buf, out...)
		buf = append(buf, pending...)
		buf = append(buf, frameSyncEnd...)
	} else {
		buf = appendInsideSync(buf, out, pending)
	}

	if _, err = w.writeOut(buf); err != nil {
		return 0, err
	}
	if bytes.Contains(pending, kittyGraphicsStart) || (!frame && bytes.Contains(out, kittyGraphicsStart)) {
		w.repaint.note(w.writeRepaint)
	}
	if frame && w.written != nil {
		w.written()
	}
	return len(p), nil
}

func (w *PostRenderWriter) writeRepaint() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.writeOut(emptySyncUpdate)
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

// FrameComposed records that the client composed a frame that differs from
// the last one, so the renderer has a frame to write.
func (w *PostRenderWriter) FrameComposed() {
	w.mu.Lock()
	w.frameOwed = true
	w.mu.Unlock()
}

// FlushIfDrawn writes the queued data now when no frame is owed: the screen
// already shows the frame the data was queued for.
//
// The renderer writes nothing for a frame equal to the one it wrote last. A
// picture that is prepared off the update loop, such as a reply's image
// preview, arrives in such a frame: its cells were drawn blank a frame
// earlier. The data queued for it then waited for the next frame that
// changed a cell, so the picture showed only after the next key or click.
func (w *PostRenderWriter) FlushIfDrawn() {
	w.mu.Lock()
	owed := w.frameOwed || len(w.pending) == 0
	w.mu.Unlock()
	if owed {
		return
	}
	_, _ = w.write(nil, false)
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
	w       io.Writer
	mu      sync.Mutex
	hook    FrameHook
	written func()
	repaint pictureRepaint
}

// NewFrameHookWriter wraps the writer a session's renderer writes to.
func NewFrameHookWriter(w io.Writer) *FrameHookWriter {
	return &FrameHookWriter{w: w}
}

// SetHook installs the function whose output follows each frame.
func (f *FrameHookWriter) SetHook(fn FrameHook) {
	f.mu.Lock()
	f.hook = fn
	f.mu.Unlock()
}

func (f *FrameHookWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	hook, written := f.hook, f.written
	f.mu.Unlock()
	out, after := p, []byte(nil)
	if hook != nil {
		out, after = hook(p)
	}
	buf := out
	if len(after) > 0 {
		buf = appendInsideSync(make([]byte, 0, len(out)+len(after)), out, after)
	}
	if _, err := f.w.Write(buf); err != nil {
		return 0, err
	}
	if bytes.Contains(after, kittyGraphicsStart) {
		f.repaint.note(f.writeRepaint)
	}
	if written != nil {
		written()
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

func (f *FrameHookFile) Write(p []byte) (int, error) { return f.hw.Write(p) }

// FrameWriter is the renderer's writer seen from the graphics side: it runs a
// hook after each frame, and takes output that is not tied to one. It also
// says when the renderer has finished writing a frame (SetFrameWritten),
// which is when the event loop is free again: see frameWritten.
type FrameWriter interface {
	SetFrameHook(fn FrameHook)
	SetFrameWritten(fn func())
	WriteOutside(p []byte)
}

// SetFrameWritten installs the function that runs after each frame is
// written.
func (w *PostRenderWriter) SetFrameWritten(fn func()) {
	w.mu.Lock()
	w.written = fn
	w.mu.Unlock()
}

// SetFrameWritten installs the function that runs after each frame is
// written.
func (f *FrameHookWriter) SetFrameWritten(fn func()) {
	f.mu.Lock()
	f.written = fn
	f.mu.Unlock()
}

// SetFrameWritten is FrameHookWriter.SetFrameWritten.
func (f *FrameHookFile) SetFrameWritten(fn func()) { f.hw.SetFrameWritten(fn) }

// WriteOutside writes p to the terminal without running the frame hook.
func (w *PostRenderWriter) WriteOutside(p []byte) { _, _ = w.write(p, false) }

// SetFrameHook is SetHook, for FrameWriter.
func (f *FrameHookWriter) SetFrameHook(fn FrameHook) { f.SetHook(fn) }

// WriteOutside writes p to the session without running the frame hook.
func (f *FrameHookWriter) WriteOutside(p []byte) {
	_, _ = f.w.Write(p)
	if bytes.Contains(p, kittyGraphicsStart) {
		f.repaint.note(f.writeRepaint)
	}
}

func (f *FrameHookWriter) writeRepaint() { _, _ = f.w.Write(emptySyncUpdate) }

// SetFrameHook is SetHook, for FrameWriter.
func (f *FrameHookFile) SetFrameHook(fn FrameHook) { f.hw.SetHook(fn) }

// WriteOutside writes p to the terminal without running the frame hook.
func (f *FrameHookFile) WriteOutside(p []byte) { f.hw.WriteOutside(p) }

// ConnectFrameWriter makes w the way this session's images reach the
// terminal: after each frame through the hook, and whenever a background
// encode finishes through WriteOutside.
//
// The hook also keeps a parked host cursor hidden. See cursor_park.go.
func (m *OS) ConnectFrameWriter(w FrameWriter) {
	park := m.connectCursorPark(w)
	w.SetFrameWritten(m.frameWritten.note)
	w.SetFrameHook(func(frame []byte) ([]byte, []byte) {
		if m.keepHostPictures.Load() {
			var erased bool
			if frame, erased = keepPicturesFrame(frame); erased && m.KittyPassthrough != nil {
				m.KittyPassthrough.hostScreenErased.Store(true)
			}
		}
		after := m.GraphicsFrameBytes(frame)
		out, seq := park.frame(frame)
		if len(seq) > 0 {
			after = append(after[:len(after):len(after)], seq...)
		}
		return out, after
	})
	if m.SixelPassthrough != nil {
		m.SixelPassthrough.mu.Lock()
		m.SixelPassthrough.direct = w.WriteOutside
		m.SixelPassthrough.mu.Unlock()
	}
}
