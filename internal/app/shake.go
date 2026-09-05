package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// A shake of the pointer turns the spotlight on and off, the way shaking a
// mouse on macOS finds the cursor. It is off until a person asks for it, and
// it goes through ToggleSpotlight, so the key, the palette, the settings row
// and the gesture all move the same state and all persist it.
//
// The whole feature is a few comparisons on an event that has already arrived,
// so it costs nothing to have. What it can cost is a screen that goes dark for
// a reason nobody typed, and that is what every number below is chosen for.
//
// Where it runs. In Update's mouse-motion case, not in the motion handler.
// Motion is routed away from handleMouseMotion whenever capture mode or the
// scrollback browser is up, and handleMouseMotion itself returns early for a
// context menu, a rail drag, a workspace pill drag and an overlay drag. Update
// sees every motion event exactly once, before any of that.
//
// What it adds to an idle client. Nothing. There is no timer, no tick and no
// term in tickNeedsWork: arriving motion is the whole clock, the state is a
// fixed-size struct that allocates nothing, and a client whose pointer is at
// rest never reaches any of it. BenchmarkIdleTick is unmoved.
//
// What it will not do while a button is down. See noteShakeMotion.

const (
	// shakeMinLegColumns is how far the pointer must travel against its own
	// direction before that counts as turning round.
	//
	// It is the whole defence against jitter. A hand resting on a mouse moves
	// the pointer one or two columns, and pointer acceleration on a high-DPI
	// device turns a small physical tremor into a larger one, so a detector
	// that counted every sign change on dx would fire on somebody breathing.
	// Eight columns is far past any of that and is a small fraction of a
	// deliberate stroke, which crosses a good part of the screen.
	shakeMinLegColumns = 8

	// shakeReversalsToFire is how many times the pointer must turn round.
	//
	// Four is two complete there-and-back cycles. macOS asks for about three to
	// find a cursor; this asks for one more, because showing a cursor is a
	// thing a person can ignore and darkening most of the screen is not. It is
	// also what rules out the ordinary sweep: crossing the screen and coming
	// back is one reversal, and correcting an overshoot is one more.
	shakeReversalsToFire = 4

	// shakeMaxReversalGap is how long one turn may take after the last one.
	//
	// This is the part that means "fast", and it is what a total time window
	// alone would miss. A person shakes at three to five turns a second, so a
	// real gesture has no pause in it at all. Two ordinary moves that each
	// overshoot and correct give four reversals as well, but they have a dwell
	// between them where the hand picks the next target. A gap bound rejects
	// those and a window of the same total length does not.
	//
	// Four reversals with no gap over 250 ms puts the whole gesture inside
	// 750 ms.
	shakeMaxReversalGap = 250 * time.Millisecond

	// shakeRestGap is how long the pointer must rest before a shake that has
	// already fired may fire again.
	//
	// A hand does not stop on the reversal that crosses the threshold. Without
	// this the rest of the same shake counts four more turns and puts the beam
	// back where it started, which reads as the gesture not working. Resting is
	// the end of the gesture, and it is free to detect: no motion event arrives
	// while the pointer is still, so the next one to arrive carries the pause
	// in its own timestamp.
	shakeRestGap = 300 * time.Millisecond
)

// shakeState is what the detector remembers between motion events. Every field
// is a scalar and there are no timers, so an OS that never sees a shake carries
// nine words and no work.
type shakeState struct {
	// extreme is the furthest column reached in the current direction. It is
	// the point a reversal is measured from, which is what makes the threshold
	// hysteresis rather than a per-event comparison: noise inside
	// shakeMinLegColumns moves nothing at all.
	extreme int
	// dir is which way the pointer is going: -1 left, +1 right, 0 not yet
	// known.
	dir int8
	// count is how many turns have been seen with no gap longer than
	// shakeMaxReversalGap between them.
	count int
	// lastAt is when the last motion event arrived, and lastTurn when the last
	// counted turn did.
	lastAt, lastTurn time.Time
	// suspended holds the detector quiet after it has fired, until the pointer
	// rests. See shakeRestGap.
	suspended bool
	// started is whether extreme holds a real column yet.
	started bool
}

// reset forgets the gesture in progress. The suspension survives it, because
// the reasons a gesture is abandoned - a button went down, an overlay took the
// pointer - are not the pointer coming to rest.
func (s *shakeState) reset() {
	s.dir, s.count, s.started = 0, 0, false
	s.lastTurn = time.Time{}
}

// ShakeGestureOn reports whether this client is watching for the gesture.
//
// The local program filters motion events to keep an idle machine idle, and
// that filter is a whitelist: a gesture read off bare motion is dead until it
// has a clause of its own there. This is that clause's test. The SSH and
// browser clients run no filter, so they need none.
func (m *OS) ShakeGestureOn() bool { return m.spotlightConfig().ShakeToggles() }

// noteShakeMotion feeds one motion event to the detector and returns the save
// command when the gesture fired. Update calls it, and nothing else does.
//
// Four things stop it before it looks at the pointer at all.
//
// A button is held. This is the one suppression that matters, and it is what
// makes the gesture safe to have on. Every way a person moves a pointer fast
// and far while working is a drag: sizing a pane by its divider, moving a
// window by its title bar, selecting text in a pane, dragging a rail row, a
// workspace pill, a slider. All of them are somebody working, and a beam that
// went on and off in the middle of one would be worse than no gesture. Held is
// read off the motion event rather than a flag, for the reason update.go reads
// pointerDown the same way: motion names the buttons still down, so it is the
// most current answer and it corrects a press or a release whose own event
// never arrived. The gesture in progress is forgotten on the way in and on the
// way out, so a drag can neither finish a shake that started before it nor
// start one that finishes after.
//
// The screen saver is up. The motion that dismisses the saver is input like any
// other, and the beam must not be what it changes.
//
// Capture mode is up. A screenshot region is drawn with the pointer, and it has
// its own motion handler.
//
// The setting is off, which is how it ships.
func (m *OS) noteShakeMotion(msg tea.MouseMotionMsg, now time.Time) tea.Cmd {
	if !m.ShakeGestureOn() {
		return nil
	}
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseNone || m.screensaver.active || m.CaptureActive() {
		m.shake.reset()
		return nil
	}

	x := mouse.X
	gap := now.Sub(m.shake.lastAt)
	m.shake.lastAt = now

	// A pause long enough to be the end of a gesture re-arms the detector after
	// a fire. It is measured on arriving motion, so a pointer that is put down
	// and left re-arms on the next move rather than on a timer.
	if m.shake.suspended {
		// The gap is read before anything else about the state, and that is the
		// point: the firing turn left the gesture reset, so a test of whether
		// the pointer has started moving would take the seed branch first and
		// the rest would not be noticed until the one after it.
		if gap < shakeRestGap {
			m.shake.extreme, m.shake.started = x, true
			return nil
		}
		m.shake.suspended = false
		m.shake.reset()
	}

	if !m.shake.started {
		m.shake.extreme, m.shake.started = x, true
		return nil
	}

	// Going further the way we were going only moves the mark. This is the
	// hysteresis: nothing is counted until the pointer has come back
	// shakeMinLegColumns against it.
	switch {
	case x > m.shake.extreme && m.shake.dir >= 0:
		m.shake.extreme, m.shake.dir = x, 1
		return nil
	case x < m.shake.extreme && m.shake.dir <= 0:
		m.shake.extreme, m.shake.dir = x, -1
		return nil
	}

	back := max(m.shake.extreme-x, x-m.shake.extreme)
	if back < shakeMinLegColumns {
		return nil
	}

	// The pointer has turned round. A turn that comes too long after the last
	// one starts the count again rather than joining it.
	if m.shake.count > 0 && now.Sub(m.shake.lastTurn) > shakeMaxReversalGap {
		m.shake.count = 0
	}
	m.shake.count++
	m.shake.lastTurn = now
	if x > m.shake.extreme {
		m.shake.dir = 1
	} else {
		m.shake.dir = -1
	}
	m.shake.extreme = x

	if m.shake.count < shakeReversalsToFire {
		return nil
	}

	// Fired. The beam moves through the same call the key and the palette use,
	// so the state and the saved config stay in step whichever way it was
	// asked for.
	m.shake.reset()
	m.shake.suspended = true
	save := m.ToggleSpotlight()
	// The same line the key shows, in the same words, because it is the same
	// change. A gesture needs it more than a key does: it can happen without
	// being meant, and a screen that darkens with no word for it reads as a
	// fault rather than as a thing the user just did.
	state := "OFF"
	if m.SpotlightOn() {
		state = "ON"
	}
	m.ShowNotification("Spotlight: "+state, "info", m.Settings.NotificationDuration)
	return save
}
