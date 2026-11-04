package main

import (
	"math"
	"runtime"
	"time"

	"github.com/egoist/mygo/ui"
)

// smoothScroll scrolls a scroll container (ui.Scroll or ui.List) on the
// frame clock, where MyGo moves it as each event comes:
//
//   - A touchpad's small moves are resampled at the frame's time, a little
//     behind the fingers (resampleLag), so that each frame moves as far as
//     the fingers did meanwhile. Events and frames that come at nearly the
//     same rate otherwise give frames that move twice and frames that do
//     not move at all, which reads as judder.
//   - A wheel's notch glides over notchGlide, easing out, as browsers do.
//   - On Linux, a flick goes on after the fingers lift, slowing down, as
//     GTK's own lists do. GTK sends no momentum of its own, only an event
//     with no distance as the fingers lift. macOS sends its momentum as
//     more moves, which are resampled as any others.
//
// It asks for frames only while the content moves.
type smoothScroll struct {
	ui.ScrollState

	// target is where the input puts the content and shown where the last
	// frame put it, both in DIPs from where the tracking began. The frame
	// moves the content by what shown changes.
	target, shown float64
	// samples are the last touchpad moves: when, and target after each.
	samples [8]scrollSample
	n       int
	// glide is a wheel notch's glide: from where to where, since when.
	glideFrom, glideTo float64
	glideAt            time.Time
	gliding            bool
	// fling is the momentum after a flick: its speed in DIPs a second at
	// flingAt, from flingFrom.
	flingV, flingFrom float64
	flingAt           time.Time
	flinging          bool
	// lastY is the offset the container wrote back in the last frame, to
	// tell that it stopped at an end.
	lastY float32
}

type scrollSample struct {
	at time.Time
	to float64
}

const (
	// resampleLag is how far behind the fingers the content follows: about
	// one period of a touchpad's reports (125 Hz on most).
	resampleLag = 9 * time.Millisecond
	// notchGlide is how long a wheel's notch glides.
	notchGlide = 150 * time.Millisecond
	// flingDecay is how fast a flick slows, per second: its speed falls
	// to 1/e in 1/flingDecay seconds, and it goes v/flingDecay in all.
	flingDecay = 4.0
	// flingMin is the least speed, in DIPs a second, that a flick goes on
	// at, and flingStop the speed it stops at.
	flingMin, flingStop = 250.0, 12.0
)

// track scrolls e, a scroll container, with s, and returns it. Call it
// before anything reads s in the frame, as the container takes the
// offset s sets when TrackScroll is called.
func (s *smoothScroll) track(c *ui.Context, e ui.Element) ui.Element {
	s.step(c)
	return e.HandleInput(func(ev ui.InputEvent) bool { return s.input(c, ev) }).TrackScroll(&s.ScrollState)
}

// input takes the vertical scrolling over the container.
func (s *smoothScroll) input(c *ui.Context, ev ui.InputEvent) bool {
	if ev.Kind != ui.InputScroll || ev.Mods&(ui.Shift|ui.Ctrl|ui.Cmd) != 0 {
		return false
	}
	now := ev.Time
	if ev.DX == 0 && ev.DY == 0 {
		// The fingers lifted (GTK's stop event): a flick goes on.
		if !ev.Precise && runtime.GOOS == "linux" {
			s.flick(now)
		}
		return true
	}
	if math.Abs(float64(ev.DX)) > math.Abs(float64(ev.DY)) {
		return false // sideways: an inner container's, or none's
	}
	s.flinging = false
	dy := float64(ev.DY)
	s.target += dy
	if ev.Precise {
		if s.gliding {
			// A notch on its way lands where the fingers take over.
			s.gliding = false
		}
		if s.n == len(s.samples) {
			copy(s.samples[:], s.samples[1:])
			s.n--
		}
		s.samples[s.n] = scrollSample{now, s.target}
		s.n++
		return true
	}
	s.n = 0
	if c.Preferences().ReduceMotion {
		s.shown = s.target
		s.Y += float32(dy)
		return true
	}
	s.glideFrom, s.glideTo, s.glideAt, s.gliding = s.at(now), s.target, now, true
	return true
}

// flick starts the momentum of a touchpad gesture that just ended at now,
// from the speed of its last moves.
func (s *smoothScroll) flick(now time.Time) {
	if s.n < 2 {
		return
	}
	last := s.samples[s.n-1]
	if now.Sub(last.at) > 60*time.Millisecond {
		return // the fingers rested before they lifted
	}
	first := last
	for i := s.n - 2; i >= 0 && last.at.Sub(s.samples[i].at) <= 100*time.Millisecond; i-- {
		first = s.samples[i]
	}
	dt := last.at.Sub(first.at).Seconds()
	if dt < 0.02 {
		return
	}
	v := (last.to - first.to) / dt
	if math.Abs(v) < flingMin {
		return
	}
	// The content goes on from where it shows, which is at most
	// resampleLag behind the fingers.
	s.n = 0
	s.target = s.shown
	s.flingV, s.flingFrom, s.flingAt, s.flinging = v, s.shown, now, true
}

// at returns where the content goes at time now: resampled from the
// touchpad's moves, along a notch's glide, or along a fling.
func (s *smoothScroll) at(now time.Time) float64 {
	switch {
	case s.flinging:
		t := now.Sub(s.flingAt).Seconds()
		decay := math.Exp(-flingDecay * t)
		if math.Abs(s.flingV*decay) < flingStop {
			s.flinging = false
			s.target = s.flingFrom + s.flingV/flingDecay*(1-decay)
			return s.target
		}
		s.target = s.flingFrom + s.flingV/flingDecay*(1-decay)
		return s.target
	case s.gliding:
		k := float64(now.Sub(s.glideAt)) / float64(notchGlide)
		if k >= 1 {
			s.gliding = false
			return s.glideTo
		}
		// A notch that came on the way glides on to the new target.
		s.glideTo = s.target
		return s.glideFrom + (s.glideTo-s.glideFrom)*(1-math.Pow(1-k, 3))
	case s.n > 0:
		when := now.Add(-resampleLag)
		last := s.samples[s.n-1]
		if !when.Before(last.at) {
			s.n = 0
			return s.target
		}
		for i := s.n - 1; i > 0; i-- {
			a, b := s.samples[i-1], s.samples[i]
			if !when.Before(a.at) {
				k := float64(when.Sub(a.at)) / float64(b.at.Sub(a.at))
				return a.to + (b.to-a.to)*k
			}
		}
		// Before the first move kept: the content waits for it.
		return s.shown
	}
	return s.target
}

// step moves the content to where it goes in this frame.
func (s *smoothScroll) step(c *ui.Context) {
	// The container stopped the content at an end: what is left of the
	// move goes. (A List also moves the offset as it measures its rows,
	// which steps relative to it ride on.)
	if s.Y != s.lastY && (s.Y <= 0 || s.Y >= s.MaxY) {
		s.flinging, s.gliding, s.n = false, false, 0
		s.target = s.shown
	}
	if s.shown == s.target && !s.gliding && !s.flinging && s.n == 0 {
		s.lastY = s.Y
		return
	}
	to := s.at(c.Now())
	s.Y = max(s.Y+float32(to-s.shown), 0)
	s.shown = to
	s.lastY = s.Y
	if s.shown != s.target || s.gliding || s.flinging || s.n > 0 {
		c.AnimationFrame()
	}
}
