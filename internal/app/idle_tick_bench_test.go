package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// idleOS builds an OS holding n idle daemon windows: live emulators, no pending
// output, no animations, nothing in flight. It is the fixture for the idle-cost
// guard, mirroring one attached client with a handful of quiet shells.
func idleOS(t testing.TB, n int) *OS {
	t.Helper()
	wins := make([]*terminal.Window, 0, n)
	for i := range n {
		wins = append(wins, newTestWindow(t, "idle-"+string(rune('a'+i)), 80, 24))
	}
	return &OS{
		Settings:       config.Global,
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

// TestIdleTickSkipsScans is the idle diet's precise guard: once the model is
// idle, a run of maintenance ticks must take the fast path and do no scan work,
// so the Work counter stays flat while Ticks climbs. A regression that puts a
// full-window scan back on the idle path trips this.
func TestIdleTickSkipsScans(t *testing.T) {
	m := idleOS(t, 3)
	// Settle any first-frame output so the model reaches true idle.
	for range 5 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work0, _ := m.TickStats()

	const ticks = 100
	for range ticks {
		m.Update(TickerMsg(time.Now()))
	}

	gotTicks, work, _ := m.TickStats()
	if work != work0 {
		t.Fatalf("idle ticks did %d units of scan work; the diet must skip them", work-work0)
	}
	if gotTicks < ticks {
		t.Fatalf("Ticks did not advance past %d", ticks)
	}
}

// foldConfiguredIdleOS is idleOS with the rail on and the fold of agent rows
// at rest configured, on a clock that moves a minute every read, and no agent
// ever seen: the setup of someone with no agents who kept the defaults.
func foldConfiguredIdleOS(tb testing.TB) *OS {
	tb.Helper()
	m := idleOS(tb, 3)
	m.Settings.SidebarEnabled = true
	m.Settings.SidebarAgentRestFold = time.Hour
	clock := time.Unix(1_800_000_000, 0)
	prev := sidebarFoldClock
	sidebarFoldClock = func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}
	tb.Cleanup(func() { sidebarFoldClock = prev })
	return m
}

// BenchmarkIdleTickFoldConfigured is BenchmarkIdleTick with the rail on and
// the agent fold configured but no agent present. The fold must add nothing
// to the idle path: no scan, no frame, whatever the clock does.
func BenchmarkIdleTickFoldConfigured(b *testing.B) {
	m := foldConfiguredIdleOS(b)
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

// TestIdleTickFoldConfiguredWithoutAgents guards the zero-agent promise of
// the fold: with it configured and minutes passing, idle ticks with no agent
// ever seen do no scan work and draw no frame.
func TestIdleTickFoldConfiguredWithoutAgents(t *testing.T) {
	m := foldConfiguredIdleOS(t)
	for range 5 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work0, render0 := m.TickStats()
	for range 100 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work, render := m.TickStats()
	if work != work0 || render != render0 {
		t.Fatalf("idle ticks with the fold configured and no agent did %d work and drew %d frames", work-work0, render-render0)
	}
	if m.SidebarAgentsSeen {
		t.Fatal("idle ticks marked an agent seen")
	}
}
