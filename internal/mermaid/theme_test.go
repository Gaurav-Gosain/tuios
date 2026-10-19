package mermaid

import (
	"image/color"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestTuiosThemeKeepsItsFloors builds the diagram theme from every tuios
// theme and measures the pairs a diagram draws: text on the background and on
// both surfaces at overlay.ContrastFloor, lines at the same, and the accent,
// critical and series colours on the background at overlay.MarkFloor.
func TestTuiosThemeKeepsItsFloors(t *testing.T) {
	ids := theme.AvailableThemes()
	if len(ids) < 50 {
		t.Fatalf("only %d themes", len(ids))
	}
	for _, id := range ids {
		if err := theme.Initialize(id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		th := Theme(theme.UI(), theme.GetANSIPalette(), theme.TerminalBg(), theme.TerminalFg())
		check := func(what string, c, ground color.Color, floor float64) {
			if r := overlay.ContrastRatio(c, ground); r < floor-0.01 {
				t.Errorf("%s: %s is %.2f, floor %.1f", id, what, r, floor)
			}
		}
		for _, g := range []struct {
			name string
			c    color.Color
		}{{"bg", th.Bg}, {"surface", th.Surface}, {"surface-alt", th.SurfaceAlt}} {
			check("fg on "+g.name, th.Fg, g.c, overlay.ContrastFloor)
		}
		check("muted on surface-alt", th.Muted, th.SurfaceAlt, overlay.ContrastFloor)
		check("line on bg", th.Line, th.Bg, overlay.ContrastFloor)
		check("accent on bg", th.Accent, th.Bg, overlay.MarkFloor)
		check("critical on bg", th.Critical, th.Bg, overlay.MarkFloor)
		for i, c := range th.Series {
			check("series "+string(rune('1'+i))+" on bg", c, th.Bg, overlay.MarkFloor)
		}
	}
}
