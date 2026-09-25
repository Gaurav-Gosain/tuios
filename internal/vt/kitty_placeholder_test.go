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

// TestThePlaceholderIDFollowsTheTranslator covers the one thing a multiplexer
// has to do to this protocol. The cells name the image by the id the guest
// chose; the host knows it by the id tuios allocated, and a cell naming an id
// the host never heard of draws nothing. An image the host was sent under the
// guest's own id, which is what a transmit-only command does, keeps that id.
//
// Negative control: removing the translate call from handleGraphemeWithin left
// the foreground at the guest's id and the translated case failed.
func TestThePlaceholderIDFollowsTheTranslator(t *testing.T) {
	const guestID, hostID = 0x0a0b0c, 0x010203
	for _, tc := range []struct {
		name      string
		translate func(uint32) (uint32, bool)
		want      uint32
	}{
		{"a known id is rewritten to the host's", func(g uint32) (uint32, bool) {
			if g == guestID {
				return hostID, true
			}
			return 0, false
		}, hostID},
		{"an untranslated id stays the guest's", func(uint32) (uint32, bool) { return 0, false }, guestID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := New(20, 4)
			term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
			term.SetKittyImageIDTranslator(tc.translate)
			if _, err := term.Write([]byte(placeholderRow(guestID, 0, 2))); err != nil {
				t.Fatalf("write: %v", err)
			}
			cell := term.CellAt(0, 0)
			if cell == nil {
				t.Fatal("no cell")
			}
			got, ok := kittyPlaceholderID(cell.Content, cell.Style.Fg)
			if !ok || got != tc.want {
				t.Errorf("cell names %#x (ok=%v), want %#x", got, ok, tc.want)
			}
		})
	}
}

// TestKittyPlaceholderID pins the encoding: the low 24 bits are the
// foreground, and an id too wide for a colour carries its top byte in a third
// combining mark. A placeholder drawn in the default or a transparent
// foreground says nothing about which image it belongs to, and guessing would
// put somebody else's picture on screen.
func TestKittyPlaceholderID(t *testing.T) {
	fg := color.RGBA{R: 0x0a, G: 0x0b, B: 0x0c, A: 0xff}
	base := string(kitty.Placeholder)
	for _, tc := range []struct {
		name    string
		content string
		fg      color.Color
		want    uint32
		ok      bool
	}{
		{"the colour alone", base, fg, 0x0a0b0c, true},
		{"the third mark is the top byte", base + string(kitty.Diacritic(1)) + string(kitty.Diacritic(2)) + string(kitty.Diacritic(7)), fg, 0x070a0b0c, true},
		{"no foreground names no image", base, nil, 0, false},
		{"a transparent foreground names no image", base, color.RGBA{}, 0, false},
	} {
		got, ok := kittyPlaceholderID(tc.content, tc.fg)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%s: got %#x (ok=%v), want %#x (ok=%v)", tc.name, got, ok, tc.want, tc.ok)
		}
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
// cell can be clipped away without taking the rest of its row with it. The
// inference must not run on past the end of a row into the next one, or out of
// one image into the one beside it: the colours tell them apart.
//
// Negative controls: removing the kittyPlaceholderSelfDescribing call from
// handleGraphemeWithin left every cell after the first with no marks, and
// putting back the early return on U+10EEEE in handlePrint left every cell
// blank. This fails on both.
func TestEveryPlaceholderCellStandsOnItsOwn(t *testing.T) {
	type rc struct{ row, col int }
	for _, tc := range []struct {
		name string
		seq  string
		want [][]rc // per screen row, the (row, col) each cell states
	}{
		{
			name: "one row",
			seq:  placeholderRow(0x0a0b0c, 2, 5),
			want: [][]rc{{{2, 0}, {2, 1}, {2, 2}, {2, 3}, {2, 4}}},
		},
		{
			name: "a second row restarts its columns",
			seq:  placeholderRow(0x0a0b0c, 0, 3) + "\r\n" + placeholderRow(0x0a0b0c, 1, 3),
			want: [][]rc{{{0, 0}, {0, 1}, {0, 2}}, {{1, 0}, {1, 1}, {1, 2}}},
		},
		{
			name: "two images side by side do not bleed",
			seq:  placeholderRow(0x0a0b0c, 0, 3) + placeholderRow(0x040506, 0, 3),
			want: [][]rc{{{0, 0}, {0, 1}, {0, 2}, {0, 0}, {0, 1}, {0, 2}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := New(20, 4)
			term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
			if _, err := term.Write([]byte(tc.seq)); err != nil {
				t.Fatalf("write: %v", err)
			}
			for y, cells := range tc.want {
				for x, want := range cells {
					cell := term.CellAt(x, y)
					if cell == nil {
						t.Fatalf("cell (%d,%d) is missing", x, y)
					}
					if cell.Width != 1 {
						t.Errorf("cell (%d,%d) width = %d, want 1", x, y, cell.Width)
					}
					row, col, hasRow, hasCol := kittyPlaceholderRowCol(cell.Content)
					if !hasRow || !hasCol || row != want.row || col != want.col {
						t.Errorf("cell (%d,%d) says (row %d, col %d, stated %v/%v), want (%d, %d)",
							x, y, row, col, hasRow, hasCol, want.row, want.col)
					}
				}
			}
		})
	}
}
