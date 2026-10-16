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
// screen is shown. The maintenance tick calls it, on the idle path as well, so
// an idle client releases them without a timer of its own. The release draws
// nothing and marks nothing dirty, so the tick still composes no frame.
//
// It runs on an interval rather than after a resize or when a program leaves
// the alternate screen: a drag resizes at every motion step and reuses the
// scratch each time, and a pager launched over and over allocates its grid at
// most once an interval.
func (m *OS) releaseEmulatorScratch(now time.Time) {
	if now.Sub(m.emulatorReleasedAt) < emulatorReleaseEvery {
		return
	}
	m.emulatorReleasedAt = now
	for _, w := range m.Windows {
		if w != nil {
			w.TryReleaseEmulatorScratch()
		}
	}
	memtrim.Request()
}
