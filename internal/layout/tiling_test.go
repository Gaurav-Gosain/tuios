package layout

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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
