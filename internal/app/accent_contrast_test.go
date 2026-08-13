package app

import (
	"image/color"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// accentCursorFloor is the ratio the cursor glyph has to clear against the
// swatch it is drawn on. It is the WCAG graphics floor rather than the text one:
// the mark is a solid 3.0-class shape, and no pair of a fixed light and a fixed
// dark ink clears 4.5 across the whole colour space.
const accentCursorFloor = 3.0

// TestAccentCursorReadsOnEverySwatch sweeps the whole space the picker can put
// under its cursor and measures the mark against the cell it lands on. Picking
// the ink by perceived luminance against a threshold put the mark at 1.54:1 on
// saturated greens, which is a cursor the user cannot find on the cell they are
// standing on.
func TestAccentCursorReadsOnEverySwatch(t *testing.T) {
	const cols, rows = 12, 8

	worst, worstOn := 21.0, color.RGBA{}
	measure := func(c color.RGBA) {
		if r := theme.ContrastRatio(accentContrast(c), c); r < worst {
			worst, worstOn = r, c
		}
	}
	for hue := 0; hue < 360; hue += 15 {
		h := float64(hue)
		for col := range cols {
			for row := range rows {
				measure(accentCellColor(h, col, row, cols, rows))
			}
		}
		// The hue strip and the harmony chips carry the cursor too.
		measure(hslToRGB(h, 1, 0.5))
	}
	// Every slot swatch wears the cursor when the quick-pick row has it.
	for i := range accentSwatchCount {
		measure(SlotAccent(i).RGB())
	}

	if worst < accentCursorFloor {
		t.Errorf("the cursor measures %.2f:1 on %s, under the %.1f:1 graphics floor",
			worst, hexString(worstOn), accentCursorFloor)
	}
	t.Logf("worst cursor contrast %.2f:1 on %s", worst, hexString(worstOn))
}

// TestContrastTextPicksTheBetterInk: whichever ink in its vocabulary reads
// better on the ground wins, on every ground. A luminance threshold cannot
// promise this and a luminance threshold is what was there.
func TestContrastTextPicksTheBetterInk(t *testing.T) {
	grounds := make([]color.RGBA, 0, 16*16*16)
	for r := 0; r < 256; r += 17 {
		for g := 0; g < 256; g += 17 {
			for b := 0; b < 256; b += 17 {
				grounds = append(grounds, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xFF})
			}
		}
	}

	// The inks it chooses between, learned from what it returns rather than
	// named here: this test is about the choice, not the palette.
	inks := map[color.Color]bool{}
	for _, bg := range grounds {
		inks[theme.ContrastText(bg)] = true
	}
	if len(inks) < 2 {
		t.Fatalf("ContrastText only ever returns %d ink(s), so it is not choosing", len(inks))
	}

	for _, bg := range grounds {
		got := theme.ContrastRatio(theme.ContrastText(bg), bg)
		for ink := range inks {
			if best := theme.ContrastRatio(ink, bg); best > got+0.001 {
				t.Fatalf("on %s the chosen ink reads %.2f:1 where %v reads %.2f:1",
					hexString(bg), got, ink, best)
			}
		}
	}
}
