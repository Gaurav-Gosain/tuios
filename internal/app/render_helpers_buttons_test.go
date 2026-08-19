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
	prev := config.WindowButtonPosition
	config.WindowButtonPosition = config.WindowButtonPositionRight
	t.Cleanup(func() { config.WindowButtonPosition = prev })

	buttonColor := lipgloss.Color("#7dd3fc")
	buttonStyle := badgeStyle(buttonColor)
	cross := buttonStyle.Render(config.GetWindowButtonClose())
	dash := buttonStyle.Render("  - ")

	var buttons string
	if tiling {
		buttons = makeRounded(dash+cross, buttonColor)
	} else {
		square := buttonStyle.Render(" □ ")
		buttons = makeRounded(dash+square+cross, buttonColor)
	}

	border := layoutBorderRow("", buttons, width, buttonColor, true).text
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

func TestControlPillWidths(t *testing.T) {
	// Left pill + "  - " + " X " + right pill, plus " [] " when floating.
	const tilingPillWidth = 9
	const floatingPillWidth = 12

	buttonColor := lipgloss.Color("#7dd3fc")
	buttonStyle := badgeStyle(buttonColor)
	cross := buttonStyle.Render(config.GetWindowButtonClose())
	dash := buttonStyle.Render("  - ")
	square := buttonStyle.Render(" □ ")

	if got := lipgloss.Width(makeRounded(dash+cross, buttonColor)); got != tilingPillWidth {
		t.Errorf("tiling pill is %d cells wide, want %d", got, tilingPillWidth)
	}
	if got := lipgloss.Width(makeRounded(dash+square+cross, buttonColor)); got != floatingPillWidth {
		t.Errorf("floating pill is %d cells wide, want %d", got, floatingPillWidth)
	}
}

func TestControlPillGlyphsLandOnTheirHitboxes(t *testing.T) {
	closeGlyph := []rune(config.GetWindowButtonClose())[1]

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

// The close glyph must occupy exactly one cell. A wider one would push every
// button in the pill left of where the hit-test looks for it, and a terminal
// that measures it as one cell while drawing it wider would overlap whatever
// sits to its right.
func TestCloseButtonIsThreeCells(t *testing.T) {
	for _, s := range []string{config.WindowButtonClose, config.WindowButtonCloseASCII} {
		if got := lipgloss.Width(s); got != 3 {
			t.Errorf("close button %q is %d cells, want 3", s, got)
		}
		runes := []rune(s)
		if len(runes) != 3 {
			t.Fatalf("close button %q is %d runes, want 3", s, len(runes))
		}
		if runes[0] != ' ' || runes[2] != ' ' {
			t.Errorf("close button %q is missing its padding spaces", s)
		}
		if got := lipgloss.Width(string(runes[1])); got != 1 {
			t.Errorf("close glyph U+%04X is %d cells, want 1", runes[1], got)
		}
	}
}
