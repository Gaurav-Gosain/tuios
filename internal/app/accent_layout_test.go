package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// TestAccentLayoutBreakpoints pins the two widths the layout turns on and the
// column either side of each, because a breakpoint is only a decision at its
// own edge.
func TestAccentLayoutBreakpoints(t *testing.T) {
	for _, tc := range []struct {
		w    int
		want accentLayout
		name string
	}{
		{120, accentLayoutWide, "desktop"},
		{74, accentLayoutWide, "one over the wide floor"},
		{73, accentLayoutWide, "the wide floor"},
		{72, accentLayoutStacked, "one under the wide floor"},
		{41, accentLayoutStacked, "one over the stacked floor"},
		{40, accentLayoutStacked, "the stacked floor"},
		{39, accentLayoutCompact, "one under the stacked floor"},
		{30, accentLayoutCompact, "the narrowest screen the overlays support"},
	} {
		m := accentTestOS(t, tc.w, 30)
		if got := m.accentPlan().Mode; got != tc.want {
			t.Errorf("w=%d (%s): layout %d, want %d", tc.w, tc.name, got, tc.want)
		}
	}

	// Wide also needs the height for its right column whole, since a clipped
	// column is worse than a stacked one.
	if got := (&OS{Width: 120, Height: 8, EffectiveWidth: 120, EffectiveHeight: 8}).accentPlan().Mode; got == accentLayoutWide {
		t.Error("a wide screen with eight rows still laid out wide")
	}
}

// accentFrame renders the picker and returns the frame with styling stripped
// and the geometry it reports.
func accentFrame(t *testing.T, m *OS) ([]string, overlay.Geometry) {
	t.Helper()
	content, geo, _ := m.renderAccentPicker()
	rows := strings.Split(content, "\n")
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, stripANSIForTrace(r))
	}
	return out, geo
}

// TestAccentLayoutRowsAreRectangular: every row of the dialog is exactly the
// dialog's width, at every screen the layout has an opinion about. A row that
// is a cell short leaves a hole the pane behind shows through.
func TestAccentLayoutRowsAreRectangular(t *testing.T) {
	for _, w := range []int{120, 74, 73, 72, 60, 41, 40, 39, 30} {
		for _, h := range []int{40, 30, 22, 20, 16, 14, 12, 10, 9} {
			m := accentTestOS(t, w, h)
			m.OpenAccentPicker("aaaaaaaa1111")
			lines, geo := accentFrame(t, m)
			if len(lines) != geo.Height {
				t.Fatalf("%dx%d: drew %d rows, reports %d", w, h, len(lines), geo.Height)
			}
			if geo.Height > h {
				t.Errorf("%dx%d: the dialog is %d rows tall", w, h, geo.Height)
			}
			for i, l := range lines {
				if got := lipgloss.Width(l); got != geo.Width {
					t.Fatalf("%dx%d: row %d is %d cells, want %d: %q", w, h, i, got, geo.Width, l)
				}
			}
			if geo.Width > w {
				t.Errorf("%dx%d: the dialog is %d cells wide", w, h, geo.Width)
			}
		}
	}
}

// TestAccentWideColumnsStayOnTheirOwnSide: the rule is the boundary, and a rect
// recorded on the wrong side of it would take clicks meant for the other
// column. Recording in column coordinates and shifting once is what this is
// checking held.
func TestAccentWideColumnsStayOnTheirOwnSide(t *testing.T) {
	for _, w := range []int{73, 74, 100, 160} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		lines, geo := accentFrame(t, m)

		// The rule's column, found in the frame rather than computed.
		ruleX := -1
		for x, r := range []rune(lines[len(lines)/2]) {
			if r == '┆' {
				ruleX = x
			}
		}
		if ruleX < 0 {
			t.Fatalf("w=%d: no column rule in the wide layout:\n%s", w, strings.Join(lines, "\n"))
		}
		if want := geo.BodyX + accentWideLeft; ruleX != want {
			t.Errorf("w=%d: the rule is in column %d, want %d", w, ruleX, want)
		}

		left := map[accentHitKind]bool{accentHitANSI: true, accentHitHue: true, accentHitGrid: true, accentHitClear: true}
		right := map[accentHitKind]bool{accentHitHex: true, accentHitSlider: true, accentHitHarmony: true}
		for _, h := range m.accentHits {
			switch {
			case left[h.Kind]:
				if h.Rect.X1 > ruleX {
					t.Errorf("w=%d: a left-column rect %v reaches past the rule at %d", w, h.Rect, ruleX)
				}
			case right[h.Kind]:
				if h.Rect.X0 <= ruleX {
					t.Errorf("w=%d: a right-column rect %v starts at or before the rule at %d", w, h.Rect, ruleX)
				}
			}
			if h.Rect.X0 < 0 || h.Rect.X1 > geo.Width || h.Rect.Y0 < 0 || h.Rect.Y1 > geo.Height {
				t.Errorf("w=%d: %v is outside the %dx%d dialog", w, h.Rect, geo.Width, geo.Height)
			}
		}
	}
}

// TestAccentHitsMatchTheDrawnCellsAtEveryBreakpoint presses both edge columns
// of every recorded rect and checks the picker lands where the rect was drawn
// for. Both edges rather than the middle: an off-by-one in a rect only shows at
// its ends.
func TestAccentHitsMatchTheDrawnCellsAtEveryBreakpoint(t *testing.T) {
	for _, w := range []int{120, 74, 73, 72, 40, 39, 30} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		p := m.accentPlan()
		m.renderAccentPicker()

		hits := append([]accentHit(nil), m.accentHits...)
		var grid, hue int
		for _, h := range hits {
			switch h.Kind {
			case accentHitGrid:
				grid++
			case accentHitHue:
				hue++
			}
		}
		if grid != p.GridCols*p.GridRows {
			t.Errorf("w=%d: %d grid rects for a %dx%d grid", w, grid, p.GridCols, p.GridRows)
		}
		if hue != p.HueCells {
			t.Errorf("w=%d: %d hue rects for a %d-cell strip", w, hue, p.HueCells)
		}
		// Every swatch is drawn in the cells its rect claims, and the row of them
		// exactly fills the column between the sigil and the pad.
		for _, h := range hits {
			if h.Kind != accentHitGrid {
				continue
			}
			if got := h.Rect.X1 - h.Rect.X0; got != p.CellWidth {
				t.Fatalf("w=%d: cell (%d,%d) is %d cells wide, want %d", w, h.Col, h.Row, got, p.CellWidth)
			}
		}

		for _, h := range hits {
			for _, x := range []int{h.Rect.X0, h.Rect.X1 - 1} {
				switch h.Kind {
				case accentHitGrid:
					if ok, _ := m.accentPickerPress(x, h.Rect.Y0); !ok {
						t.Fatalf("w=%d: a press at column %d of %v was not routed", w, x, h.Rect)
					}
					if m.AccentPicker.Col != h.Col || m.AccentPicker.Row != h.Row {
						t.Fatalf("w=%d: pressing column %d of cell (%d,%d) selected (%d,%d)",
							w, x, h.Col, h.Row, m.AccentPicker.Col, m.AccentPicker.Row)
					}
				case accentHitHue:
					if ok, _ := m.accentPickerPress(x, h.Rect.Y0); !ok {
						t.Fatalf("w=%d: a press at column %d of %v was not routed", w, x, h.Rect)
					}
					if want := accentHueAt(h.Col, p.HueCells); m.AccentPicker.Hue != want {
						t.Fatalf("w=%d: pressing column %d of hue cell %d held %v, want %v",
							w, x, h.Col, m.AccentPicker.Hue, want)
					}
				case accentHitANSI:
					if ok, _ := m.accentPickerPress(x, h.Rect.Y0); !ok {
						t.Fatalf("w=%d: a press at column %d of %v was not routed", w, x, h.Rect)
					}
					if m.AccentPicker.Slot != h.Col {
						t.Fatalf("w=%d: pressing column %d of slot %d selected %d",
							w, x, h.Col, m.AccentPicker.Slot)
					}
				}
			}
			m.OverlayMouseRelease()
		}
	}
}

// TestAccentGridCursorSitsInTheMiddleOfItsSwatch: a three-cell swatch with the
// mark against one edge reads as belonging to the swatch beside it. Checked in
// the frame, at every cell of every layout.
func TestAccentGridCursorSitsInTheMiddleOfItsSwatch(t *testing.T) {
	for _, w := range []int{120, 60, 38} {
		m := accentTestOS(t, w, 30)
		m.OpenAccentPicker("aaaaaaaa1111")
		p := m.accentPlan()

		for col := range p.GridCols {
			for row := range p.GridRows {
				m.AccentPickerCell(col, row)
				lines, _ := accentFrame(t, m)
				var rect overlay.Rect
				for _, h := range m.accentHits {
					if h.Kind == accentHitGrid && h.Col == col && h.Row == row {
						rect = h.Rect
					}
				}
				at := -1
				for x, r := range []rune(lines[rect.Y0]) {
					if r == '◆' && x >= rect.X0 && x < rect.X1 {
						at = x
					}
				}
				if want := rect.X0 + (p.CellWidth-1)/2; at != want {
					t.Fatalf("w=%d: the mark on cell (%d,%d) is in column %d of %v, want %d",
						w, col, row, at, rect, want)
				}
			}
		}
	}
}

// TestAccentSeedLandsOnItsOwnCell: the picker opens on the colour the target is
// wearing, and the cursor opens on the cell nearest it. A coarser grid makes
// that cell coarser; it must not make it wrong.
func TestAccentSeedLandsOnItsOwnCell(t *testing.T) {
	const id = "aaaaaaaa1111"
	for _, hex := range []string{"#3aa0ff", "#801020", "#12ef88", "#cccccc", "#101010"} {
		want, ok := parseHexColor(hex)
		if !ok {
			t.Fatalf("%q is not a colour", hex)
		}
		for _, w := range []int{120, 60, 38} {
			m := accentTestOS(t, w, 30)
			m.SetWindowAccent(id, RGBAccent(want))
			m.OpenAccentPicker(id)

			cols, rows := m.accentGridSize()
			wantHue, wantCol, wantRow := accentCellFor(want, 0, cols, rows)
			s := &m.AccentPicker
			if s.Cur != want {
				t.Errorf("%s w=%d: the picker opened on %s", hex, w, hexString(s.Cur))
			}
			if s.Hue != wantHue || s.Col != wantCol || s.Row != wantRow {
				t.Errorf("%s w=%d: the cursor opened on hue %v (%d,%d), want %v (%d,%d)",
					hex, w, s.Hue, s.Col, s.Row, wantHue, wantCol, wantRow)
			}
		}
	}
}

// TestAccentWideDropsInOrder: on a screen too short for everything the wide
// layout gives up the breathing blanks first and the theme's colours next, and
// never the sliders, which are the reason the column exists.
func TestAccentWideDropsInOrder(t *testing.T) {
	for _, tc := range []struct {
		h                     int
		wantBlanks, wantSlots bool
		wantChipRows          int
	}{
		{40, true, true, 2},
		{16, true, true, 2},
		{15, true, false, 2},
		{14, false, false, 2},
		{12, false, false, 2},
		{11, false, false, 1},
	} {
		m := accentTestOS(t, 100, tc.h)
		p := m.accentPlan()
		if p.Mode != accentLayoutWide {
			t.Fatalf("h=%d: the layout is not wide", tc.h)
		}
		if p.Blanks != tc.wantBlanks || p.Slots != tc.wantSlots {
			t.Errorf("h=%d: blanks=%v slots=%v, want blanks=%v slots=%v",
				tc.h, p.Blanks, p.Slots, tc.wantBlanks, tc.wantSlots)
		}
		if p.HarmonyRows != tc.wantChipRows {
			t.Errorf("h=%d: %d rows of chips, want %d", tc.h, p.HarmonyRows, tc.wantChipRows)
		}
		if !p.Sliders {
			t.Errorf("h=%d: the wide layout dropped its sliders", tc.h)
		}
		if p.GridRows < 1 {
			t.Errorf("h=%d: the grid is %d rows", tc.h, p.GridRows)
		}
	}
}

// TestAccentHueNudgeReachesBetweenTheCells: the strip is a cell every ten
// degrees, so nine hues in ten are not on a cell. The shifted arrow reaches
// them, the cursor stays on the nearest cell, and the hex says which hue is
// actually held.
func TestAccentHueNudgeReachesBetweenTheCells(t *testing.T) {
	m := accentTestOS(t, 120, 30)
	m.OpenAccentPicker("aaaaaaaa1111")
	cells := m.accentHueCells()
	if cells != 36 {
		t.Fatalf("the wide strip is %d cells, want 36 (ten degrees each)", cells)
	}

	m.AccentPickerHueCell(21) // 210 degrees
	m.AccentPicker.Focus = accentFocusHue
	for range 3 {
		m.AccentPickerMoveShift(1, 0)
	}
	if got := m.AccentPicker.Hue; got != 213 {
		t.Errorf("three shifted steps from 210 reached %v, want 213", got)
	}
	if got := accentHueCell(m.AccentPicker.Hue, cells); got != 21 {
		t.Errorf("hue 213 puts the strip cursor on cell %d, want 21", got)
	}

	// The colour is the exact hue, not the cell's, and the dialog prints it.
	s := &m.AccentPicker
	if want := hslToRGB(213, s.Sat, s.Light); s.Cur != want {
		t.Errorf("hue 213 holds %s, want %s", hexString(s.Cur), hexString(want))
	}
	if want := hexString(s.Cur); !strings.Contains(strings.Join(pickerLines(t, m), "\n"), want) {
		t.Errorf("the dialog does not print the nudged colour %s", want)
	}

	// A plain arrow is still a whole cell.
	m.AccentPickerMove(1, 0)
	if got := m.AccentPicker.Hue; got != 220 {
		t.Errorf("a plain arrow from 213 reached %v, want the next cell at 220", got)
	}

	// The circle wraps in both directions.
	m.AccentPickerHueCell(0)
	m.AccentPicker.Focus = accentFocusHue
	m.AccentPickerMoveShift(-1, 0)
	if got := m.AccentPicker.Hue; got != 359 {
		t.Errorf("stepping back off zero reached %v, want 359", got)
	}
	for range 2 {
		m.AccentPickerMoveShift(1, 0)
	}
	if got := m.AccentPicker.Hue; got != 1 {
		t.Errorf("stepping forward over the wrap reached %v, want 1", got)
	}
}

// TestAccentCompactKeepsTheV1Layout: below the stacked floor the picker is the
// layout it shipped with, so the screens that worked before still work.
func TestAccentCompactKeepsTheV1Layout(t *testing.T) {
	m := accentTestOS(t, 38, 24)
	m.OpenAccentPicker("aaaaaaaa1111")
	p := m.accentPlan()
	if p.Sliders {
		t.Error("the compact layout drew sliders it has no width for")
	}
	plain := strings.Join(pickerLines(t, m), "\n")
	for _, want := range []string{"accent", "now", "hex", "comp"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the compact dialog lost %q:\n%s", want, plain)
		}
	}
}
