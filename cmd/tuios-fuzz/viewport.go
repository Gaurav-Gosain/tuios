package main

import (
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz/apptarget"
)

// The app viewport's plumbing, and the only place the display touches the
// target at all.
//
// The model is not safe to render from a second goroutine, so the display never
// reaches into it. Instead the display raises a flag saying it would like a
// frame, and the next action to execute takes one on the engine's own goroutine
// and publishes it. The display then draws whatever was last published.
//
// That costs one render per drawn frame rather than one per action, and it
// cannot race: every read of the model happens between two actions, on the
// goroutine that owns it. When the display is off none of this is installed.

// watched is a target that publishes the app's frame when the display asks.
type watched struct {
	*apptarget.Target
	live *current
}

func (w *watched) Apply(a fuzz.Action) error {
	if err := w.Target.Apply(a); err != nil {
		return err
	}
	w.live.capture(w.Target)
	return nil
}

func (w *watched) Close() {
	w.live.clear(w)
	w.Target.Close()
}

// current holds the frame the display is drawing and the flag asking for the
// next one. The engine writes it, the renderer reads it, and neither waits for
// the other.
type current struct {
	want atomic.Bool

	mu     sync.Mutex
	frame  string
	target *watched
}

func (c *current) set(w *watched) {
	c.mu.Lock()
	c.target = w
	c.mu.Unlock()
}

func (c *current) clear(w *watched) {
	c.mu.Lock()
	if c.target == w {
		c.target = nil
	}
	c.mu.Unlock()
}

// capture takes a frame only when one was asked for. A run with the display
// drawing every twelve actions renders the app twelve times less often than the
// oracle already does, so the viewport is not what a throughput gate would see.
func (c *current) capture(t *apptarget.Target) {
	if !c.want.Swap(false) {
		return
	}
	f := t.Screen()
	c.mu.Lock()
	c.frame = f
	c.mu.Unlock()
}

// screen is what the display calls. It returns the last published frame and
// asks for the next, so the viewport trails the instruments by one frame. That
// is a real property of pulling rather than pushing, and one frame at twelve
// actions is not a lag anybody can see.
func (c *current) screen() string {
	c.want.Store(true)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frame
}
