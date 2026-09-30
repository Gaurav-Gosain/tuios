package app

import (
	"math/rand"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The picture-in-picture view's cost budget: nothing at idle, and a small
// share of a frame while its pane floods.

// TestPiPIdleAddsNoFrames is the idle guard. A pinned pane that writes nothing
// must not make a maintenance tick compose a frame or do scan work.
func TestPiPIdleAddsNoFrames(t *testing.T) {
	m := idleOS(t, 3)
	m.UserConfig = config.DefaultConfig()
	if err := m.PinPiP(m.Windows[2].ID); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work0, render0 := m.TickStats()
	for range 100 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work, render := m.TickStats()
	if work != work0 || render != render0 {
		t.Fatalf("100 idle ticks with a pane pinned did %d scan work and %d renders, want none",
			work-work0, render-render0)
	}
}

// pipFloodOS is a 160x44 client with a pane a that has the focus and a pane b
// on workspace 2 that floods, b pinned or not.
func pipFloodOS(b *testing.B, pinned bool) (*OS, func()) {
	b.Helper()
	m := gapTestOS(b, 2)
	m.Width, m.Height = 160, 44
	m.UserConfig = config.DefaultConfig()
	m.TileAllWindows()
	m.FocusWindow(0)
	m.MoveWindowToWorkspace(1, 2)
	src := m.Windows[1]
	if pinned {
		if err := m.PinPiP(src.ID); err != nil {
			b.Fatal(err)
		}
	}
	rng := rand.New(rand.NewSource(1))
	cols, rows := src.ContentWidth(), src.ContentHeight()
	return m, func() {
		floodFrame(b, src, rng, cols, rows)
		src.HasNewOutput.Store(true)
	}
}

// BenchmarkPiPFlood composes one frame per flood frame of the pinned pane, as
// the PTY data path does, against the same flood with nothing pinned. The
// flood write into the emulator is inside the loop in every row. The view
// takes the fast path away, so unpinned-compositor is the fair baseline for
// what the view itself costs.
func BenchmarkPiPFlood(b *testing.B) {
	for _, tc := range pipBenchCases {
		b.Run(tc.name, func(b *testing.B) {
			tc.setup(b)
			m, flood := pipFloodOS(b, tc.pinned)
			_ = m.composeFrame()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				flood()
				m.MarkTerminalsWithNewContent()
				_ = m.composeFrame()
			}
		})
	}
}

// pipBenchCase is one row both benchmarks report.
type pipBenchCase struct {
	name   string
	pinned bool
	slow   bool
}

// setup turns the fast path off for the run when the row asks for it.
func (c pipBenchCase) setup(b *testing.B) {
	if !c.slow {
		return
	}
	prev := fastPathDisabled
	fastPathDisabled = true
	b.Cleanup(func() { fastPathDisabled = prev })
}

var pipBenchCases = []pipBenchCase{
	{"unpinned", false, false}, {"unpinned-compositor", false, true}, {"pinned", true, false},
}

// BenchmarkPiPQuietFrame composes frames while the pinned pane is quiet and
// the focused pane is the one changing, as when the user types. The focused
// pane fills the screen, so with nothing pinned the frame takes the fast path;
// unpinned-compositor is the same frame through the compositor, which is the
// path a pinned view puts it on.
func BenchmarkPiPQuietFrame(b *testing.B) {
	for _, tc := range pipBenchCases {
		b.Run(tc.name, func(b *testing.B) {
			tc.setup(b)
			m, _ := pipFloodOS(b, tc.pinned)
			focused := m.Windows[0]
			_ = m.composeFrame()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				focused.WriteOutput([]byte("x"))
				m.MarkTerminalsWithNewContent()
				_ = m.composeFrame()
			}
		})
	}
}
