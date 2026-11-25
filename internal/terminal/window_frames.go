package terminal

import (
	"strconv"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

// A daemon can send a pane the client is behind on as frames of its screen
// instead of bytes (session.FeaturePTYFrames). The window tells it that it is
// behind once its queue holds more than lagMark bytes the emulator has not
// parsed, and again only after the queue ran empty or a frame came. A frame
// replaces the screen and leaves the history to the daemon: the lines that
// scrolled off while the pane was sent as frames are read from it by number.

// lagMark is how far the queue may run ahead of the emulator before the
// window reports it: 512 KiB, about 20 ms of parsing, or the pty_lag_kb knob.
var lagMark = sync.OnceValue(func() int64 {
	if kb, err := strconv.Atoi(testknob.String("pty_lag_kb")); err == nil && kb > 0 {
		return int64(kb) << 10
	}
	return 512 << 10
})

// SetLagReporter sets how the window tells its daemon it is behind on the
// pane's output, or with nil, that it does not.
func (w *Window) SetLagReporter(report func()) {
	if report == nil {
		w.lagReport.Store(nil)
		return
	}
	w.lagReport.Store(&report)
}

// reportLag tells the daemon the window is behind, once a backlog.
func (w *Window) reportLag() {
	if report := w.lagReport.Load(); report != nil && w.lagSent.CompareAndSwap(false, true) {
		(*report)()
	}
}

// FrameApplied records that a frame from the daemon replaced the screen: a
// later backlog is reported again, and the pane is drawn.
func (w *Window) FrameApplied() {
	w.lagSent.Store(false)
	w.NoteRedraw()
}
