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
	// TickerWakes is every time a frame woke Bubble Tea's frame ticker from
	// idle to the frame rate, and TickerSleeps every time the ticker went
	// idle. See noteFrame and idleFrameTicker.
	TickerWakes  uint64
	TickerSleeps uint64
	// AgentsTicks is every minute tick of the open agents view, and Cockpit
	// every frame of it drawn afresh, which an unchanged view never is.
	AgentsTicks uint64
	Cockpit     uint64
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
	_ = os.WriteFile(path, []byte(fmt.Sprintf("ticks=%d work=%d render=%d rail=%d motion=%d tickerWakes=%d tickerSleeps=%d agentsTicks=%d cockpit=%d\n",
		st.Ticks, st.Work, st.Render, st.Rail, st.Motion, st.TickerWakes, st.TickerSleeps, st.AgentsTicks, st.Cockpit)), 0o600)
}

// tickStatsDumpMsg asks Update to write the tick counters now. See
// dumpTickStatsOnSignal.
type tickStatsDumpMsg struct{}
