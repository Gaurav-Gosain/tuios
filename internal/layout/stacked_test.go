package layout

import (
	"fmt"
	"testing"
)

// stackedDisjoint checks the tiler contract on one arrangement: every rect
// inside the region, no two overlapping.
func stackedDisjoint(t *testing.T, layouts []TileLayout, w, h int) {
	t.Helper()
	for i, l := range layouts {
		if l.Width <= 0 || l.Height <= 0 {
			t.Errorf("rect %d is %dx%d", i, l.Width, l.Height)
		}
		if l.X < 0 || l.X+l.Width > w || l.Y < 0 || l.Y+l.Height > h {
			t.Errorf("rect %d (%d,%d %dx%d) outside the %dx%d region", i, l.X, l.Y, l.Width, l.Height, w, h)
		}
		for j := i + 1; j < len(layouts); j++ {
			o := layouts[j]
			if l.X < o.X+o.Width && o.X < l.X+l.Width &&
				l.Y < o.Y+o.Height && o.Y < l.Y+l.Height {
				t.Errorf("rects %d (%d,%d %dx%d) and %d (%d,%d %dx%d) overlap",
					i, l.X, l.Y, l.Width, l.Height, j, o.X, o.Y, o.Width, o.Height)
			}
		}
	}
}

func TestStackedLayoutFillsTheRegionWithBarsAndOneFocusedPane(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5, 9} {
		// A focused slot at the top, one in the middle, one at the bottom.
		foci := []int{0, n / 2, n - 1}
		for _, focused := range foci {
			for _, gap := range []int{0, 1, 2} {
				for _, sz := range []struct{ w, h int }{{160, 48}, {80, 24}, {45, 14}} {
					label := fmt.Sprintf("focused=%d/n=%d/gap=%d/%dx%d", focused, n, gap, sz.w, sz.h)
					layouts := CalculateStackedLayout(n, focused, sz.w, sz.h, 0, gap)
					if len(layouts) != n {
						t.Errorf("%s: got %d rects, want %d", label, len(layouts), n)
						continue
					}
					stackedDisjoint(t, layouts, sz.w, sz.h)
					// The focused pane holds the space every bar gave up, so it is
					// the tallest rect whenever the region is roomy enough for the
					// bars to leave it something.
					tallest := 0
					for _, l := range layouts {
						if l.Height > tallest {
							tallest = l.Height
						}
					}
					if layouts[focused].Height != tallest && sz.h >= 3*n {
						t.Errorf("%s: focused pane is %d rows, tallest is %d", label, layouts[focused].Height, tallest)
					}
				}
			}
		}
	}
}

func TestStackedLayoutBarsAreOneRow(t *testing.T) {
	layouts := CalculateStackedLayout(5, 1, 120, 40, 0, 1)
	for i, l := range layouts {
		if i == 1 {
			continue
		}
		if l.Height != 1 {
			t.Errorf("bar %d is %d rows, want 1", i, l.Height)
		}
		if l.Width != 120 {
			t.Errorf("bar %d is %d wide, want the full 120", i, l.Width)
		}
	}
}

func TestStackedLayoutKeepsSlotOrder(t *testing.T) {
	layouts := CalculateStackedLayout(4, 2, 100, 30, 0, 1)
	for i := 1; i < len(layouts); i++ {
		if layouts[i].Y < layouts[i-1].Y+layouts[i-1].Height {
			t.Errorf("rect %d starts at row %d before rect %d ends at %d",
				i, layouts[i].Y, i-1, layouts[i-1].Y+layouts[i-1].Height)
		}
	}
}

// A region too tight for every bar and gap gives the gaps up before it gives
// the focused pane away, so the stack stays legible even under pressure.
func TestStackedLayoutShrinksGapsUnderPressure(t *testing.T) {
	// 6 bars + 5 gaps of 2 = 16 rows; the region is 12.
	layouts := CalculateStackedLayout(7, 3, 80, 12, 0, 2)
	stackedDisjoint(t, layouts, 80, 12)
	adjacent := true
	for i := 1; i < len(layouts); i++ {
		if layouts[i].Y != layouts[i-1].Y+layouts[i-1].Height {
			adjacent = false
		}
	}
	if !adjacent {
		t.Error("a region that cannot hold the gaps still kept them")
	}
}

func TestStackedLayoutOutOfRangefocusDefaultsToTheFirstPane(t *testing.T) {
	a := CalculateStackedLayout(3, -1, 80, 24, 0, 1)
	b := CalculateStackedLayout(3, 0, 80, 24, 0, 1)
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("rect %d differs between focus -1 and focus 0", i)
		}
	}
}

func TestStackedLayoutHonorsTopMargin(t *testing.T) {
	layouts := CalculateStackedLayout(3, 0, 80, 20, 4, 1)
	if layouts[0].Y != 4 {
		t.Errorf("first bar starts at row %d, want the 4-row margin", layouts[0].Y)
	}
	if layouts[2].Y+layouts[2].Height > 24 {
		t.Errorf("stack ends at row %d, past the margin's bottom", layouts[2].Y+layouts[2].Height)
	}
}
