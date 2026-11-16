package app

import (
	"testing"
	"time"
)

// TestASlowFrameDoesNotTurnTheNextSwitchIntoACut: a client that draws its
// frames in time keeps its slides when one or two frames are late. A frame
// is timed on the wall clock, so a process that the system deschedules for
// a moment has a slow frame without being a slow client. Three slow frames
// in the last five are a slow client, and the next switch is a cut.
//
// NEGATIVE CONTROL: the smoothed cost this replaced (cost += (d - cost) / 4)
// fails the first two cases: one frame of 155 ms after quick ones gives a
// cost of 43 ms, over the 16.7 ms a slide frame lasts.
func TestASlowFrameDoesNotTurnTheNextSwitchIntoACut(t *testing.T) {
	const frame = time.Second / 60
	quick := 3 * time.Millisecond
	cases := []struct {
		name   string
		frames []time.Duration
		slow   bool
	}{
		{"one frame descheduled", []time.Duration{quick, quick, quick, quick, quick, 155 * time.Millisecond}, false},
		{"two slow frames", []time.Duration{quick, quick, quick, quick, 45 * time.Millisecond, 32 * time.Millisecond}, false},
		{"three slow frames in five", []time.Duration{quick, quick, 40 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}, true},
		{"every frame slow", []time.Duration{30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond}, true},
		{"slow, then quick again", []time.Duration{40 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond, quick, quick, quick}, false},
		{"no frame yet", nil, false},
	}
	for _, c := range cases {
		var s workspaceSlide
		for _, d := range c.frames {
			s.noteFrameCost(d)
		}
		if got := s.tooSlow(frame); got != c.slow {
			t.Errorf("ASSERTION: %s: tooSlow = %v, want %v", c.name, got, c.slow)
		}
	}
}
