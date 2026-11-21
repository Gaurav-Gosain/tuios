package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

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
