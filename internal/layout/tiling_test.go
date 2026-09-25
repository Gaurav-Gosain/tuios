package layout

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestCalculateTilingLayout_TwoWindows tests layout with two windows side by side
func TestCalculateTilingLayout_TwoWindows(t *testing.T) {
	tests := []struct {
		name        string
		masterRatio float64
		expectLeft  int
		expectRight int
	}{
		{"50-50 split", 0.5, 100, 100},
		{"60-40 split", 0.6, 120, 80},
		{"30-70 split", 0.3, 60, 140},
		{"70-30 split", 0.7, 140, 60},
		{"20-80 split", 0.2, 40, 160},
		{"90-10 split", 0.9, 180, 20},
		{"Clamped low", 0.05, 20, 180},  // Should clamp to 0.1
		{"Clamped high", 0.95, 180, 20}, // Should clamp to 0.9
		{"Unset", 0, 100, 100},          // Zero is the default split
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layouts := CalculateTilingLayout(2, 200, 100, 0, tt.masterRatio, 0)

			if len(layouts) != 2 {
				t.Fatalf("Expected 2 layouts, got %d", len(layouts))
			}

			// Check left window
			if layouts[0].X != 0 || layouts[0].Y != 0 {
				t.Errorf("Left window: expected position (0, 0), got (%d, %d)",
					layouts[0].X, layouts[0].Y)
			}
			if layouts[0].Width != tt.expectLeft {
				t.Errorf("Left window: expected width %d, got %d",
					tt.expectLeft, layouts[0].Width)
			}
			if layouts[0].Height != 100 {
				t.Errorf("Left window: expected height 100, got %d", layouts[0].Height)
			}

			// Check right window
			if layouts[1].X != tt.expectLeft {
				t.Errorf("Right window: expected X=%d, got %d",
					tt.expectLeft, layouts[1].X)
			}
			if layouts[1].Width != tt.expectRight {
				t.Errorf("Right window: expected width %d, got %d",
					tt.expectRight, layouts[1].Width)
			}

			// Verify windows cover full width
			totalWidth := layouts[0].Width + layouts[1].Width
			if totalWidth != 200 {
				t.Errorf("Total width should be 200, got %d", totalWidth)
			}
		})
	}
}

// outside reports the rectangles that leave the region, and overlapping reports
// the pairs that sit on top of each other. Between them they are the tiler's
// whole contract.
func outside(layouts []TileLayout, w, h, topMargin int) []TileLayout {
	var out []TileLayout
	for _, r := range layouts {
		if r.Width <= 0 || r.Height <= 0 ||
			r.X < 0 || r.X+r.Width > w ||
			r.Y < topMargin || r.Y+r.Height > topMargin+h {
			out = append(out, r)
		}
	}
	return out
}

func overlapping(layouts []TileLayout) [][2]TileLayout {
	var out [][2]TileLayout
	for a := range layouts {
		for b := a + 1; b < len(layouts); b++ {
			p, q := layouts[a], layouts[b]
			if p.X < q.X+q.Width && q.X < p.X+p.Width &&
				p.Y < q.Y+q.Height && q.Y < p.Y+p.Height {
				out = append(out, [2]TileLayout{p, q})
			}
		}
	}
	return out
}

// TestPanesShrinkRatherThanOverlap is the regression for the fixed pane minimum
// this tiler used to enforce. Every tile was grown to config.DefaultWindowWidth
// by config.DefaultWindowHeight and then shoved back inside the screen, so a
// region that could not give every pane that much drew them on top of each
// other.
//
// The two sizes named here are the ones it was found at: seven panes on a 51x37
// terminal came out with four overlapping pairs, and 45x14 overlapped from five
// panes up. Both are a comfortable half of a laptop screen.
func TestPanesShrinkRatherThanOverlap(t *testing.T) {
	for _, c := range []struct{ n, w, h int }{{7, 51, 35}, {5, 45, 12}} {
		layouts := CalculateTilingLayout(c.n, c.w, c.h, 1, 0.5, 0)
		if len(layouts) != c.n {
			t.Fatalf("%d panes on %dx%d produced %d rectangles", c.n, c.w, c.h, len(layouts))
		}
		for _, pair := range overlapping(layouts) {
			t.Errorf("%d panes on %dx%d: %+v and %+v overlap", c.n, c.w, c.h, pair[0], pair[1])
		}
		for _, r := range outside(layouts, c.w, c.h, 1) {
			t.Errorf("%d panes on %dx%d: %+v is outside the region", c.n, c.w, c.h, r)
		}
		// At least one pane is smaller than the old minimum on at least one
		// axis, which is the whole point: there was no room for it and the
		// tiler used to take it anyway. Without this the two sizes could drift
		// into ones the old minimum happened to fit, and the case above would
		// stop demonstrating anything.
		narrowest, shortest := layouts[0].Width, layouts[0].Height
		for _, r := range layouts {
			narrowest, shortest = min(narrowest, r.Width), min(shortest, r.Height)
		}
		if narrowest >= config.DefaultWindowWidth && shortest >= config.DefaultWindowHeight {
			t.Errorf("%d panes on %dx%d leave the smallest at %dx%d, which the old minimum of %dx%d "+
				"could have satisfied; this size no longer demonstrates anything",
				c.n, c.w, c.h, narrowest, shortest, config.DefaultWindowWidth, config.DefaultWindowHeight)
		}
	}
}

// TestATightRegionGivesUpGroundBeforeItGivesUpTheRegion states the order of
// precedence when the asked-for gaps do not fit. Ground is spacing and a pane
// outside the region is not on screen, so the gaps shrink first.
//
// Without it, nine panes at a gap of two on a region six rows tall put the
// bottom row three rows past the end of it.
func TestATightRegionGivesUpGroundBeforeItGivesUpTheRegion(t *testing.T) {
	for _, c := range []struct{ n, w, h, gap int }{
		{9, 45, 6, 2}, {7, 30, 6, 2}, {9, 45, 8, 2}, {6, 24, 9, 3},
	} {
		layouts := CalculateTilingLayout(c.n, c.w, c.h, 0, 0.5, c.gap)
		for _, pair := range overlapping(layouts) {
			t.Errorf("n=%d %dx%d gap=%d: %+v and %+v overlap", c.n, c.w, c.h, c.gap, pair[0], pair[1])
		}
		for _, r := range outside(layouts, c.w, c.h, 0) {
			t.Errorf("n=%d %dx%d gap=%d: %+v is outside the region", c.n, c.w, c.h, c.gap, r)
		}
	}
}

// TestMasterStackStackRatio checks that the stack ratio splits the two stacked
// panes of the three pane layout, and that an unset ratio keeps the equal
// split the layout always used.
func TestMasterStackStackRatio(t *testing.T) {
	equal := CalculateMasterStackLayout(3, 200, 60, 0, 0.5, 0, 1)
	legacy := CalculateTilingLayout(3, 200, 60, 0, 0.5, 1)
	for i := range equal {
		if equal[i] != legacy[i] {
			t.Errorf("pane %d: unset stack ratio gave %+v, want the equal split %+v", i, equal[i], legacy[i])
		}
	}

	got := CalculateMasterStackLayout(3, 200, 60, 0, 0.5, 0.25, 1)
	// 60 rows less one gap row is 59, and a quarter of that is 14.
	if got[1].Height != 14 {
		t.Errorf("top stacked pane is %d rows, want 14", got[1].Height)
	}
	if got[2].Y != got[1].Y+got[1].Height+1 || got[2].Y+got[2].Height != 60 {
		t.Errorf("bottom stacked pane %+v does not fill the rest below %+v", got[2], got[1])
	}
	if got[1].X != got[2].X || got[1].Width != got[2].Width {
		t.Errorf("stacked panes are not one column: %+v and %+v", got[1], got[2])
	}
}

// TestSplitRatioForRoundTrips checks that the ratio derived from a near side
// reproduces that near side exactly, for every size inside the tiler's range.
func TestSplitRatioForRoundTrips(t *testing.T) {
	for _, gap := range []int{0, 1, 2} {
		for _, total := range []int{37, 80, 121, 240} {
			avail := total - gap
			for near := 1; near < avail; near++ {
				ratio := SplitRatioFor(near, total, gap)
				if ratio < MinSplitRatio || ratio > MaxSplitRatio {
					t.Errorf("total %d gap %d: near %d gave ratio %v outside the clamp", total, gap, near, ratio)
				}
				// Sizes at the very ends of the range are clamped, so only the
				// ones strictly inside it must come back exactly.
				share := (float64(near) + 0.5) / float64(avail)
				if share <= MinSplitRatio || share >= MaxSplitRatio {
					continue
				}
				if got, _ := splitByRatio(0, total, ratio, gap); got.Size != near {
					t.Errorf("total %d gap %d: near %d gave ratio %v, which splits at %d", total, gap, near, ratio, got.Size)
				}
			}
		}
	}
}
