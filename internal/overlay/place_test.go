package overlay

import "testing"

// TestPlace pins the rule in Place's doc comment, one row per edge case
// there. It is a unit test because most of those cases cannot be reached
// from the outside: an anchor off the screen comes from a view of a larger
// session, a box larger than the screen from a terminal a few rows tall, an
// empty box from a list with nothing in it. Every popup in tuios goes through
// Place, so a wrong answer here is a wrong answer at every site at once.
func TestPlace(t *testing.T) {
	screen := Rect{X0: 0, Y0: 0, X1: 80, Y1: 24}
	below := Placement{Side: SideBelow}
	menu := Placement{Side: SideBelow, Gap: -1, FlipAlign: true}
	for _, tc := range []struct {
		name   string
		anchor Rect
		w, h   int
		screen Rect
		p      Placement
		x, y   int
		side   Side
	}{
		{"fits on the side asked for", PointRect(10, 5), 20, 6, screen, below, 10, 6, SideBelow},
		{"gap leaves cells between", PointRect(10, 5), 20, 6, screen, Placement{Side: SideBelow, Gap: 2}, 10, 8, SideBelow},
		{"fits only across flips", PointRect(10, 20), 20, 6, screen, below, 10, 14, SideAbove},
		{"above flips below at the top", PointRect(10, 0), 20, 1, screen, Placement{Side: SideAbove}, 10, 1, SideBelow},
		{"right flips left at the edge", Rect{X0: 60, Y0: 3, X1: 70, Y1: 4}, 15, 4, screen, Placement{Side: SideRight}, 45, 3, SideLeft},
		{"left flips right at the edge", Rect{X0: 2, Y0: 3, X1: 10, Y1: 4}, 15, 4, screen, Placement{Side: SideLeft}, 10, 3, SideRight},
		{"neither side fits: the roomier, shifted", PointRect(10, 15), 20, 18, screen, below, 10, 0, SideAbove},
		{"neither side fits, equal room: keeps the side", PointRect(10, 11), 20, 13, Rect{X1: 80, Y1: 23}, below, 10, 10, SideBelow},
		{"taller than the screen starts at its top", PointRect(10, 5), 20, 40, screen, below, 10, 0, SideBelow},
		{"wider than the screen starts at its left", PointRect(10, 5), 100, 3, screen, below, 0, 6, SideBelow},
		{"anchor wider than the screen", Rect{X0: -10, Y0: 5, X1: 100, Y1: 6}, 30, 3, screen, below, 0, 6, SideBelow},
		{"zero size sits at the anchor's edge", PointRect(10, 5), 0, 0, screen, below, 10, 6, SideBelow},
		{"negative size is zero", PointRect(10, 5), -3, -1, screen, below, 10, 6, SideBelow},
		{"anchor off the right edge", PointRect(95, 5), 20, 3, screen, below, 60, 6, SideBelow},
		{"anchor above the screen", PointRect(10, -8), 20, 3, screen, below, 10, 0, SideBelow},
		{"a rail narrows the screen", PointRect(5, 5), 20, 3, Rect{X0: 24, Y0: 0, X1: 80, Y1: 23}, below, 24, 6, SideBelow},
		{"a dock narrows the screen", PointRect(10, 21), 20, 3, Rect{X0: 0, Y0: 0, X1: 80, Y1: 23}, below, 10, 18, SideAbove},
		{"empty screen", PointRect(10, 5), 20, 3, Rect{X0: 4, Y0: 7, X1: 4, Y1: 7}, below, 4, 7, SideBelow},
		{"align end", Rect{X0: 30, Y0: 5, X1: 40, Y1: 6}, 20, 3, screen, Placement{Side: SideBelow, Align: AlignEnd}, 20, 6, SideBelow},
		{"align center", Rect{X0: 30, Y0: 5, X1: 40, Y1: 6}, 20, 3, screen, Placement{Side: SideBelow, Align: AlignCenter}, 25, 6, SideBelow},
		{"start past the edge shifts", PointRect(70, 5), 20, 3, screen, below, 60, 6, SideBelow},
		{"a menu covers the pointer cell", PointRect(10, 5), 20, 6, screen, menu, 10, 5, SideBelow},
		{"a menu at the right edge flips its corner", PointRect(79, 5), 20, 6, screen, menu, 60, 5, SideBelow},
		{"a menu at the bottom right corner", PointRect(79, 23), 20, 6, screen, menu, 60, 18, SideAbove},
		{"a menu wider than the screen", PointRect(79, 5), 90, 6, screen, menu, 0, 5, SideBelow},
		{"right side aligns rows", Rect{X0: 0, Y0: 22, X1: 10, Y1: 23}, 15, 4, screen, Placement{Side: SideRight}, 10, 20, SideRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, y, side := Place(tc.anchor, tc.w, tc.h, tc.screen, tc.p)
			if x != tc.x || y != tc.y || side != tc.side {
				t.Errorf("Place = (%d, %d, %d), want (%d, %d, %d)", x, y, side, tc.x, tc.y, tc.side)
			}
		})
	}
}
