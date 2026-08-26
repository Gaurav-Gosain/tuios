package tfx

import (
	"runtime"
	"testing"
)

// fullScreenGrid builds a dense capture where every cell holds a character.
// paletteSize is how many distinct colours the screen uses, which is what
// drives the engine's cost: a character resolving to its own colour needs its
// own ramp, and characters that share a colour share the whole ramp.
//
// A real terminal screen uses a theme, so a dozen or two. A pane dumping
// truecolor output is the other extreme and gets one colour per cell.
func fullScreenGrid(cols, rows, paletteSize int) [][]InputCell {
	grid := make([][]InputCell, rows)
	symbols := []string{"a", "b", "c", "d", "e", "#", "|", "-"}
	for y := range grid {
		row := make([]InputCell, cols)
		for x := range row {
			shade := (y*cols + x) % paletteSize
			row[x] = InputCell{
				Symbol: symbols[(x+y)%len(symbols)],
				Fg:     RGB(uint8(shade*7), uint8(shade*11), 200),
				HasFg:  true,
			}
		}
		grid[y] = row
	}
	return grid
}

// buildFullScreen builds the heaviest effect over a full screen and reports
// what it cost.
func buildFullScreen(tb testing.TB, cols, rows, paletteSize int) (liveMB, churnMB float64, visuals int) {
	tb.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	term := NewTerminalFromCells(fullScreenGrid(cols, rows, paletteSize), TerminalConfig{
		Width: cols, Height: rows, ExistingColorHandling: DynamicExistingColors,
	})
	engine := NewEngine(term, NewRng(1))
	if err := NewDecrypt(DefaultDecryptConfig()).Build(engine); err != nil {
		tb.Fatalf("Build: %v", err)
	}

	// The live heap is what the saver actually holds while it runs. The churn
	// is everything allocated on the way there, most of which is slice regrowth
	// and is collectable straight away.
	runtime.GC()
	runtime.ReadMemStats(&after)
	if got := len(term.InputCharacters); got != cols*rows {
		tb.Errorf("input characters = %d, want %d", got, cols*rows)
	}
	live := float64(after.HeapAlloc-before.HeapAlloc) / (1 << 20)
	churn := float64(after.TotalAlloc-before.TotalAlloc) / (1 << 20)
	runtime.KeepAlive(engine)
	return live, churn, len(term.visuals.m)
}

// TestFullScreenDecryptStaysWithinBudget is the guard on the case that decided
// the visual cache: a fullscreen decrypt is around a million frames, and
// without sharing that is a million formatted strings.
//
// The ceilings are generous. They are here to catch a change that removes the
// sharing, not to pin an allocation count.
//
// Negative control: making visualCache.get always return a fresh visual takes
// the themed screen well past both its visual count and its memory ceiling.
func TestFullScreenDecryptStaysWithinBudget(t *testing.T) {
	const cols, rows = 200, 50

	// A themed screen, which is what a session actually looks like.
	themedMB, themedChurn, themedVisuals := buildFullScreen(t, cols, rows, 16)
	t.Logf("themed screen: %d visuals, %.1f MB live, %.1f MB churn", themedVisuals, themedMB, themedChurn)

	// The other extreme, for the record: one colour per cell.
	truecolorMB, truecolorChurn, truecolorVisuals := buildFullScreen(t, cols, rows, cols*rows)
	t.Logf("truecolor screen: %d visuals, %.1f MB live, %.1f MB churn", truecolorVisuals, truecolorMB, truecolorChurn)

	if themedVisuals > 20_000 {
		t.Errorf("a themed screen built %d visuals, so the cache is not sharing", themedVisuals)
	}
	const themedCeilingMB = 60
	if themedMB > themedCeilingMB {
		t.Errorf("a themed screen cost %.1f MB to build, want under %d MB", themedMB, themedCeilingMB)
	}
	// The pathological case is allowed to cost more, but not without limit:
	// it runs on an idle machine and the memory is freed on dismissal.
	const truecolorCeilingMB = 80
	if truecolorMB > truecolorCeilingMB {
		t.Errorf("a truecolor screen cost %.1f MB to build, want under %d MB", truecolorMB, truecolorCeilingMB)
	}
}

// BenchmarkFullScreenFrame measures one animation frame at a full screen,
// which is what the host pays per rendered frame while the saver runs.
//
// The effect is rebuilt off the clock when it runs out, so the measurement
// covers frames rather than builds.
func BenchmarkFullScreenFrame(b *testing.B) {
	const cols, rows = 200, 50
	grid := fullScreenGrid(cols, rows, 16)
	build := func() (*Engine, Effect) {
		term := NewTerminalFromCells(grid, TerminalConfig{
			Width: cols, Height: rows, ExistingColorHandling: DynamicExistingColors,
		})
		engine := NewEngine(term, NewRng(1))
		effect := NewDecrypt(DefaultDecryptConfig())
		if err := effect.Build(engine); err != nil {
			b.Fatalf("Build: %v", err)
		}
		// Get past the typing phase so the benchmark measures a busy frame.
		for i := 0; i < 4000; i++ {
			if !effect.Advance(engine) {
				break
			}
		}
		return engine, effect
	}

	engine, effect := build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !effect.Advance(engine) {
			b.StopTimer()
			engine, effect = build()
			b.StartTimer()
			continue
		}
		_ = engine.Frame()
	}
}
