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

// TestAPlaceholderCellReachesTheGrid is the regression test for images in a
// pager. tuios used to drop U+10EEEE at print time, so the cells that say
// where an image goes never existed and nothing was drawn.
//
// Negative control: putting the `if r == kittyPlaceholderChar { return }` back
// at the top of handlePrint left every cell blank and this failed.
func TestAPlaceholderCellReachesTheGrid(t *testing.T) {
	term := New(20, 4)
	if _, err := term.Write([]byte(placeholderRow(0x0a0b0c, 0, 3))); err != nil {
		t.Fatalf("write: %v", err)
	}
	for x := range 3 {
		cell := term.CellAt(x, 0)
		if cell == nil {
			t.Fatalf("cell %d is missing", x)
		}
		if !IsKittyPlaceholder(cell.Content) {
			t.Errorf("cell %d content = %q, want a placeholder", x, cell.Content)
		}
		if cell.Width != 1 {
			t.Errorf("cell %d width = %d, want 1", x, cell.Width)
		}
	}
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
