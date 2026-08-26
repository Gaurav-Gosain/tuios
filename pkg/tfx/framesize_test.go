package tfx

import (
	"runtime"
	"testing"
)

// TestFrameReusesItsBuffer guards the frame writer against going back to a
// fresh buffer per frame.
//
// A full screen of decrypt is about 28 KB of output, but Frame used a
// strings.Builder and Builder.Reset drops the buffer, so every frame regrew
// from nothing and allocated 827 KB to produce those 28 KB. At sixty frames a
// second that is 48 MB of garbage a second for a thing that runs while the
// machine is idle.
//
// The ceiling is per frame and generous. It is here to catch the buffer being
// dropped again, not to pin a byte count.
//
// Negative control: putting the strings.Builder and its Reset back takes this
// to roughly 800 KB a frame.
func TestFrameReusesItsBuffer(t *testing.T) {
	const cols, rows = 200, 50
	term := NewTerminalFromCells(fullScreenGrid(cols, rows, 16), TerminalConfig{
		Width: cols, Height: rows, ExistingColorHandling: DynamicExistingColors,
	})
	engine := NewEngine(term, NewRng(1))
	effect := NewDecrypt(DefaultDecryptConfig())
	if err := effect.Build(engine); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Get past the typing phase and let the buffer reach its working size.
	for i := 0; i < 4000; i++ {
		if !effect.Advance(engine) {
			break
		}
	}
	for i := 0; i < 20; i++ {
		_ = engine.Frame()
	}

	const frames = 200
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < frames; i++ {
		_ = engine.Frame()
	}
	runtime.ReadMemStats(&after)

	perFrame := float64(after.TotalAlloc-before.TotalAlloc) / frames
	frameSize := len(engine.Frame())
	t.Logf("Frame is %d bytes and allocates %.0f bytes per call at %dx%d",
		frameSize, perFrame, cols, rows)

	// One frame's worth of string is unavoidable, because the caller is handed
	// a string. The ceiling is expressed against the frame's own size rather
	// than as a byte count so it scales with the effect and still catches the
	// regression it exists for, which cost six times the frame.
	if ceiling := 3 * float64(frameSize); perFrame > ceiling {
		t.Errorf("Frame allocates %.0f bytes per call for a %d byte frame, want under %.0f",
			perFrame, frameSize, ceiling)
	}
}
