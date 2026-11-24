package app

import (
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// emulatorReleaseEvery is how often the client drops its panes' spare
// emulator buffers. It is the daemon's cleanup interval, and the daemon's sweep
// drops the same reflow scratch and alternate screen for the same panes. It
// does not drop the decoded scrollback lines, which only the client releases.
var emulatorReleaseEvery = sync.OnceValue(session.CleanupEvery)

// releaseEmulatorScratch drops, at most once every emulatorReleaseEvery, the
// buffers each pane's emulator keeps that nothing on screen reads: the reflow
// scratch from its last resize, the alternate screen's cells while the main
// screen is shown, and the decoded history lines a scrolled pane left behind.
// The maintenance tick calls it. The release draws nothing
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
	m.frameBuffersReleaseDue = true
	memtrim.Request()
}

// releaseFrameBuffers drops the compositor's canvas and the layers parsed to
// cells, which the next frame builds again. Each is a cell buffer of the
// screen's size (118 bytes a cell, 1.35 MB at 207x55), and an idle client
// held them for as long as nothing moved. releaseEmulatorScratch marks it
// due, and View runs it on a frame it serves from the cache once the frame
// ticker has stopped, so no compose follows it at once. The mark does not
// wait for the ticker: the emulator release runs once after the last frame
// change, often before the ticker stops 500 ms after that frame. A compose
// that builds them again arms another release (GetCanvas). A busy screen
// keeps the buffers: the cost is one full compose for the first frame after
// an idle period.
func (m *OS) releaseFrameBuffers() {
	if m.screen != nil && !m.screen.ForgetFrame() {
		return
	}
	m.frameBuffersReleaseDue = false
	m.renderCanvas = nil
	m.canvasIsFrame = false
	m.layerCells = nil
	m.kept = keptCanvas{}
	m.directHold.drawn = false
	m.directHold.saved = nil
}

// emulatorReleaseDue is when releaseEmulatorScratch next has work, and false
// when no frame has changed since the last release.
func (m *OS) emulatorReleaseDue() (time.Time, bool) {
	if !m.emulatorReleasePending {
		return time.Time{}, false
	}
	return m.emulatorReleasedAt.Add(emulatorReleaseEvery()), true
}
