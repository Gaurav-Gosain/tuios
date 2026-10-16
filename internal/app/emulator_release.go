package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// emulatorReleaseEvery is how often the client drops its panes' spare
// emulator buffers. It is the daemon's cleanup interval, which holds the same
// buffers for the same panes and drops them in its cleanup sweep.
var emulatorReleaseEvery = session.CleanupEvery()

// releaseEmulatorScratch drops, at most once every emulatorReleaseEvery, the
// buffers each pane's emulator keeps that nothing on screen reads: the reflow
// scratch from its last resize and the alternate screen's cells while the main
// screen is shown. The maintenance tick calls it. The release draws nothing
// and marks nothing dirty, so the tick still composes no frame.
//
// It runs on an interval rather than after a resize or when a program leaves
// the alternate screen: a drag resizes at every motion step and reuses the
// scratch each time, and a pager launched over and over allocates its grid at
// most once an interval.
//
// It runs only after a frame changed since the last release
// (emulatorReleasePending), and nextWake arms a tick for it then. Buffers do
// not grow while nothing is drawn, so an idle client releases once and then
// sleeps. A pane whose lock was busy keeps the release pending.
func (m *OS) releaseEmulatorScratch(now time.Time) {
	if !m.emulatorReleasePending || now.Sub(m.emulatorReleasedAt) < emulatorReleaseEvery {
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

// emulatorReleaseDue is when releaseEmulatorScratch next has work, and false
// when no frame has changed since the last release.
func (m *OS) emulatorReleaseDue() (time.Time, bool) {
	if !m.emulatorReleasePending {
		return time.Time{}, false
	}
	return m.emulatorReleasedAt.Add(emulatorReleaseEvery), true
}
