package app

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/charmbracelet/x/ansi"
)

// The control pill's width and the column each glyph lands on are a contract
// with the mouse hit-test, which addresses the buttons as negative offsets from
// the window's right edge. Changing a button's character or its padding silently
// slides every glyph sideways and the clicks stop landing, so pin the layout.

// pillColumns renders the top border the way addToBorder does and returns the
// visible cells, so a column can be addressed as an offset from the right edge.
func pillColumns(t *testing.T, tiling bool, width int) []rune {
	t.Helper()

	// The documented offsets are counted in from the row's right edge, so this
	// pins the position rather than leaning on it being the default.
	prev := config.Global.WindowButtonPosition
	config.Global.WindowButtonPosition = config.WindowButtonPositionRight
	t.Cleanup(func() { config.Global.WindowButtonPosition = prev })

	buttonColor := lipgloss.Color("#7dd3fc")
	buttonStyle := badgeStyle(buttonColor)
	cross := buttonStyle.Render(config.Global.GetWindowButtonClose())
	dash := buttonStyle.Render("  - ")

	var buttons string
	if tiling {
		buttons = makeRounded(dash+cross, buttonColor, &config.Global)
	} else {
		square := buttonStyle.Render(" □ ")
		buttons = makeRounded(dash+square+cross, buttonColor, &config.Global)
	}

	border := layoutBorderRow("", buttons, width, buttonColor, true, &config.Global).text
	if border == "" {
		t.Fatalf("layoutBorderRow drew nothing for width %d", width)
	}
	return []rune(ansi.Strip(border))
}

// at returns the cell at a negative offset from the right edge, matching the
// convention the button-position constants use.
func at(t *testing.T, cols []rune, offset int) rune {
	t.Helper()
	idx := len(cols) + offset
	if idx < 0 || idx >= len(cols) {
		t.Fatalf("offset %d is outside a %d-cell border", offset, len(cols))
	}
	return cols[idx]
}

func TestControlPillGlyphsLandOnTheirHitboxes(t *testing.T) {
	closeGlyph := []rune(config.Global.GetWindowButtonClose())[1]

	// Wide and narrow panes: the pill is right-aligned, so its glyphs sit at
	// the same offsets from the right edge regardless of how much border runs
	// to their left.
	for _, width := range []int{20, 40, 78, 200} {
		t.Run("tiling", func(t *testing.T) {
			cols := pillColumns(t, true, width)

			// " X " spans CloseButtonLeft..CloseButtonRight with the glyph in
			// the middle and a padding cell on either side.
			if got := at(t, cols, config.CloseButtonLeft); got != ' ' {
				t.Errorf("width %d: CloseButtonLeft (%d) holds %q, want a padding space",
					width, config.CloseButtonLeft, got)
			}
			if got := at(t, cols, config.CloseButtonLeft+1); got != closeGlyph {
				t.Errorf("width %d: close glyph is not centred in its hitbox: found %q",
					width, got)
			}
			if got := at(t, cols, config.CloseButtonRight); got != ' ' {
				t.Errorf("width %d: CloseButtonRight (%d) holds %q, want a padding space",
					width, config.CloseButtonRight, got)
			}

			if got := at(t, cols, config.MinimizeButtonLeftTiling+1); got != '-' {
				t.Errorf("width %d: minimize glyph not centred in its tiling hitbox: found %q",
					width, got)
			}
		})

		t.Run("floating", func(t *testing.T) {
			cols := pillColumns(t, false, width)

			if got := at(t, cols, config.CloseButtonLeft+1); got != closeGlyph {
				t.Errorf("width %d: close glyph is not centred in its hitbox: found %q",
					width, got)
			}
			if got := at(t, cols, config.MaximizeButtonLeft+1); got != '□' {
				t.Errorf("width %d: maximize glyph not centred in its hitbox: found %q",
					width, got)
			}
			if got := at(t, cols, config.MinimizeButtonLeftNonTiling+1); got != '-' {
				t.Errorf("width %d: minimize glyph not centred in its non-tiling hitbox: found %q",
					width, got)
			}
		})
	}
}
