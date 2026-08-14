// Package vis draws a fuzzing run while it happens.
//
// It attaches to the engine through fuzz.Observer, which hands out what
// happened and takes nothing back, so nothing here can change what the fuzzer
// explores. The determinism gate in internal/fuzz proves that for the seam and
// the gate in this package proves it for the whole display: one seed, drawn and
// undrawn, has to produce the same trace.
//
// The design position is that the truth is the spectacle. Every mark maps to
// something the engine did: a tape cell is one action, a dot is one registered
// invariant, a funnel bar is one shrink candidate at its real length. There is
// no progress bar, because a fuzzer has no denominator; no coverage figure,
// because nothing measures coverage; and no idle animation, because motion on
// this screen means work happened.
package vis

import (
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Cadence defaults. Thirty frames a second is what a terminal recording wants;
// over thousands of actions a second nothing on the tape is legible at that
// rate, which is what per-batch is for: it draws every N actions, so the tape
// advances a readable amount per frame and the recording is watchable.
const (
	DefaultFPS   = 30
	DefaultBatch = 12

	// railWidth is fixed. The instruments are the whole truth of the run, so
	// they keep their columns and the app viewport flexes around them.
	railWidth = 39

	// MinWidth and MinHeight are the smallest frame worth drawing. Below this
	// the rail alone does not fit and visual mode declines rather than showing
	// a mangled instrument.
	MinWidth  = 60
	MinHeight = 20
)

// Options configures a display. Every field has a working default, so the
// zero value plus a rule registry is a usable display.
type Options struct {
	// Rules is the target's invariant registry, in the order it checks them.
	// The display never invents a name: with no registry it draws no matrix.
	Rules []fuzz.RuleInfo
	// Classes groups the action alphabet for the tape and the mix. Nil takes
	// DefaultClasses.
	Classes []Class

	Out           io.Writer
	Width, Height int

	// FPS draws on a wall-clock cadence. Batch draws every N actions instead,
	// and wins when both are set.
	FPS   int
	Batch int

	ASCII bool
	Mono  bool

	// Screen supplies the app under test's current frame for the viewport. It
	// is pulled on the renderer's cadence rather than pushed, so a display that
	// is not drawing costs the target nothing. Nil drops the viewport and the
	// instruments take the whole frame, which is still the whole truth.
	Screen func() string

	// Command is the invocation the end card prints so a reader can reproduce
	// the run. It is shown verbatim, so it has to be the real one.
	Command string
}

func (o Options) fps() int {
	if o.FPS <= 0 {
		return DefaultFPS
	}
	return o.FPS
}

// Display is the observer and the renderer. It implements fuzz.Observer.
type Display struct {
	*state
	opts Options

	wake    chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	closed  atomic.Bool
}

// New builds a display. It does not draw until Start.
func New(o Options) *Display {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Classes == nil {
		o.Classes = DefaultClasses()
	}
	if o.Width <= 0 {
		o.Width = 120
	}
	if o.Height <= 0 {
		o.Height = 34
	}
	d := &Display{
		state:   newState(o.Rules, o.Classes),
		opts:    o,
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	d.state.wake = d.wake
	return d
}

// Fits reports whether a terminal this size can carry the instruments. Below it
// the caller should say so in one line and run without a display rather than
// draw something misleading.
func Fits(w, h int) bool { return w >= MinWidth && h >= MinHeight }

// Open begins drawing. Frames go out on whichever cadence Options set.
//
// It is not called Start because Start is the observer's own lifecycle event,
// and a display whose renderer shadowed it would quietly stop being an
// Observer: the interface would still be satisfied by the embedded state, but
// the engine's Start would go to the wrong method.
func (d *Display) Open() {
	overlay.SetASCII(d.opts.ASCII)
	d.enter()
	go d.loop()
}

// Close stops the renderer, draws one last frame so the end card is what the
// recording ends on, and restores the terminal.
func (d *Display) Close() {
	if d.closed.Swap(true) {
		return
	}
	close(d.stop)
	<-d.stopped
	d.draw()
	d.leave()
}

func (d *Display) loop() {
	defer close(d.stopped)
	if d.opts.Batch > 0 {
		d.loopBatch()
		return
	}
	d.loopTimed()
}

// loopTimed coalesces to a frame rate. It draws only when the generation moved,
// so an engine that has stopped costs no frames.
func (d *Display) loopTimed() {
	tick := time.NewTicker(time.Second / time.Duration(d.opts.fps()))
	defer tick.Stop()
	var last uint64
	for {
		select {
		case <-d.stop:
			return
		case <-tick.C:
			gen, moved := d.takeFrame(last)
			if !moved {
				continue
			}
			last = gen
			d.draw()
		}
	}
}

// loopBatch draws every N actions and on every phase change. It is what makes a
// recording legible: at thirty frames a second over thousands of actions a
// second the tape is a blur, and a blur of real work still reads as a fake
// animation.
func (d *Display) loopBatch() {
	phase := PhaseGenerating
	for {
		select {
		case <-d.stop:
			return
		case <-d.wake:
			next, ready := d.batchReady(d.opts.Batch, phase)
			phase = next
			if ready {
				d.draw()
			}
		}
	}
}

// Frame renders the current state. It is what the tests and the frame captures
// call, and it is the same function the renderer uses, so a capture is the
// frame a viewer sees rather than a reconstruction of one.
func (d *Display) Frame() string {
	return Render(d.snapshot(), d.opts)
}

func (d *Display) draw() {
	frame := d.Frame()
	var b strings.Builder
	// Synchronized output: the terminal holds the frame until it is complete,
	// so a recording never catches a half-drawn instrument.
	b.WriteString("\x1b[?2026h\x1b[H")
	for i, line := range strings.Split(frame, "\n") {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(line)
		b.WriteString("\x1b[K")
	}
	b.WriteString("\x1b[J\x1b[?2026l")
	_, _ = io.WriteString(d.opts.Out, b.String())
}

func (d *Display) enter() {
	_, _ = io.WriteString(d.opts.Out, "\x1b[?1049h\x1b[?25l\x1b[2J")
}

func (d *Display) leave() {
	_, _ = io.WriteString(d.opts.Out, "\x1b[?25h\x1b[?1049l")
}
