package vt

import "testing"

// TestImageMarkersPlaceAndErase: both emulators write a picture's marker
// cells at the cursor and blank only the picture asked for, leaving the
// other picture, the text around them and the cursor where they were. A kitty
// delete (a=d) of a picture drawn as glyphs relies on this: a marker cell
// left in the grid holds its id, and the id comes round again.
func TestImageMarkersPlaceAndErase(t *testing.T) {
	term := NewWithScrollback(20, 8, 100)
	defer term.Close()
	_, _ = term.Write([]byte("\x1b[1;1Habc\x1b[2;2H"))
	term.PlaceImageMarkers(2, 3, 5, false)
	if x, y := cursorOf(term); x != 1 || y != 1 {
		t.Fatalf("a placement that does not move the cursor left it at %d,%d", x, y)
	}
	_, _ = term.Write([]byte("\x1b[2;10H"))
	term.PlaceImageMarkers(2, 3, 6, true)
	if x, y := cursorOf(term); x != 0 || y != 3 {
		t.Fatalf("a placement that moves the cursor left it at %d,%d, want 0,3", x, y)
	}
	count := func(id uint32) int {
		n := 0
		for y := range 8 {
			for x := range 20 {
				if c := term.CellAt(x, y); c != nil {
					if got, _, _, ok := ParseSixelMarker(c.Content); ok && got == id {
						n++
					}
				}
			}
		}
		return n
	}
	if count(5) != 6 || count(6) != 6 {
		t.Fatalf("placed %d and %d marker cells, want 6 and 6", count(5), count(6))
	}
	_, _ = term.Write([]byte("\x1b[7;5H"))
	term.EraseImageMarkers(func(id uint32) bool { return id == 5 })
	if count(5) != 0 {
		t.Errorf("%d cells of the erased picture are left", count(5))
	}
	if count(6) != 6 {
		t.Errorf("the other picture has %d cells after the erase, want 6", count(6))
	}
	if c := term.CellAt(0, 0); c == nil || c.Content != "a" {
		t.Errorf("the text beside the pictures changed: %+v", c)
	}
	if x, y := cursorOf(term); x != 4 || y != 6 {
		t.Errorf("the erase moved the cursor to %d,%d, want 4,6", x, y)
	}
}

func cursorOf(term Terminal) (int, int) {
	p := term.CursorPosition()
	return p.X, p.Y
}
