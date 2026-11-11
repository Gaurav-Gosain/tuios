package app

import "time"

// motionEvery is the shortest interval among the effects the motion clock is
// running for now, zero when nothing moves: how often the clock wakes.
func motionEvery(m *OS) time.Duration {
	var every time.Duration
	for i := range effects {
		e := &effects[i]
		if e.every == nil || !e.live(m) {
			continue
		}
		if d := e.every(m); every == 0 || d < every {
			every = d
		}
	}
	return every
}

// effectIndex is the place of the named effect in effects.
func effectIndex(name string) int {
	for i := range effects {
		if effects[i].name == name {
			return i
		}
	}
	panic("no effect named " + name)
}

// dueNow makes every running effect's frame due now, as if its timer had
// fired: a test that calls handleMotionFrame twice in a row is otherwise
// inside the first frame's interval.
func dueNow(m *OS) {
	for i := range m.motion.due {
		if !m.motion.due[i].IsZero() {
			m.motion.due[i] = time.Now()
		}
	}
}
