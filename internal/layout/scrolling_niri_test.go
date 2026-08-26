package layout

import "testing"

// The scrolling layout is modelled on niri, so the questions worth asking of it
// are the ones a niri user would: does the strip stay put when I step along it,
// and does a column remember which of its windows I was in.

// A column remembers the window it was focused on. Stepping away and back had
// always returned to the top of the stack, because a column had no notion of an
// active window and GetFocusedWindowID answered with WindowIDs[0].
//
// NEGATIVE CONTROL: with GetFocusedWindowID back at WindowIDs[0], the step back
// reports window 1 instead of the window 3 the test focused.
func TestAColumnRemembersTheWindowItWasFocusedOn(t *testing.T) {
	s := NewScrollingLayout()
	s.AddColumn(1)
	s.AddColumn(4)
	// Stack 2 and 3 under 1.
	s.Columns[0].WindowIDs = []int{1, 2, 3}

	if !s.FocusColumnContaining(3) {
		t.Fatal("window 3 must be findable")
	}
	if got := s.GetFocusedWindowID(); got != 3 {
		t.Fatalf("focusing window 3 left the column on window %d", got)
	}

	s.FocusRight()
	if got := s.GetFocusedWindowID(); got != 4 {
		t.Fatalf("stepping right landed on window %d, want 4", got)
	}
	s.FocusLeft()
	if got := s.GetFocusedWindowID(); got != 3 {
		t.Errorf("stepping back landed on window %d, want the 3 the column was left on", got)
	}
}

// Closing a window above the active one keeps the column on the same window;
// closing the active one falls to the window below it, which is where the eye
// already is.
func TestClosingInAStackKeepsTheColumnOnAWindow(t *testing.T) {
	s := NewScrollingLayout()
	s.AddColumn(1)
	s.Columns[0].WindowIDs = []int{1, 2, 3}
	s.Columns[0].Active = 2 // window 3

	s.RemoveWindow(1)
	if got := s.GetFocusedWindowID(); got != 3 {
		t.Errorf("closing the window above left the column on %d, want 3", got)
	}

	s.RemoveWindow(3)
	if got := s.GetFocusedWindowID(); got != 2 {
		t.Errorf("closing the focused window left the column on %d, want 2", got)
	}
}

// Expel moves the window the column is focused on, and focus goes with it.
// It used to expel whichever window was at the bottom of the stack, so the
// action moved a pane the user was not in and could not undo a consume.
//
// NEGATIVE CONTROL: with ExpelWindow back on the last window and no focus move,
// the expelled window is 3 rather than the focused 2, and FocusedCol stays 0.
func TestExpelMovesTheFocusedWindowAndFollowsIt(t *testing.T) {
	s := NewScrollingLayout()
	s.AddColumn(1)
	s.Columns[0].WindowIDs = []int{1, 2, 3}
	s.Columns[0].Active = 1 // window 2

	s.ExpelWindow()
	if len(s.Columns) != 2 {
		t.Fatalf("expected two columns after expel, got %d", len(s.Columns))
	}
	if got := s.Columns[1].WindowIDs; len(got) != 1 || got[0] != 2 {
		t.Errorf("the new column holds %v, want just the focused window 2", got)
	}
	if got := s.Columns[0].WindowIDs; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("the old column holds %v, want [1 3]", got)
	}
	if s.FocusedCol != 1 {
		t.Errorf("focus is on column %d, want the column the window went to", s.FocusedCol)
	}
	if got := s.GetFocusedWindowID(); got != 2 {
		t.Errorf("focus is on window %d, want the 2 that moved", got)
	}
}

// Consume and expel undo each other, which they can only do if both act on the
// focused window.
func TestConsumeAndExpelAreInverses(t *testing.T) {
	s := NewScrollingLayout()
	s.AddColumn(1)
	s.AddColumn(2)
	s.FocusedCol = 0

	s.ConsumeWindow()
	if len(s.Columns) != 1 || len(s.Columns[0].WindowIDs) != 2 {
		t.Fatalf("consume left %d columns", len(s.Columns))
	}
	if got := s.GetFocusedWindowID(); got != 2 {
		t.Errorf("after consume the focus is on window %d, want the 2 that moved", got)
	}

	s.ExpelWindow()
	if len(s.Columns) != 2 {
		t.Fatalf("expel left %d columns", len(s.Columns))
	}
	if got := s.Columns[0].WindowIDs; len(got) != 1 || got[0] != 1 {
		t.Errorf("the first column holds %v, want [1]", got)
	}
	if got := s.Columns[1].WindowIDs; len(got) != 1 || got[0] != 2 {
		t.Errorf("the second column holds %v, want [2]", got)
	}
}

// Stepping along the strip scrolls by the least that shows the column, not by
// enough to centre it. Centring moved the whole strip on every press even when
// the column asked for was already most of the way on screen, which is what
// makes a scrolling layout hard to keep your place in, and it is not what niri
// does unless you ask for it.
//
// NEGATIVE CONTROL: with ScrollToFocusedColumn back to
// ViewportX = colX - (screenWidth-colW)/2, the first step right moves the
// viewport to 25 instead of leaving it at 4.
func TestSteppingAlongTheStripScrollsTheLeastItCan(t *testing.T) {
	s := NewScrollingLayout()
	s.DefaultWidth = 0.5
	for id := 1; id <= 4; id++ {
		s.AddColumn(id)
	}
	// Columns are 50 wide on a 100 wide screen, at x = 0, 50, 100, 150.
	s.FocusedCol = 0
	s.ViewportX = 0

	s.FocusRight() // column 1, x = 50..100
	s.ScrollToFocusedColumn(100)
	// It is already fully visible; only the peek beside it needs room.
	if s.ViewportX != scrollPeek {
		t.Errorf("stepping to an already visible column moved the viewport to %d, want %d",
			s.ViewportX, scrollPeek)
	}

	s.FocusLeft() // back to column 0
	s.ScrollToFocusedColumn(100)
	if s.ViewportX != 0 {
		t.Errorf("stepping back to the first column left the viewport at %d, want 0", s.ViewportX)
	}
}

// The far end of the strip is reachable and stops there: the viewport never
// scrolls past the content in either direction.
func TestTheStripStopsAtItsEnds(t *testing.T) {
	s := NewScrollingLayout()
	s.DefaultWidth = 0.5
	for id := 1; id <= 4; id++ {
		s.AddColumn(id)
	}
	total := s.TotalStripWidth(100)

	s.FocusedCol = 3
	s.ScrollToFocusedColumn(100)
	if s.ViewportX != total-100 {
		t.Errorf("the last column put the viewport at %d, want the end of the strip %d", s.ViewportX, total-100)
	}

	s.FocusedCol = 0
	s.ScrollToFocusedColumn(100)
	if s.ViewportX != 0 {
		t.Errorf("the first column put the viewport at %d, want 0", s.ViewportX)
	}
}

// A retile must not move the strip under a column the user can already see.
// EnsureFocusedVisible runs on every retile and on every click, and its own
// comment has always said so; the code tested "not fully visible" and then
// centred, so clicking a column with one cell off the edge jumped the strip.
//
// NEGATIVE CONTROL: with the old fullyVisible test and the centring, the
// viewport moves from 10 to 25.
func TestARetileLeavesAVisibleColumnWhereItIs(t *testing.T) {
	s := NewScrollingLayout()
	s.DefaultWidth = 0.5
	for id := 1; id <= 4; id++ {
		s.AddColumn(id)
	}
	s.FocusedCol = 1 // x = 50..100
	s.ViewportX = 10 // the column runs to the right edge and one cell past it

	s.EnsureFocusedVisible(100)
	if s.ViewportX != 10 {
		t.Errorf("a partly visible column moved the viewport to %d; it was at 10 and the user can see it",
			s.ViewportX)
	}

	// And the same from the other side, where the column runs off the left
	// edge. This is the case that separates the threshold from the scroll: a
	// least-scroll would still pull the column fully on, so only the "some of it
	// is visible" test keeps the strip still.
	s.ViewportX = 60
	s.EnsureFocusedVisible(100)
	if s.ViewportX != 60 {
		t.Errorf("a column cut off on the left moved the viewport to %d; it was at 60 and the user can see it",
			s.ViewportX)
	}

	// A column with nothing on screen is brought on, by the least it takes.
	s.ViewportX = 0
	s.FocusedCol = 3 // x = 150..200
	s.EnsureFocusedVisible(100)
	if s.ViewportX != 100 {
		t.Errorf("an off-screen column put the viewport at %d, want the 100 that just shows it", s.ViewportX)
	}
}
