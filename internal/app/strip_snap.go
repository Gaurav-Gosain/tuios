package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// The strip's snap: appearance.niri_scroll_snap.
//
// A wheel, a trackpad and a finger on tuios-web all move the strip in steps of
// niri_scroll_cells, so wherever they stop the strip can be left with a column
// cut at the edge. niri lines the strip up on a column when the gesture ends.
// A terminal reports no end to a gesture, only the wheel steps, so the end is
// a pause: stripSnapQuiet after the last step that moved the strip, it snaps
// to the nearest column (ScrollingLayout.SnapViewport), sliding there as a
// keyboard move does. A step in the pause starts the wait again.

// stripSnapQuiet is how long the strip has to stay still before it snaps.
// Long enough for the gaps in a trackpad's momentum tail, short enough to
// read as the end of the gesture.
const stripSnapQuiet = 180 * time.Millisecond

// StripSnapMsg is the snap's timer. Gen is the step it was armed by.
type StripSnapMsg struct{ Gen uint64 }

// ScheduleStripSnap arms the snap after a wheel step moved the strip, or
// returns nil with the option off.
func (m *OS) ScheduleStripSnap() tea.Cmd {
	if !m.Settings.NiriScrollSnap {
		return nil
	}
	m.stripSnapGen++
	gen := m.stripSnapGen
	return tea.Tick(stripSnapQuiet, func(time.Time) tea.Msg { return StripSnapMsg{Gen: gen} })
}

// SnapStrip lines the strip up on the nearest column, if no step has moved
// it since the timer was armed and the workspace is still on the strip.
func (m *OS) SnapStrip(msg StripSnapMsg) {
	if msg.Gen != m.stripSnapGen || !m.AutoTiling || !m.UseScrollingLayout {
		return
	}
	sl := m.GetOrCreateScrollingLayout()
	before := sl.ViewportX
	sl.SnapViewport(m.ScrollingViewWidth())
	if sl.ViewportX != before {
		m.ScrollingSetPositions()
	}
}
