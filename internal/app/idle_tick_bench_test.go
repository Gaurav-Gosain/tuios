package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// idleOS builds an OS holding n idle daemon windows: live emulators, no pending
// output, no animations, nothing in flight. It is the fixture for the idle-cost
// guard, mirroring one attached client with a handful of quiet shells.
func idleOS(t testing.TB, n int) *OS {
	t.Helper()
	wins := make([]*terminal.Window, 0, n)
	for i := 0; i < n; i++ {
		wins = append(wins, newTestWindow(t, "idle-"+string(rune('a'+i)), 80, 24))
	}
	return &OS{
		Windows:        wins,
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
		Width:          120,
		Height:         40,
	}
}

// BenchmarkIdleTick measures the work and allocations of one maintenance tick
// when nothing is animating and no process is exiting. This is the number every
// future milestone's Gate defends: a regression here means the idle path grew a
// cost. See docs/perf.md for recorded baselines.
func BenchmarkIdleTick(b *testing.B) {
	m := idleOS(b, 3)
	// Prime the frame-skip state the idle path relies on.
	m.Update(TickerMsg(time.Now()))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(TickerMsg(time.Now()))
	}
	b.StopTimer()

	_, work, render := m.TickStats()
	b.ReportMetric(float64(work)/float64(b.N), "work/tick")
	b.ReportMetric(float64(render)/float64(b.N), "render/tick")
}

// TestIdleTickAccounting checks the counter mechanism the idle guards read: a
// run of ticks with nothing happening advances Ticks and never draws a frame,
// so the render count stays flat regardless of how the per-tick work is gated.
func TestIdleTickAccounting(t *testing.T) {
	m := idleOS(t, 3)
	// First tick settles frame-skip; count from the settled state.
	m.Update(TickerMsg(time.Now()))
	_, _, render0 := m.TickStats()

	const ticks = 50
	for i := 0; i < ticks; i++ {
		m.Update(TickerMsg(time.Now()))
	}

	got, _, render := m.TickStats()
	if got < ticks {
		t.Fatalf("Ticks did not advance: got %d, want >= %d", got, ticks)
	}
	if render != render0 {
		t.Fatalf("idle ticks drew %d frame(s); idle must skip rendering", render-render0)
	}
}
