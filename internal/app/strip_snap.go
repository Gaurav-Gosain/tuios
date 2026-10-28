package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// The strip's snap: appearance.niri_scroll_snap.
//
// A wheel and a trackpad move the strip in steps of niri_scroll_cells, and a
// finger on tuios-web one cell at a time, so wherever they stop the strip can
// be left with a column cut at the edge. niri lines the strip up on a column
// when the gesture ends.
//
// A finger's end is known: tuios-web's touch layer reports the lift, and the
// strip snaps then to the nearest column (SnapStripNow), however long the
// finger rested on the way. A wheel and a trackpad report no end, only the
// steps, so their end is a pause: stripSnapQuiet after the last step that
// moved the strip, it snaps to the next column in the way the steps went
// (ScrollingLayout.SnapViewport), sliding there as a keyboard move does. A
// step in the pause starts the wait again. Snapping to the nearest column
// instead would send one notch, shorter than half a column, back to where it
// started, and the wheel could never move the strip.
//
// A snap changes the strip's offset like any other scroll, so it is sent to
// the daemon, or the other clients would not see it and the next state the
// daemon sends would undo it.

// stripSnapQuiet is how long the strip has to stay still before it snaps.
// Long enough for the gaps in a trackpad's momentum tail, short enough to
// read as the end of the gesture.
const stripSnapQuiet = 180 * time.Millisecond

// StripSnapMsg is the snap's timer. Gen is the step it was armed by, and
// Workspace the workspace whose strip the step moved.
type StripSnapMsg struct {
	Gen       uint64
	Workspace int
}

// ScheduleStripSnap arms the snap after a wheel step moved the strip in
// direction dir, or returns nil with the option off.
func (m *OS) ScheduleStripSnap(dir int) tea.Cmd {
	if !m.Settings.NiriScrollSnap {
		return nil
	}
	m.stripSnapGen++
	m.stripSnapDir = dir
	msg := StripSnapMsg{Gen: m.stripSnapGen, Workspace: m.CurrentWorkspace}
	return tea.Tick(stripSnapQuiet, func(time.Time) tea.Msg { return msg })
}

// SnapStrip lines the strip up on a column, if no step has moved it since
// the timer was armed and the workspace the step moved is still the one on
// screen. It reports whether the strip moved.
func (m *OS) SnapStrip(msg StripSnapMsg) bool {
	if msg.Gen != m.stripSnapGen || msg.Workspace != m.CurrentWorkspace {
		return false
	}
	return m.snapStrip(m.stripSnapDir)
}

// CancelStripSnap drops a pause snap a wheel armed, for a finger's step: a
// finger's drag snaps when it lifts, and not when it rests on the way.
func (m *OS) CancelStripSnap() { m.stripSnapGen++ }

// SnapStripNow lines the strip up on the nearest column at once, for a
// finger that lifted, with the option on.
func (m *OS) SnapStripNow() {
	if !m.Settings.NiriScrollSnap {
		return
	}
	m.CancelStripSnap()
	m.snapStrip(0)
}

// snapStrip lines the strip up on a column, if the workspace is on the
// strip, and reports whether it moved. dir is as for SnapViewport.
func (m *OS) snapStrip(dir int) bool {
	if !m.AutoTiling || !m.UseScrollingLayout {
		return false
	}
	sl := m.GetOrCreateScrollingLayout()
	before := sl.ViewportX
	sl.SnapViewport(m.ScrollingViewWidth(), dir)
	if sl.ViewportX == before {
		return false
	}
	m.ScrollingSetPositions()
	return true
}
