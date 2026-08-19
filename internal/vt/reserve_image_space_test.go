package vt_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Space reserved under an image must come out blank, whatever colour the guest
// had in its pen when it transmitted.
//
// ScrollUp fills the rows it exposes with the pen background, which is what a
// guest that scrolled by printing should get. The reservation is not that: the
// guest emitted a graphics command and no text, and tuios invents the scroll to
// make room. Inheriting the pen there painted every reserved row full width in
// the guest's colour while the image covered only its own columns, so any app
// that transmits with a background set (a shell with a coloured prompt segment,
// a TUI mid-draw) drew a solid block around its image. Reported against
// tuios-web as a bright red rectangle across the top of a pane with the image
// preview inside it at the left edge.
func TestReserveImageSpaceLeavesBlankRows(t *testing.T) {
	const (
		cols     = 20
		rows     = 8
		reserved = 4
	)

	emu := vt.NewEmulator(cols, rows)
	// A guest with a background set that then prints, exactly as a coloured
	// prompt segment does. Filling the screen puts the cursor on the last row,
	// so the reservation has to scroll to make its room.
	if _, err := emu.WriteString("\x1b[41m"); err != nil {
		t.Fatalf("set pen: %v", err)
	}
	for range rows {
		if _, err := emu.WriteString("text\r\n"); err != nil {
			t.Fatalf("print: %v", err)
		}
	}
	if y := emu.CursorPosition().Y; y != rows-1 {
		t.Fatalf("setup: cursor on row %d, want the last row %d", y, rows-1)
	}

	// The pen is still red here, as it is when an image lands mid-draw. The
	// reservation needs (rows-1)+reserved rows and has rows, so it scrolls
	// reserved-1 of them in.
	emu.ReserveImageSpace(reserved, cols)

	for y := rows - (reserved - 1); y < rows; y++ {
		for x := range cols {
			c := emu.CellAt(x, y)
			if c == nil {
				continue
			}
			if c.Style.Bg != nil {
				t.Fatalf("reserved cell (%d,%d) has background %v, want none", x, y, c.Style.Bg)
			}
		}
	}
}

// The fix must not reach the guest's own scrolling: a guest that scrolls by
// printing still gets background-colour erase on the rows it exposes.
func TestPrintingScrollStillPaintsPenBackground(t *testing.T) {
	const (
		cols = 20
		rows = 4
	)

	emu := vt.NewEmulator(cols, rows)
	if _, err := emu.WriteString("\x1b[41m"); err != nil {
		t.Fatalf("set pen: %v", err)
	}
	// Print past the bottom so the guest's own output scrolls the screen.
	for range rows * 2 {
		if _, err := emu.WriteString("x\r\n"); err != nil {
			t.Fatalf("print: %v", err)
		}
	}

	// The column past the printed text on the last written row was exposed by
	// the guest's scroll and must carry the guest's background.
	c := emu.CellAt(cols-1, 0)
	if c == nil || c.Style.Bg == nil {
		t.Fatalf("guest scroll lost its background-colour erase at (%d,0): %#v", cols-1, c)
	}
}

// The same thing from the bytes a guest actually emits: a background left set
// in the pen, then a sixel, which reserves its rows inside the emulator.
func TestSixelReservationLeavesBlankRows(t *testing.T) {
	const (
		cols       = 20
		rows       = 8
		cellW      = 10
		cellH      = 20
		imagePxH   = 120 // six rows at cellH
		imageRows  = imagePxH / cellH
		scrolledIn = imageRows - 1 // the cursor is already on the last row
	)

	emu := vt.NewEmulator(cols, rows)
	emu.SetCellSize(cellW, cellH)

	if _, err := emu.WriteString("\x1b[41m"); err != nil {
		t.Fatalf("set pen: %v", err)
	}
	for range rows {
		if _, err := emu.WriteString("text\r\n"); err != nil {
			t.Fatalf("print: %v", err)
		}
	}

	// A sixel 40px wide and 120px tall, so it needs imageRows rows.
	if _, err := emu.WriteString("\x1bPq\"1;1;40;120#0;2;0;0;100#0~~~~\x1b\\"); err != nil {
		t.Fatalf("sixel: %v", err)
	}

	for y := rows - scrolledIn; y < rows; y++ {
		for x := range cols {
			c := emu.CellAt(x, y)
			if c == nil {
				continue
			}
			if c.Style.Bg != nil {
				t.Fatalf("cell (%d,%d) reserved for the sixel has background %v, want none",
					x, y, c.Style.Bg)
			}
		}
	}
}

// A reservation that fits on screen scrolls nothing, so nothing is repainted
// and the guest's existing rows are left exactly as they were.
func TestReserveImageSpaceWithoutScrollLeavesRowsAlone(t *testing.T) {
	const (
		cols = 20
		rows = 8
	)

	emu := vt.NewEmulator(cols, rows)
	if _, err := emu.WriteString("\x1b[41mtext"); err != nil {
		t.Fatalf("write: %v", err)
	}

	emu.ReserveImageSpace(2, 4)

	c := emu.CellAt(0, 0)
	if c == nil || c.Style.Bg == nil {
		t.Fatalf("a reservation that did not scroll repainted the guest's own row: %#v", c)
	}
}
