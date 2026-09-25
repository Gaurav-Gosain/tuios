package vt

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"
)

// placeholderRow is what an application using Unicode placeholders prints for
// one row of an image: the id in the foreground, the row index as a combining
// mark on the first cell, and the rest of the row continuing it.
func placeholderRow(id uint32, row, cols int) string {
	var b strings.Builder
	b.WriteString("\x1b[38;2;")
	b.WriteString(decStr(int((id >> 16) & 0xff)))
	b.WriteByte(';')
	b.WriteString(decStr(int((id >> 8) & 0xff)))
	b.WriteByte(';')
	b.WriteString(decStr(int(id & 0xff)))
	b.WriteByte('m')
	b.WriteRune(kitty.Placeholder)
	b.WriteRune(kitty.Diacritic(row))
	for range cols - 1 {
		b.WriteRune(kitty.Placeholder)
	}
	b.WriteString("\x1b[39m")
	return b.String()
}

func decStr(n int) string {
	if n == 0 {
		return "0"
	}
	var d [4]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}

// TestThePlaceholderIDIsRewrittenToTheHostID covers the one thing a
// multiplexer has to do to this protocol. The cells name the image by the id
// the guest chose; the host knows it by the id tuios allocated, and a cell
// naming an id the host never heard of draws nothing.
//
// Negative control: removing the translate call from handleGraphemeWithin left
// the foreground at the guest's id and this failed.
func TestThePlaceholderIDIsRewrittenToTheHostID(t *testing.T) {
	const guestID, hostID = 0x0a0b0c, 0x010203
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	term.SetKittyImageIDTranslator(func(g uint32) (uint32, bool) {
		if g == guestID {
			return hostID, true
		}
		return 0, false
	})
	if _, err := term.Write([]byte(placeholderRow(guestID, 0, 2))); err != nil {
		t.Fatalf("write: %v", err)
	}
	cell := term.CellAt(0, 0)
	if cell == nil {
		t.Fatal("no cell")
	}
	got, ok := kittyPlaceholderID(cell.Content, cell.Style.Fg)
	if !ok {
		t.Fatalf("the cell names no image, fg = %v", cell.Style.Fg)
	}
	if got != hostID {
		t.Errorf("cell names image %#x, want the host's %#x", got, hostID)
	}
}

// TestAnUntranslatedPlaceholderKeepsTheGuestID is the other half. An image the
// host was sent under the guest's own id, which is what a transmit-only
// command does, must keep that id.
func TestAnUntranslatedPlaceholderKeepsTheGuestID(t *testing.T) {
	const guestID = 0x0a0b0c
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	term.SetKittyImageIDTranslator(func(uint32) (uint32, bool) { return 0, false })
	if _, err := term.Write([]byte(placeholderRow(guestID, 0, 2))); err != nil {
		t.Fatalf("write: %v", err)
	}
	cell := term.CellAt(0, 0)
	got, ok := kittyPlaceholderID(cell.Content, cell.Style.Fg)
	if !ok || got != guestID {
		t.Errorf("cell names %#x (ok=%v), want the guest's %#x", got, ok, guestID)
	}
}

// TestPlaceholderCellsSurviveRendering checks the cells come back out. The
// emulator's own Render is the fast path an unfocused pane takes, and an image
// that only worked on the focused pane would be a strange bug to chase.
func TestPlaceholderCellsSurviveRendering(t *testing.T) {
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	if _, err := term.Write([]byte(placeholderRow(0x0a0b0c, 0, 3))); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := term.Render()
	if strings.Count(out, string(kitty.Placeholder)) != 3 {
		t.Errorf("Render() carried %d placeholder cells, want 3:\n%q",
			strings.Count(out, string(kitty.Placeholder)), out)
	}
	if !strings.Contains(out, "38;2;10;11;12") {
		t.Errorf("Render() lost the foreground that names the image:\n%q", out)
	}
}

// TestTheIDIsReadFromTheColourAndTheThirdMark pins the encoding: the low 24
// bits are the foreground, and an id too wide for a colour carries its top
// byte in a third combining mark.
func TestTheIDIsReadFromTheColourAndTheThirdMark(t *testing.T) {
	fg := color.RGBA{R: 0x0a, G: 0x0b, B: 0x0c, A: 0xff}

	got, ok := kittyPlaceholderID(string(kitty.Placeholder), fg)
	if !ok || got != 0x0a0b0c {
		t.Errorf("colour alone gave %#x (ok=%v), want 0x0a0b0c", got, ok)
	}

	wide := string(kitty.Placeholder) + string(kitty.Diacritic(1)) + string(kitty.Diacritic(2)) + string(kitty.Diacritic(7))
	got, ok = kittyPlaceholderID(wide, fg)
	if !ok || got != 0x070a0b0c {
		t.Errorf("third mark gave %#x (ok=%v), want 0x070a0b0c", got, ok)
	}
}

// TestACellWithNoColourNamesNoImage keeps the guard that stops tuios guessing.
// A placeholder drawn in the default foreground says nothing about which image
// it belongs to, and picking one would put somebody else's picture on screen.
func TestACellWithNoColourNamesNoImage(t *testing.T) {
	if _, ok := kittyPlaceholderID(string(kitty.Placeholder), nil); ok {
		t.Error("a cell with no foreground claimed to name an image")
	}
	if _, ok := kittyPlaceholderID(string(kitty.Placeholder), color.RGBA{}); ok {
		t.Error("a fully transparent foreground claimed to name an image")
	}
}

// TestIsKittyPlaceholderLooksAtTheBase makes sure ordinary text with a
// combining mark is never mistaken for an image cell.
func TestIsKittyPlaceholderLooksAtTheBase(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{string(kitty.Placeholder), true},
		{string(kitty.Placeholder) + string(kitty.Diacritic(3)), true},
		{"a", false},
		{"e" + string(kitty.Diacritic(0)), false},
		{"", false},
		{" ", false},
	} {
		if got := IsKittyPlaceholder(tc.in); got != tc.want {
			t.Errorf("IsKittyPlaceholder(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestPlaceholdersAreDroppedByDefault is the safety property. A host that
// cannot draw these renders them as missing-glyph boxes where the picture
// should be, which is worse than the blank space the application made room
// for, so keeping them is opt in.
//
// Negative control: making KittyPlaceholdersKeep the zero value left the cells
// in the grid and this failed.
func TestPlaceholdersAreDroppedByDefault(t *testing.T) {
	term := New(20, 4)
	if _, err := term.Write([]byte(placeholderRow(0x0a0b0c, 0, 3))); err != nil {
		t.Fatalf("write: %v", err)
	}
	for x := range 3 {
		if cell := term.CellAt(x, 0); cell != nil && IsKittyPlaceholder(cell.Content) {
			t.Fatalf("cell %d kept a placeholder without being asked to", x)
		}
	}
	if strings.Contains(term.Render(), string(kitty.Placeholder)) {
		t.Error("a placeholder reached the host from a terminal set to drop them")
	}
}

// TestEveryPlaceholderCellStandsOnItsOwn is the fix for the case kitty's
// specification says the protocol does not handle: "this will not work for
// horizontal scrolling and overlapping images".
//
// An application writes the row on the first cell of a row and leaves the rest
// to be inferred from the cell to the left. A multiplexer takes the left of
// rows away all the time, by clipping a pane at the screen edge or by drawing
// a window over the left half of an image, and the survivors then have nothing
// to inherit from. Filling the marks in here, while the row is whole, means any
// cell can be clipped away without taking the rest of its row with it.
//
// Negative control: removing the kittyPlaceholderSelfDescribing call from
// handleGraphemeWithin left every cell after the first with no marks and this
// failed.
func TestEveryPlaceholderCellStandsOnItsOwn(t *testing.T) {
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	if _, err := term.Write([]byte(placeholderRow(0x0a0b0c, 2, 5))); err != nil {
		t.Fatalf("write: %v", err)
	}
	for x := range 5 {
		cell := term.CellAt(x, 0)
		if cell == nil {
			t.Fatalf("cell %d is missing", x)
		}
		row, col, hasRow, hasCol := kittyPlaceholderRowCol(cell.Content)
		if !hasRow || !hasCol {
			t.Errorf("cell %d states row=%v col=%v, want both so it can be clipped alone", x, hasRow, hasCol)
			continue
		}
		if row != 2 || col != x {
			t.Errorf("cell %d says (row %d, col %d), want (2, %d)", x, row, col, x)
		}
	}
}

// TestASecondRowRestartsItsColumns checks the inference does not run on past
// the end of a row into the next one.
func TestASecondRowRestartsItsColumns(t *testing.T) {
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	seq := placeholderRow(0x0a0b0c, 0, 3) + "\r\n" + placeholderRow(0x0a0b0c, 1, 3)
	if _, err := term.Write([]byte(seq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	for y := range 2 {
		for x := range 3 {
			cell := term.CellAt(x, y)
			row, col, hasRow, hasCol := kittyPlaceholderRowCol(cell.Content)
			if !hasRow || !hasCol || row != y || col != x {
				t.Errorf("cell (%d,%d) says (row %d, col %d, stated %v/%v), want (%d, %d)",
					x, y, row, col, hasRow, hasCol, y, x)
			}
		}
	}
}

// TestTwoImagesSideBySideDoNotBleed keeps the inference from walking out of one
// image into the next. The colours are what tell them apart.
func TestTwoImagesSideBySideDoNotBleed(t *testing.T) {
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	seq := placeholderRow(0x0a0b0c, 0, 3) + placeholderRow(0x040506, 0, 3)
	if _, err := term.Write([]byte(seq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The second image starts its own columns at zero rather than continuing
	// the first image's run.
	cell := term.CellAt(3, 0)
	row, col, _, _ := kittyPlaceholderRowCol(cell.Content)
	if row != 0 || col != 0 {
		t.Errorf("the second image's first cell says (row %d, col %d), want (0, 0)", row, col)
	}
}
