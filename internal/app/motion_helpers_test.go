package app

import "time"

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

func effectIndex(name string) int {
	for i := range effects {
		if effects[i].name == name {
			return i
		}
	}
	panic("no effect named " + name)
}

func dueNow(m *OS) {
	for i := range m.motion.due {
		if !m.motion.due[i].IsZero() {
			m.motion.due[i] = time.Now()
		}
	}
}
