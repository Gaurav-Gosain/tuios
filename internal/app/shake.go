package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	shakeMinLegColumns = 8

	shakeReversalsToFire = 4

	shakeMaxReversalGap = 250 * time.Millisecond

	shakeRestGap = 300 * time.Millisecond
)

type shakeState struct {
	extreme          int
	dir              int8
	count            int
	lastAt, lastTurn time.Time
	suspended        bool
	started          bool
}

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

	if m.shake.suspended {
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

	m.shake.reset()
	m.shake.suspended = true
	save := m.ToggleSpotlight()
	m.AnnounceSpotlight()
	return save
}
