package vt

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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

// TestADroppedPlaceholderStillTakesItsCell is the other half of dropping them
// (issue 292). The application counted every placeholder as one cell and
// writes what comes after the image where that count puts it. A dropped
// placeholder that did not advance the cursor pulled the rest of the row left
// by the width of the image, and its row and column marks, left with no base,
// landed on the character before the image.
//
// Negative control: returning early from handlePrint for a dropped
// placeholder, as it used to, put the B at column 1 and failed this.
func TestADroppedPlaceholderStillTakesItsCell(t *testing.T) {
	term := New(20, 4)
	seq := "A" + placeholderRow(0x0a0b0c, 0, 3) + "B"
	if _, err := term.Write([]byte(seq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cell := term.CellAt(0, 0); cell == nil || cell.Content != "A" {
		t.Errorf("cell 0 is %+q, want a plain A", cell.Content)
	}
	for x := 1; x <= 3; x++ {
		if cell := term.CellAt(x, 0); cell == nil || strings.TrimSpace(cell.Content) != "" {
			t.Errorf("cell %d is %+v, want a blank where the image would be", x, cell)
		}
	}
	if cell := term.CellAt(4, 0); cell == nil || cell.Content != "B" {
		t.Errorf("cell 4 is %+v, want the B that follows the image", cell)
	}
}

// TestASmallHostIDIsAnIndexedColour keeps the id intact on a host that takes
// only 256 colours (issue 292). tuios rounds true colours to that profile when
// it draws, and host id 2 written as RGB 0,0,2 reached the host as index 22.
// Written as index 2 it reaches the host as index 2, which kitty reads as the
// id. A guest that names its image with an index is read the same way.
//
// Negative control: returning color.RGBA from kittyPlaceholderFg for every id
// left the translated foreground a true colour and this failed.
func TestASmallHostIDIsAnIndexedColour(t *testing.T) {
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	term.SetKittyImageIDTranslator(func(g uint32) (uint32, bool) {
		if g == 0x0a0b0c || g == 7 {
			return 2, true
		}
		return 0, false
	})
	seq := placeholderRow(0x0a0b0c, 0, 2) + "\r\n\x1b[38;5;7m" + string(kitty.Placeholder) + string(kitty.Diacritic(1)) + "\x1b[39m"
	if _, err := term.Write([]byte(seq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, at := range [][2]int{{0, 0}, {1, 0}, {0, 1}} {
		cell := term.CellAt(at[0], at[1])
		if cell == nil || !IsKittyPlaceholder(cell.Content) {
			t.Fatalf("cell %v is not a placeholder: %+v", at, cell)
		}
		if got, ok := cell.Style.Fg.(ansi.IndexedColor); !ok || got != 2 {
			t.Errorf("cell %v names its image with %#v, want indexed colour 2", at, cell.Style.Fg)
		}
	}
}

// TestTheHighByteMarkFollowsTheTranslator is kitten icat with
// --unicode-placeholder. icat picks ids with a non-zero high byte and writes
// that byte as a third mark on every cell. The foreground was rewritten to the
// host id and the guest's third mark kept, so the cell named, for host id 1,
// the image 0xF7000001, which the host does not have. The mark now carries the
// host id's high byte, and goes when that byte is zero.
//
// Every cell of the row is checked, because only the first one carries its
// row and column: the ones after it are told theirs from the cell to the left,
// which is stored translated while they are not yet, so comparing colours
// found them different images and left them with no marks.
//
// Negative controls: keeping the guest's third mark failed the mark check,
// and comparing the colours instead of the translated ids failed the row and
// column check on every cell after the first.
func TestTheHighByteMarkFollowsTheTranslator(t *testing.T) {
	const guestID = 0xF7000A0B
	for _, tc := range []struct {
		name     string
		hostID   uint32
		wantHigh int // -1 for no third mark
	}{
		{"a small host id drops the mark", 1, -1},
		{"a wide host id states its own high byte", 0x03000005, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := New(20, 4)
			term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
			term.SetKittyImageIDTranslator(func(g uint32) (uint32, bool) {
				if g == guestID {
					return tc.hostID, true
				}
				return 0, false
			})
			// icat's shape: row, column and high byte on the first cell,
			// and the high byte alone is not allowed without them, so the
			// rest of the row carries none.
			var seq strings.Builder
			seq.WriteString("\x1b[38;2;0;10;11m")
			seq.WriteRune(kitty.Placeholder)
			seq.WriteRune(kitty.Diacritic(0))
			seq.WriteRune(kitty.Diacritic(0))
			seq.WriteRune(kitty.Diacritic(0xF7))
			for range 3 {
				seq.WriteRune(kitty.Placeholder)
			}
			seq.WriteString("\x1b[39m")
			if _, err := term.Write([]byte(seq.String())); err != nil {
				t.Fatal(err)
			}
			for x := range 4 {
				cell := term.CellAt(x, 0)
				if cell == nil || !IsKittyPlaceholder(cell.Content) {
					t.Fatalf("cell %d is not a placeholder: %+v", x, cell)
				}
				if id, _ := kittyPlaceholderID(cell.Content, cell.Style.Fg); id != tc.hostID {
					t.Errorf("cell %d names image %#x, want host id %#x", x, id, tc.hostID)
				}
				high, hasHigh := kittyPlaceholderHighByte(cell.Content)
				if (tc.wantHigh < 0) == hasHigh || (hasHigh && high != tc.wantHigh) {
					t.Errorf("cell %d third mark = %d (present %v), want %d", x, high, hasHigh, tc.wantHigh)
				}
				row, col, hasRow, hasCol := kittyPlaceholderRowCol(cell.Content)
				if !hasRow || !hasCol || row != 0 || col != x {
					t.Errorf("cell %d states (%d, %d) present %v/%v, want (0, %d)", x, row, col, hasRow, hasCol, x)
				}
			}
		})
	}
}

// TestASmallPlacementIDIsAnIndexedColour protects the placement id in the
// underline colour the same way the image id is protected in the foreground.
//
// Negative control: leaving the underline colour alone kept it a true colour
// and this failed.
func TestASmallPlacementIDIsAnIndexedColour(t *testing.T) {
	term := New(20, 4)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	seq := "\x1b[58;2;0;0;9m" + placeholderRow(0x0a0b0c, 0, 2) + "\x1b[59m"
	if _, err := term.Write([]byte(seq)); err != nil {
		t.Fatal(err)
	}
	cell := term.CellAt(1, 0)
	if cell == nil || !IsKittyPlaceholder(cell.Content) {
		t.Fatalf("not a placeholder: %+v", cell)
	}
	if got, ok := cell.Style.UnderlineColor.(ansi.IndexedColor); !ok || got != 9 {
		t.Errorf("placement id colour = %#v, want indexed colour 9", cell.Style.UnderlineColor)
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
// Negative controls: making rewriteKittyPlaceholder keep the cell's own marks
// left every cell after the first with no marks, and
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

// TestACaptureHoldsNoPlaceholders covers capture-pane on a pane that keeps
// placeholder cells: each cell leaves as a space, marks and all, so the text
// keeps its columns and carries no picture fragments.
//
// Negative control: returning s unchanged failed this.
func TestACaptureHoldsNoPlaceholders(t *testing.T) {
	term := New(20, 2)
	term.SetKittyPlaceholderMode(KittyPlaceholdersKeep)
	if _, err := term.Write([]byte("A" + placeholderRow(0x0a0b0c, 0, 3) + "B")); err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{StripKittyPlaceholders(term.String()), StripKittyPlaceholders(term.Render())} {
		if strings.ContainsRune(got, kitty.Placeholder) || strings.ContainsRune(got, kitty.Diacritic(0)) {
			t.Errorf("the capture kept a placeholder: %q", got)
		}
	}
	if line := strings.SplitN(StripKittyPlaceholders(term.String()), "\n", 2)[0]; !strings.HasPrefix(line, "A   B") {
		t.Errorf("the capture reads %q, want A, three blanks, B", line)
	}
}
