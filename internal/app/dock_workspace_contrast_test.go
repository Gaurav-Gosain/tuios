package app

import (
	"image/color"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestWorkspaceStripInkClearsTheContrastFloor is the measurement the strip was
// shipped without. A workspace you can switch to was drawn in FgMute on Panel
// at 2.19:1 and the current one in the raw accent at 2.76:1, which is why the
// second pill in the dock read as absent rather than as inactive.
//
// The ratios are asserted rather than the colours: the accent follows the
// terminal theme, so pinning a hex here would pass for exactly one theme.
func TestWorkspaceStripInkClearsTheContrastFloor(t *testing.T) {
	pal := theme.UI()
	for _, tc := range []struct {
		name   string
		fg, bg color.Color
		before float64
	}{
		{"a pill at rest", workspacePillFg(false, pal), pal.Panel, 2.19},
		{"the current pill", workspacePillFg(true, pal), pal.Panel, 2.76},
		{"an overflow arrow", dockStripArrowFg(pal), pal.Canvas, 2.60},
	} {
		if got := theme.ContrastRatio(tc.fg, tc.bg); got < theme.ContrastFloor {
			t.Errorf("%s measures %.2f:1 against its ground, under the %.1f:1 floor (was %.2f:1)",
				tc.name, got, theme.ContrastFloor, tc.before)
		}
	}
}
