package app

import "time"

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
		for _, w := range m.Windows {
			if w != nil {
				w.ReleaseAnnouncements()
			}
		}
	}()
	apply()
}

const announceHoldTimeout = 5 * time.Second

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

func (m *OS) releaseGestureAnnouncements() {
	if !m.announceGestureHeld {
		return
	}
	m.announceGestureHeld = false
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

func (m *OS) staleAnnounceHold() bool {
	if !m.announceGestureHeld {
		return false
	}
	return !m.pointerDown || m.lastPointerAt.IsZero() ||
		time.Since(m.lastPointerAt) > announceHoldTimeout
}

func (m *OS) releaseStaleAnnounceHold() {
	if m.staleAnnounceHold() {
		m.releaseGestureAnnouncements()
	}
}
