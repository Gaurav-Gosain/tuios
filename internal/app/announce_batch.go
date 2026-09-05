package app

import "time"

// A layout update is not one resize; it is several. Settle the border
// allowance, place the rectangle, reclaim the columns a divider was holding,
// drain a resize the pointer deferred - each step resizes the pane, and each
// resize used to reach the guest as its own SIGWINCH. A full-screen program
// repaints on every one, so a layout-mode switch that left a pane exactly the
// size it started at still cost it two full repaints, and a shell left two
// stacked prompts behind.
//
// Only the size a pane settles at was ever real. settleSizes holds the
// announcements for the duration of an update and sends each pane the one size
// it ended up with, or nothing at all when it ended up where it started.

// settleSizes runs a layout update with every pane's announcement held, then
// tells each pane its settled size once.
//
// Nesting is expected - a mode switch retiles, a retile places panes - so the
// hold is depth counted and only the outermost call releases.
func (m *OS) settleSizes(apply func()) {
	m.announceDepth++
	if m.announceDepth == 1 {
		for _, w := range m.Windows {
			if w != nil {
				w.HoldAnnouncements()
			}
		}
	}
	defer func() {
		m.announceDepth--
		if m.announceDepth > 0 {
			return
		}
		// Over m.Windows as it stands now, not as it was: a pane closed inside
		// the update is gone and a pane opened inside it was never held, and
		// releasing a pane that is not holding does nothing.
		for _, w := range m.Windows {
			if w != nil {
				w.ReleaseAnnouncements()
			}
		}
	}()
	apply()
}

// A pointer gesture is the other shape of the same problem, and settleSizes
// cannot hold it: a drag is not one call, it is a press, a run of motions and a
// release, each arriving as its own message. The sizes in between are not sizes
// the guest was ever meant to live at. A borderless pane gives its shared-border
// allowance up on the first motion so it can draw its own frame, and the retile
// on the drop gives it back; a divider drag moves an edge for every cell the
// pointer crosses. In a tiled layout the pane usually lands the size it started,
// so every one of those was avoidable, and each is a SIGWINCH a shell answers by
// repainting its prompt.
//
// So the hold is armed by the button going down and ended by it coming up. That
// is deliberately wider than "a pane is being dragged": every gesture that can
// move a pane starts with a press, and arming on the press rather than in each
// gesture's own setup means a gesture added later is covered without being told
// to be.
//
// The hold must never outlive the gesture. A pane that holds for ever is a pane
// that never learns its size again, which is worse than the bug. So the end is
// unconditional, and only the first of these is the normal way it happens:
//
//   - the input package's mouse-release path, once the drop has been laid out;
//   - endLostGesture, which is what runs when a release goes missing;
//   - releaseStaleAnnounceHold below, run every maintenance tick, which covers
//     a release that reached none of the above.

// announceHoldTimeout bounds a gesture hold against a pointer that has gone
// silent with a button still believed down. That happens when the release is
// lost outside the surface the events come from and no motion follows to
// correct it: the press said a button is down and nothing ever says otherwise.
//
// It is far longer than resizeDeferralTimeout, which bounds the same kind of
// staleness for the deferred resize, because the two pay opposite costs for
// expiring early. Draining a deferred resize early costs one retile taking the
// full path. Ending this hold early costs the guest the SIGWINCH this whole
// mechanism exists to withhold, and a user who pauses in the middle of a drag
// is doing nothing unusual.
const announceHoldTimeout = 5 * time.Second

// holdGestureAnnouncements opens the gesture's hold. Idempotent: a press
// arriving while one is open, which a second button going down is, adds
// nothing to unwind.
func (m *OS) holdGestureAnnouncements() {
	if m.announceGestureHeld {
		return
	}
	m.announceGestureHeld = true
	for _, w := range m.Windows {
		if w != nil {
			w.HoldAnnouncements()
		}
	}
}

// releaseGestureAnnouncements ends the gesture's hold and tells each pane the
// one size it ended up at, or nothing at all when it ended up where it started.
// Idempotent, because every path a gesture can end by calls it.
func (m *OS) releaseGestureAnnouncements() {
	if !m.announceGestureHeld {
		return
	}
	m.announceGestureHeld = false
	// Over m.Windows as it stands now, for the reason settleSizes gives: a pane
	// opened during the gesture was never held, and releasing it does nothing.
	for _, w := range m.Windows {
		if w != nil {
			w.ReleaseAnnouncements()
		}
	}
}

// ReleaseGestureAnnouncements is releaseGestureAnnouncements for the input
// package, whose mouse release handler ends the gesture after the drop has been
// laid out.
func (m *OS) ReleaseGestureAnnouncements() { m.releaseGestureAnnouncements() }

// AnnounceGestureHeld reports whether the gesture's hold is open, which is a
// state where a pane's announced size is deliberately ahead of the size its
// guest has. It is exported for the same reason PendingViewportResize is: the
// out-of-process fuzz target (internal/fuzz/apptarget) asserts that the two
// agree, and has to know that a disagreement here is the design rather than the
// fault it is looking for.
func (m *OS) AnnounceGestureHeld() bool { return m.announceGestureHeld }

// staleAnnounceHold reports whether a hold is open that no gesture is holding
// any more.
//
// The button coming up makes it stale at once. A button still believed down
// makes it stale after announceHoldTimeout of pointer silence, which is the
// only remaining way the flag could stick: the release is lost outside the
// surface the events come from, so nothing corrects the press's "a button is
// down" and no motion follows to say otherwise.
//
// The idle tick asks this before it decides to sleep, because a hold the diet
// slept through is precisely the stranded hold that must not survive.
func (m *OS) staleAnnounceHold() bool {
	if !m.announceGestureHeld {
		return false
	}
	return !m.pointerDown || m.lastPointerAt.IsZero() ||
		time.Since(m.lastPointerAt) > announceHoldTimeout
}

// releaseStaleAnnounceHold is the per-tick backstop: a hold cannot outlive the
// button that armed it.
func (m *OS) releaseStaleAnnounceHold() {
	if m.staleAnnounceHold() {
		m.releaseGestureAnnouncements()
	}
}
