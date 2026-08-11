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
	_ = os.WriteFile(path, []byte(fmt.Sprintf("ticks=%d work=%d render=%d\n",
		m.tickStats.Ticks, m.tickStats.Work, m.tickStats.Render)), 0o600)
}
