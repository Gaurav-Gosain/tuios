package app

import "time"

// WheelGestureGap is how still the wheel has to be before the next event counts
// as a new gesture rather than more of the last one. A trackpad reports a
// continuous stream while the fingers are on the glass and then a momentum tail,
// and the gaps inside both are frames, not quarter seconds; two separate flicks
// are further apart than this in any hand.
const WheelGestureGap = 250 * time.Millisecond

// wheelAxis remembers which way the scroll gesture in progress is actually
// going, so the drift on the other axis can be dropped.
//
// A trackpad puts a little sideways drift into almost every vertical scroll, and
// the terminal forwards that as a left or right wheel button interleaved with
// the up and down ones. Anywhere both axes mean something, the drift fights the
// scroll: in the scrolling layout it walks the strip back against the direction
// the user is pushing it, which is the stutter this exists to stop.
//
// The rule is the majority axis of the gesture so far, because drift is by
// definition the minority: a gesture that is mostly up and down stays vertical
// however much the hand wanders. It is not "vertical wins", because a mouse
// wheel with shift held arrives as nothing but left and right on macOS, where
// the window server swaps the axes before the terminal ever sees the event. A
// gesture that genuinely changes axis mid-flight switches once the new axis has
// more events than the old, and a pause of WheelGestureGap starts it over.
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
		// The first event of a gesture is the only evidence there is, so it
		// decides. Requiring a majority here would swallow the first notch of
		// every wheel gesture, which is often the only notch there is.
		w.axis = this
	case w.axis == wheelAxisVertical && w.horizontal > w.vertical:
		w.axis = wheelAxisHorizontal
	case w.axis == wheelAxisHorizontal && w.vertical > w.horizontal:
		w.axis = wheelAxisVertical
	}
	return w.axis == this
}
