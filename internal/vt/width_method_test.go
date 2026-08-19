package vt_test

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestWidthMethod_MatchesHowCharactersAreActuallyPlaced covers a disagreement
// the emulator had with itself.
//
// The emulator satisfies uv.Screen, and ultraviolet asks a screen which width
// method it uses before building a cell to write into it. The placement path
// measures every cluster with ansi.GraphemeWidth and always has. WidthMethod
// answered ansi.WcWidth unless DEC mode 2027 was on, so a cell built by
// ultraviolet for this screen could be one column narrower than the cell the
// emulator would have written for the same text.
//
// One column is the whole bug. A wrong width on a cluster shifts everything
// after it along the row, and in a multiplexer the column it shifts into
// belongs to the pane next door.
func TestWidthMethod_MatchesHowCharactersAreActuallyPlaced(t *testing.T) {
	// The classes where the two methods disagree. Everything else, including
	// ambiguous width, combining marks, skin tones and joined sequences, comes
	// out the same either way.
	samples := []struct {
		name string
		s    string
	}{
		{"emoji presentation selector", "❤️"},
		{"warning sign with selector", "⚠️"},
		{"information source with selector", "ℹ️"},
		{"keycap sequence", "1️⃣"},
		{"regional indicator pair", "\U0001f1fa\U0001f1f8"},
		{"plain wide character", "世"},
		{"combining mark", "é"},
		{"skin tone", "\U0001f44d\U0001f3fd"},
	}

	for _, tc := range samples {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(20, 1)
			if _, err := emu.WriteString(tc.s); err != nil {
				t.Fatalf("write: %v", err)
			}
			placed := emu.CellAt(0, 0)
			if placed == nil {
				t.Fatal("nothing was placed")
			}

			// What ultraviolet would build for the same text, having asked the
			// screen which method it uses.
			built := uv.NewCell(emu.WidthMethod(), tc.s)
			if built == nil {
				t.Fatal("uv.NewCell returned nothing")
			}

			if built.Width != placed.Width {
				t.Errorf("the emulator places %q in %d columns, but a cell built with the "+
					"method it reports is %d columns; anything written through ultraviolet "+
					"lands off by %d",
					tc.s, placed.Width, built.Width, placed.Width-built.Width)
			}
		})
	}
}
