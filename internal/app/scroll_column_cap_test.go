package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// TestScrollingColumnCap is the report: the resize keys (resize_master_grow,
// the > key in the default config) used to top out at nine tenths of the
// screen, written into scrollingResizeColumn as a literal, regardless of
// appearance.scroll_column_max. Someone who set scroll_column_max = 100 got a
// stuck column at 90%, which is the entry the report came from.
//
// The same ceiling shows up in two other places: the column-width cycle, which
// walks preset proportions, and the maximize action, which jumps straight to
// the configured ceiling. The test pins all three against scroll_column_max,
// so the next change to one without the others is caught.
//
// NEGATIVE CONTROL: put the literal nine tenths back in scrollingResizeColumn
// and the first case fails (it stops at 72 cells on an 80-wide strip). Drop
// the filter from CycleWidth and the third case reaches 1.0 even with
// scroll_column_max at 90.
func TestScrollingColumnCap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cap     int // scroll_column_max
		resizeW int // cell width after resize_master_grow storms
		wantCap int // max cells a column may occupy at the strip's width
	}{
		// The default ceiling: the resize keys cannot pass 90% of the strip.
		{name: "default-90", cap: 90, resizeW: 72, wantCap: 72},
		// A raised ceiling: the resize keys reach the full strip width.
		{name: "raised-100", cap: 100, resizeW: 80, wantCap: 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := modeOS(t, LayoutModeScrolling, false, 2, 2, 80, 24)
			m.Settings.ScrollColumnMax = tc.cap
			viewW := m.ScrollingViewWidth()

			sl := m.GetOrCreateScrollingLayout()
			sl.FocusedCol = 0
			// Storm the > key: 100 presses of 4 cells each. The cap, not the
			// count, decides the final width.
			for range 100 {
				m.scrollingResizeColumn(4)
			}
			m.CompleteAllAnimations()

			got := sl.ResolveColumnWidth(0, viewW)
			if got != tc.wantCap {
				t.Fatalf("resize_master_grow stops at %d cells, want %d (the configured ceiling)",
					got, tc.wantCap)
			}
			if got != tc.resizeW {
				t.Fatalf("resize_master_grow leaves the column at %d cells, want %d", got, tc.resizeW)
			}

			// Maximize lands on the same ceiling, never past it.
			m.ScrollingMaximizeColumn()
			m.CompleteAllAnimations()
			maxW := sl.ResolveColumnWidth(0, viewW)
			if maxW != tc.wantCap {
				t.Errorf("ScrollingMaximizeColumn leaves the column at %d cells, want %d", maxW, tc.wantCap)
			}
		})
	}
}

// TestScrollingCycleWidthRespectsTheCeiling pins the half of the cap fix a
// resize cannot see. The 1.0 preset the cycle walks past 0.9 only joins the
// chain when the ceiling is high enough to let it through: at the default
// 90% it resolves to the same width as 0.9 and would force a second press to
// wrap back to 0.333, which is the UX regression the filter prevents.
//
// NEGATIVE CONTROL: drop the `if w > ceiling+0.01 { continue }` from
// CycleWidth and the default case fails (one press from 0.9 lands on 1.0,
// which resolves to 0.9 and does nothing visible, instead of wrapping to
// 0.333 in a single press).
func TestScrollingCycleWidthRespectsTheCeiling(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  int
		// The preset one press from 0.9 lands on: 0.333 (wrap) when the
		// ceiling is 90 and 1.0 is filtered out, 1.0 (the new endpoint)
		// when the ceiling is 100 and 1.0 is in the chain.
		wantAfter09 float64
	}{
		{name: "default-90-skips-1.0", cap: 90, wantAfter09: 0.333},
		{name: "raised-100-uses-1.0", cap: 100, wantAfter09: 1.0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := modeOS(t, LayoutModeScrolling, false, 2, 2, 80, 24)
			m.Settings.ScrollColumnMax = tc.cap
			sl := m.GetOrCreateScrollingLayout()
			sl.FocusedCol = 0
			sl.Columns[0].Proportion = 0.9
			sl.Columns[0].FixedWidth = 0

			m.ScrollingCycleWidth()
			got := sl.Columns[0].Proportion
			if got != tc.wantAfter09 {
				t.Errorf("one press from 0.9 lands on %v, want %v (ceiling=%d)",
					got, tc.wantAfter09, tc.cap)
			}
		})
	}
}

// TestMaximizeColumnClearsFixedWidth pins the half of MaximizeColumn a fresh
// setup cannot see: a column the resize keys pinned to a fixed cell count
// (FixedWidth > 0) keeps that pin when MaximizeColumn is called, because
// resolveWidth prefers FixedWidth over Proportion. MaximizeColumn has to
// clear it, the way CycleWidth does, or the action looks like it did nothing.
//
// NEGATIVE CONTROL: drop `col.FixedWidth = 0` from MaximizeColumn and the
// width stays pinned at the resize key's last value.
func TestMaximizeColumnClearsFixedWidth(t *testing.T) {
	s := layout.NewScrollingLayout()
	s.Columns = []layout.ScrollColumn{
		{WindowIDs: []int{1}, FixedWidth: 30},
	}
	s.FocusedCol = 0
	s.MaxProportion = 0.9

	s.MaximizeColumn()
	if s.Columns[0].FixedWidth != 0 {
		t.Errorf("MaximizeColumn left FixedWidth at %d, want 0 so resolveWidth reads the new Proportion",
			s.Columns[0].FixedWidth)
	}
	if s.Columns[0].Proportion != 0.9 {
		t.Errorf("MaximizeColumn left Proportion at %v, want 0.9", s.Columns[0].Proportion)
	}
}

// TestScrollingColumnCapConstantsCross is the rule behind the cap: the
// default ceiling must leave the next column peeking in at the edge, and the
// floor of scroll_column_max must still be a usable column width.
func TestScrollingColumnCapConstantsCross(t *testing.T) {
	if config.ScrollColumnWidthCeiling <= config.ScrollColumnWidthMax {
		t.Errorf("the ceiling (%d) must be above the default cap (%d) for scroll_column_max to widen it",
			config.ScrollColumnWidthCeiling, config.ScrollColumnWidthMax)
	}
	if config.ScrollColumnWidthMin < 20 {
		t.Errorf("the floor (%d) is below the narrowest shell", config.ScrollColumnWidthMin)
	}
}
