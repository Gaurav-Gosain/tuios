package vt_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestMargins_OutOfRangeDoesNotPanic covers a crash that took the daemon down,
// not one pane: DECSTBM accepted a bottom row the screen did not have, and the
// first scroll inside that region indexed past the end of the cell buffer. A
// guest reaches it by sizing its scroll region for the terminal it was started
// in and then being resized smaller, which is what every full-screen program
// does across a window resize.
func TestMargins_OutOfRangeDoesNotPanic(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// The reported repro: 80x24, resized to 80x10, then a region sized for
		// the old screen followed by a scroll up.
		{"stbm past bottom then SU", "\x1b[1;24r\x1b[3S"},
		{"stbm past bottom then SD", "\x1b[1;24r\x1b[3T"},
		{"stbm past bottom then IL", "\x1b[1;24r\x1b[5L"},
		{"stbm past bottom then DL", "\x1b[1;24r\x1b[5M"},
		{"stbm top and bottom past bottom", "\x1b[5;99r\x1b[2S"},
		{"stbm absurd bottom", "\x1b[1;1000r\x1b[999S"},
		{"stbm past bottom then linefeeds", "\x1b[1;24r\nx\n\n\n\n\n\n\n\n\n\n\n\n"},
		{"stbm past bottom then RI at bottom", "\x1b[1;24r\x1b[24;1H\x1bM"},
		{"stbm past bottom with origin mode", "\x1b[1;24r\x1b[?6h\x1b[20;1H\x1b[3S"},
		// DECSLRM has the same shape, behind DECLRMM.
		{"slrm past right then SU", "\x1b[?69h\x1b[1;200s\x1b[3S"},
		{"slrm past right then ICH", "\x1b[?69h\x1b[1;200s\x1b[5@"},
		{"slrm past right then DCH", "\x1b[?69h\x1b[1;200s\x1b[5P"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(80, 24)
			emu.Resize(80, 10)
			if _, err := emu.WriteString(tc.input); err != nil {
				t.Fatalf("write: %v", err)
			}
			_ = emu.String()

			// The region a guest named must never reach past the screen, or
			// every later scroll is a panic waiting for a trigger.
			r := emu.ScrollRegion()
			if r.Max.Y > emu.Height() || r.Min.Y < 0 {
				t.Errorf("vertical margins %v escape a %d-row screen", r, emu.Height())
			}
			if r.Max.X > emu.Width() || r.Min.X < 0 {
				t.Errorf("horizontal margins %v escape a %d-column screen", r, emu.Width())
			}
		})
	}
}

// TestMargins_ClampedRegionStillScrolls checks the clamp did not turn a legal
// region into a dead one: a bottom past the edge scrolls the rows that do
// exist rather than being ignored outright.
func TestMargins_ClampedRegionStillScrolls(t *testing.T) {
	emu := vt.NewEmulator(10, 4)
	if _, err := emu.WriteString("\x1b[1;99r\x1b[1;1Ha\r\nb\r\nc\r\nd\x1b[1S"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := emu.String(), "b\nc\nd\n"; got != want {
		t.Errorf("after clamped-region scroll got %q want %q", got, want)
	}
}
