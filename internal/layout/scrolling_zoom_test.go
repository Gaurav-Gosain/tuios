package layout

import "testing"

// The strip is already a camera: it is wider than the screen and shows what it
// cannot fit at the edges. So a zoom here is one column widened past the cap
// the others are held to, rather than a second camera over the first.

// stripOf is a layout with n columns at the default width.
func stripOf(n int) *ScrollingLayout {
	s := NewScrollingLayout()
	for i := range n {
		s.AddColumn(i + 1)
	}
	return s
}

// TestTheCapIsTheSettingAndNotALiteral is the bug behind the report that a
// column could not be made to fill the screen.
//
// The cap was nine tenths of the screen, written as a literal in the width
// resolver. appearance.scroll_column_max was added for exactly this and reached
// the settings row, the config file and the stepper, and then the layout went
// on clamping to its own number regardless, so the setting appeared to work and
// changed nothing on screen.
func TestTheCapIsTheSettingAndNotALiteral(t *testing.T) {
	const screen = 200
	s := stripOf(3)
	s.Columns[0].Proportion = 1

	if got := s.ResolveColumnWidth(0, screen); got != screen*9/10 {
		t.Fatalf("with no cap set the column resolved to %d, want the default %d", got, screen*9/10)
	}

	s.MaxProportion = 1
	if got := s.ResolveColumnWidth(0, screen); got != screen {
		t.Errorf("with the cap at 100 percent the column resolved to %d, want the whole screen %d", got, screen)
	}

	s.MaxProportion = 0.5
	if got := s.ResolveColumnWidth(0, screen); got != screen/2 {
		t.Errorf("with the cap at half the column resolved to %d, want %d", got, screen/2)
	}
}

// TestAZeroCapReadsAsTheDefault pins that a layout built by hand, which carries
// a zero here, keeps the behaviour it always had rather than collapsing every
// column to the floor.
func TestAZeroCapReadsAsTheDefault(t *testing.T) {
	const screen = 200
	s := stripOf(2)
	s.Columns[0].Proportion = 1
	if got := s.ResolveColumnWidth(0, screen); got != screen*9/10 {
		t.Errorf("a zero cap resolved the column to %d, want the default %d", got, screen*9/10)
	}
}

// TestTheZoomedColumnTakesItsShare pins the zoom: the column holding the zoomed
// pane is given its share of the screen, past the cap the others are held to.
func TestTheZoomedColumnTakesItsShare(t *testing.T) {
	const screen = 200
	s := stripOf(3)
	s.ZoomedCol = 1
	s.ZoomProportion = 0.9

	if got := s.ResolveColumnWidth(1, screen); got != screen*9/10 {
		t.Errorf("the zoomed column resolved to %d, want %d", got, screen*9/10)
	}
	// The others are untouched, which is what leaves them running off the edges
	// for the zoomed one to peek past.
	other := s.ResolveColumnWidth(0, screen)
	if other == s.ResolveColumnWidth(1, screen) {
		t.Error("the columns beside the zoom were widened with it")
	}
	if want := int(float64(screen) * s.DefaultWidth); other != want {
		t.Errorf("a column beside the zoom resolved to %d, want its own %d", other, want)
	}
}

// TestAZoomedColumnMayFillTheScreen pins that the zoom is not held to the
// column cap: the cap is about how much of the screen a column takes when you
// are working across several, and a zoom is the statement that you are not.
func TestAZoomedColumnMayFillTheScreen(t *testing.T) {
	const screen = 200
	s := stripOf(3)
	s.MaxProportion = 0.9
	s.ZoomedCol = 0
	s.ZoomProportion = 1

	if got := s.ResolveColumnWidth(0, screen); got != screen {
		t.Errorf("a zoom of the whole screen resolved to %d, want %d: it was held to the column cap",
			got, screen)
	}
}

// TestTheStripStaysLongerThanTheScreenUnderAZoom pins what makes the peek work:
// widening one column does not fold the others away, so the strip still runs
// off both edges and the neighbours are there to see.
func TestTheStripStaysLongerThanTheScreenUnderAZoom(t *testing.T) {
	const screen = 200
	s := stripOf(3)
	s.ZoomedCol = 1
	s.ZoomProportion = 0.9

	if total := s.TotalStripWidth(screen); total <= screen {
		t.Errorf("the strip is %d cells in a %d cell screen, so nothing runs off the edges", total, screen)
	}
}

// TestNoZoomedColumnChangesNothing pins the default. A layout with no zoom on
// it resolves every column exactly as it did before any of this.
func TestNoZoomedColumnChangesNothing(t *testing.T) {
	const screen = 200
	s := stripOf(3)
	s.ZoomedCol = -1

	for i := range s.Columns {
		want := int(float64(screen) * s.DefaultWidth)
		if got := s.ResolveColumnWidth(i, screen); got != want {
			t.Errorf("column %d resolved to %d, want %d", i, got, want)
		}
	}
}
