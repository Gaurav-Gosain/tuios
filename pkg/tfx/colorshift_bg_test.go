package tfx

import (
	"strings"
	"testing"
)

// TestColorShiftKeepsTheInputBackground checks that a captured screen's
// background fills survive the wave.
//
// A selection bar or a filled panel is a run of cells whose background is the
// whole of what they draw. Upstream shifts only the foreground, which is right
// for piped text and wrong here: the character never moves, so a lost
// background blinks the bar out for the entire effect and back at the end.
// This was caught by looking at a rendered frame, not by a test.
//
// Negative control: dropping the background carry in the gradient scene makes
// the frames between the first and the last carry no background at all.
func TestColorShiftKeepsTheInputBackground(t *testing.T) {
	blue := RGB(0, 0, 255)
	grid := [][]InputCell{{
		{Symbol: "x", Fg: RGB(255, 255, 255), HasFg: true, Bg: blue, HasBg: true},
	}}
	term := NewTerminalFromCells(grid, TerminalConfig{
		Width: 1, Height: 1, ExistingColorHandling: DynamicExistingColors,
	})
	engine := NewEngine(term, NewRng(2))
	config := DefaultColorShiftConfig()
	config.Cycles = 1
	effect := NewColorShift(config)

	frames, err := Run(effect, engine, 5000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) < 10 {
		t.Fatalf("the effect ran for %d frames, want a real animation", len(frames))
	}
	const wantBg = "\x1b[48;2;0;0;255m"
	for i, frame := range frames {
		if !strings.Contains(frame, wantBg) {
			t.Fatalf("frame %d of %d lost the input background: %q", i, len(frames), frame)
		}
	}
}

// TestColorShiftLeavesBackgroundsAloneWithoutInputColors checks the change
// above is scoped. Under the default colour policy the effect is still exactly
// upstream's: foreground only.
//
// Negative control: carrying the background unconditionally makes this frame
// carry a background sequence.
func TestColorShiftLeavesBackgroundsAloneWithoutInputColors(t *testing.T) {
	grid := [][]InputCell{{
		{Symbol: "x", Fg: RGB(255, 255, 255), HasFg: true, Bg: RGB(0, 0, 255), HasBg: true},
	}}
	term := NewTerminalFromCells(grid, TerminalConfig{Width: 1, Height: 1})
	engine := NewEngine(term, NewRng(2))
	config := DefaultColorShiftConfig()
	config.Cycles = 1
	effect := NewColorShift(config)

	frames, err := Run(effect, engine, 5000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("the effect produced no frames")
	}
	if strings.Contains(frames[0], "\x1b[48;2;") {
		t.Errorf("the wave carried a background under the default colour policy: %q", frames[0])
	}
}
