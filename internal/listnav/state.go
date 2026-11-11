package listnav

import "github.com/Gaurav-Gosain/tuios/internal/textedit"

// State is a list a person moves in: the filter they typed, the row the
// cursor is on and the first row shown. Rows are indexes into the list as
// filtered. Every picker holds one, so the rules below are kept in one place:
//
//   - A change to the query puts the cursor and the window back at the top
//     (Refiltered), since the row the cursor was on may be gone.
//   - A key moves the cursor by Step or StepSkip under the caller's wrap rule;
//     the wheel moves it one row and never wraps.
//   - After every move the window follows the cursor by Scroll, as little as
//     it can.
//   - Fit clamps both to the list as it is drawn, after the rows changed.
type State struct {
	// Query is the filter line. A list with no filter leaves it empty.
	Query textedit.Line
	// Cursor is the row the cursor is on, and Scroll the first row shown.
	Cursor, Scroll int
}

// Move moves the cursor delta rows in a list of n with visible rows shown,
// wrapping a single step when wrap is set and stepping over the rows rest
// rejects (nil: every row holds the cursor). It reports whether the cursor
// moved.
func (s *State) Move(delta, n, visible int, wrap bool, rest func(int) bool) bool {
	if n <= 0 {
		s.Cursor, s.Scroll = 0, 0
		return false
	}
	before := s.Cursor
	s.Cursor = StepSkip(s.Cursor, delta, n, wrap, rest)
	s.Scroll = Scroll(s.Scroll, s.Cursor, n, visible)
	return s.Cursor != before
}

// Wheel moves the cursor one row for a turn of the mouse wheel. A wheel stops
// at the ends of a list: a wrap there would throw the eye from one end to the
// other in the middle of a scroll.
func (s *State) Wheel(up bool, n, visible int, rest func(int) bool) bool {
	delta := 1
	if up {
		delta = -1
	}
	return s.Move(delta, n, visible, false, rest)
}

// Select puts the cursor on row i, as a click or a hover does, and keeps it in
// view. A row out of range is ignored.
func (s *State) Select(i, n, visible int) bool {
	if i < 0 || i >= n || i == s.Cursor {
		return false
	}
	s.Cursor = i
	s.Scroll = Scroll(s.Scroll, s.Cursor, n, visible)
	return true
}

// Fit clamps the cursor and the window to a list of n with visible rows shown,
// for the draw after the rows changed under them.
func (s *State) Fit(n, visible int) {
	if n <= 0 {
		s.Cursor, s.Scroll = 0, 0
		return
	}
	s.Cursor = min(max(s.Cursor, 0), n-1)
	s.Scroll = Scroll(s.Scroll, s.Cursor, n, visible)
}

// Refiltered puts the cursor on row first and the window at the top, after
// the query changed what the list holds. Most lists pass 0; a list whose first
// rows cannot be chosen passes the first one that can.
func (s *State) Refiltered(first int) {
	s.Cursor, s.Scroll = max(first, 0), 0
}

// Reset empties the query and puts the cursor and the window at the top, for
// a list that opens again.
func (s *State) Reset() {
	s.Query.Set("")
	s.Cursor, s.Scroll = 0, 0
}
