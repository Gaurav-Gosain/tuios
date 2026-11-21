package app

import (
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

var emulatorReleaseEvery = sync.OnceValue(session.CleanupEvery)

func (m *OS) releaseEmulatorScratch(now time.Time) {
	if !m.emulatorReleasePending || now.Sub(m.emulatorReleasedAt) < emulatorReleaseEvery() {
		return
	}
	m.emulatorReleasedAt = now
	pending := false
	for _, w := range m.Windows {
		if w != nil && !w.TryReleaseEmulatorScratch() {
			pending = true
		}
	}
	m.emulatorReleasePending = pending
	memtrim.Request()
}

func (m *OS) emulatorReleaseDue() (time.Time, bool) {
	if !m.emulatorReleasePending {
		return time.Time{}, false
	}
	return m.emulatorReleasedAt.Add(emulatorReleaseEvery()), true
}
