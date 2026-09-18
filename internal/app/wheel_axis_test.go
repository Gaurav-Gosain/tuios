package app

import (
	"testing"
	"time"
)

// The majority rule, its three interesting shapes: drift on a vertical gesture,
// drift on a horizontal one, and a gesture that genuinely changes axis. The
// input package's TestSidewaysDriftDoesNotFightTheStrip is the end-to-end half
// of this; here the events carry a clock, so the gesture window can be crossed
// without sleeping.
func TestWheelAxisFollowsTheMajorityAxis(t *testing.T) {
	const (
		vert  = false
		horiz = true
	)
	for _, tc := range []struct {
		name   string
		events []bool
		want   []bool
	}{
		{
			name:   "drift through a vertical gesture is dropped",
			events: []bool{vert, vert, horiz, vert, horiz, vert},
			want:   []bool{true, true, false, true, false, true},
		},
		{
			// A mouse wheel with shift held on macOS: the window server swaps
			// the axes, so the whole gesture arrives horizontal.
			name:   "drift through a horizontal gesture is dropped",
			events: []bool{horiz, horiz, vert, horiz, vert, horiz},
			want:   []bool{true, true, false, true, false, true},
		},
		{
			// Not drift: the hand turned. The new axis takes over as soon as it
			// has more events than the old one, rather than being locked out for
			// the rest of the gesture.
			name:   "a gesture that turns follows the hand",
			events: []bool{vert, horiz, horiz, horiz},
			want:   []bool{true, false, true, true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &OS{}
			now := time.Now()
			for i, horizontal := range tc.events {
				now = now.Add(10 * time.Millisecond)
				if got := m.WheelAxisAccepts(horizontal, now); got != tc.want[i] {
					t.Fatalf("event %d (horizontal=%v) accepted=%v, want %v", i, horizontal, got, tc.want[i])
				}
			}
		})
	}
}

// A gesture that has been over for a while is not the gesture the next event
// belongs to. Without the expiry, one long vertical scroll would leave the
// horizontal axis locked out until the client was restarted.
func TestWheelAxisStartsOverAfterAPause(t *testing.T) {
	m := &OS{}
	now := time.Now()
	for range 5 {
		now = now.Add(10 * time.Millisecond)
		if !m.WheelAxisAccepts(false, now) {
			t.Fatal("a vertical gesture rejected one of its own events")
		}
	}
	now = now.Add(10 * time.Millisecond)
	if m.WheelAxisAccepts(true, now) {
		t.Fatal("drift inside the gesture was accepted")
	}
	// Measured from the drift event, because a dropped event is still the
	// gesture happening: it keeps the window open like any other.
	if !m.WheelAxisAccepts(true, now.Add(WheelGestureGap+time.Millisecond)) {
		t.Fatal("a horizontal gesture after the pause was still treated as drift")
	}
}
