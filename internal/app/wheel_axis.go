package app

import "time"

// WheelGestureGap is how still the wheel has to be before the next event counts
// as a new gesture rather than more of the last one. A trackpad reports a
// continuous stream while the fingers are on the glass and then a momentum tail,
// and the gaps inside both are frames, not quarter seconds; two separate flicks
// are further apart than this in any hand.
const WheelGestureGap = 250 * time.Millisecond

type wheelAxis struct {
	last       time.Time
	vertical   int
	horizontal int
	axis       wheelAxisKind
}

type wheelAxisKind uint8

const (
	wheelAxisNone wheelAxisKind = iota
	wheelAxisVertical
	wheelAxisHorizontal
)

// WheelAxisAccepts records a wheel event on one axis and reports whether that is
// the axis the gesture is on. An event on the other axis is drift and the caller
// should drop it.
//
// now is a parameter rather than a call to time.Now inside, so a test can put
// two events a known distance apart without sleeping.
func (m *OS) WheelAxisAccepts(horizontal bool, now time.Time) bool {
	w := &m.wheelAxis
	if !w.last.IsZero() && now.Sub(w.last) > WheelGestureGap {
		w.vertical, w.horizontal, w.axis = 0, 0, wheelAxisNone
	}
	w.last = now

	this := wheelAxisVertical
	if horizontal {
		w.horizontal++
		this = wheelAxisHorizontal
	} else {
		w.vertical++
	}

	switch {
	case w.axis == wheelAxisNone:
		w.axis = this
	case w.axis == wheelAxisVertical && w.horizontal > w.vertical:
		w.axis = wheelAxisHorizontal
	case w.axis == wheelAxisHorizontal && w.vertical > w.horizontal:
		w.axis = wheelAxisVertical
	}
	return w.axis == this
}
