package app

import (
	"fmt"
	"os"
)

type tickStats struct {
	Ticks        uint64
	Work         uint64
	Render       uint64
	Motion       uint64
	Rail         uint64
	TickerWakes  uint64
	TickerSleeps uint64
	AgentsTicks  uint64
	Cockpit      uint64
}

// TickStats returns the maintenance-tick counters (ticks, work, renders). The
// idle benchmark and idle e2e read these to assert idle ticks stay cheap.
func (m *OS) TickStats() (ticks, work, render uint64) {
	return m.tickStats.Ticks, m.tickStats.Work, m.tickStats.Render
}

// DumpTickStats writes the tick counters to the file named by TUIOS_STATS_FILE,
// if set. Called once on clean exit so the idle e2e can read cumulative work
// and render counts from a real run without an internal probe. With the same
// variable set, SIGUSR1 writes them too (see dumpTickStatsOnSignal), which is
// how a test reads the counts at both ends of an idle window.
func (m *OS) DumpTickStats() {
	path := os.Getenv("TUIOS_STATS_FILE")
	if path == "" {
		return
	}
	st := m.tickStats
	_ = os.WriteFile(path, fmt.Appendf(nil, "ticks=%d work=%d render=%d rail=%d motion=%d tickerWakes=%d tickerSleeps=%d agentsTicks=%d cockpit=%d\n",
		st.Ticks, st.Work, st.Render, st.Rail, st.Motion, st.TickerWakes, st.TickerSleeps, st.AgentsTicks, st.Cockpit), 0o600)
}

type tickStatsDumpMsg struct{}
