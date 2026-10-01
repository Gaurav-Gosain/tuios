package app

import (
	"fmt"
	"os"
)

// tickStats is the idle-cost regression guard's counter. Ticks is every
// TickerMsg handled; Work is those that ran the full maintenance scans (the
// idle diet skips the rest); Render is those that drew a frame. At idle Work
// and Render must stay flat while Ticks climbs.
type tickStats struct {
	Ticks  uint64
	Work   uint64
	Render uint64
	// Motion is every motion clock frame handled (see motion.go). With nothing
	// moving it stays at zero, because the clock is never armed.
	Motion uint64
	// Rail is every rebuild of the rail's rows, a frame the render cache
	// could not serve. A quiet folder refresh that changed nothing adds none.
	Rail uint64
}

// TickStats returns the maintenance-tick counters (ticks, work, renders). The
// idle benchmark and idle e2e read these to assert idle ticks stay cheap.
func (m *OS) TickStats() (ticks, work, render uint64) {
	return m.tickStats.Ticks, m.tickStats.Work, m.tickStats.Render
}

// DumpTickStats writes the tick counters to the file named by TUIOS_STATS_FILE,
// if set. Called once on clean exit so the idle e2e can read cumulative work
// and render counts from a real run without an internal probe.
func (m *OS) DumpTickStats() {
	path := os.Getenv("TUIOS_STATS_FILE")
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte(fmt.Sprintf("ticks=%d work=%d render=%d rail=%d motion=%d\n",
		m.tickStats.Ticks, m.tickStats.Work, m.tickStats.Render, m.tickStats.Rail, m.tickStats.Motion)), 0o600)
}
