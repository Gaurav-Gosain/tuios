package vt

import (
	"encoding/binary"

	uv "github.com/charmbracelet/ultraviolet"
)

// Kitty image cells in an emulator that draws no images.
//
// A kitty terminal moves the cursor past an image it places, unless the
// guest asks it not to (C=1): to the last row of the image, right after its
// last column. An emulator that only mirrors a pane, such as the daemon's,
// draws no pixels and so moved nothing, and its grid ended up rows short of
// the terminal the guest drew on. A client that paints that grid (guios after
// a resize) then drew the text under an image over the image's rows.
//
// ReserveKittyPlacement moves the cursor as kitty does and writes the cells
// the image covers as sixel marker cells under id 0: reserved, drawn blank,
// and carried by the grid as it scrolls and reflows, so a client can find
// where the image now is. See sixel_marker.go.

// KittyPlacementCells returns the rows and columns a placement covers, as a
// kitty terminal works them out: the c= and r= keys, or the image's pixels
// (the source rectangle, else s= and v=, else a PNG's header) over the cell
// size, scaled by the one of c= and r= that is set. It returns 0, 0 when the
// size cannot be known, as for a=p of an image whose upload named no size.
func KittyPlacementCells(cmd *KittyCommand, cellW, cellH int) (rows, cols int) {
	if cmd == nil || cellW <= 0 || cellH <= 0 {
		return 0, 0
	}
	if cmd.Columns > 0 && cmd.Rows > 0 {
		return cmd.Rows, cmd.Columns
	}
	w, h := cmd.Width, cmd.Height
	if (w <= 0 || h <= 0) && cmd.Format == KittyFormatPNG && len(cmd.pngHead) >= 24 {
		// IHDR: width and height, big endian, after the 8-byte signature
		// and the chunk's length and type.
		w, h = int(binary.BigEndian.Uint32(cmd.pngHead[16:])), int(binary.BigEndian.Uint32(cmd.pngHead[20:]))
	}
	if cmd.SourceWidth > 0 {
		w = cmd.SourceWidth
	}
	if cmd.SourceHeight > 0 {
		h = cmd.SourceHeight
	}
	if w <= 0 || h <= 0 || w > 1<<20 || h > 1<<20 {
		return 0, 0
	}
	switch {
	case cmd.Columns > 0:
		// Scaled to c= columns, the height by the aspect ratio.
		w2 := cmd.Columns * cellW
		w, h = w2, h*w2/w
	case cmd.Rows > 0:
		h2 := cmd.Rows * cellH
		w, h = w*h2/h, h2
	}
	xo, yo := min(max(cmd.XOffset, 0), cellW-1), min(max(cmd.YOffset, 0), cellH-1)
	return (h + yo + cellH - 1) / cellH, (w + xo + cellW - 1) / cellW
}

// ReserveKittyPlacement lays out a kitty placement in the grid: the cells it
// covers become reserved image cells, and the cursor moves past it as a kitty
// terminal moves it. It does nothing for a command that places nothing at the
// cursor, one that keeps the cursor (C=1), a virtual placement (U=1), or a
// size it cannot work out. A chunked upload is laid out at its first chunk,
// which carries the keys; the cursor does not move between chunks.
func (e *Emulator) ReserveKittyPlacement(cmd *KittyCommand) {
	if cmd == nil || cmd.CursorMove == 1 || cmd.Virtual ||
		(cmd.Action != KittyActionTransmitPlace && cmd.Action != KittyActionPlace) {
		return
	}
	cellW, cellH := e.CellSize()
	rows, cols := KittyPlacementCells(cmd, cellW, cellH)
	if rows <= 0 || cols <= 0 {
		return
	}
	e.layOutKittyImage(rows, cols, true)
}

// ReserveKittyImageSpace moves the cursor past an image rows by cols cells at
// the cursor, as a kitty terminal does and as ReserveKittyPlacement does,
// without marking the cells. A client that draws the image itself (the TUI's
// passthrough) lays its grid out with it, so its rows agree with the daemon's
// grid, which a reattach or a resize snapshot brings back.
func (e *Emulator) ReserveKittyImageSpace(rows, cols int) {
	if rows <= 0 || cols <= 0 {
		return
	}
	e.layOutKittyImage(rows, cols, false)
}

// layOutKittyImage scrolls so the image's rows fit under the cursor, marks
// its cells when mark is set, and leaves the cursor on the image's last row,
// right after its last column.
func (e *Emulator) layOutKittyImage(rows, cols int, mark bool) {
	startX, startY := e.scr.CursorPosition()
	height, width := e.scr.Height(), e.scr.Width()
	scroll := 0
	if startY+rows > height {
		// Bounded as in ReserveImageSpace: a hostile r= could otherwise
		// drive a very long scroll loop. A picture taller than the screen
		// runs into the scrollback, as in kitty.
		scroll = min(startY+rows-height, maxReserveScroll)
		// A blank pen, as in placeSixel: the guest printed no text.
		e.scr.withBlankPen(func() {
			for range scroll {
				e.scr.ScrollUp(1)
			}
		})
	}
	top := startY - scroll
	if mark {
		// A marker states at most SixelMaxCells rows and columns.
		for r := range min(rows, SixelMaxCells) {
			y := top + r
			if y < 0 || y >= height {
				continue
			}
			for c := range min(cols, SixelMaxCells) {
				x := startX + c
				if x >= width {
					break
				}
				cell := uv.Cell{Content: SixelMarker(0, r, c), Width: 1}
				if old := e.scr.CellAt(x, y); old != nil {
					cell.Style.Bg = old.Style.Bg
				}
				e.scr.SetCell(x, y, &cell)
			}
		}
	}
	// The last row of the image, right after its last column. At the right
	// edge kitty wraps once: column 0 of the next row.
	y := min(max(top+rows-1, 0), height-1)
	x := startX + cols
	if x >= width {
		x = 0
		if y == height-1 {
			e.scr.withBlankPen(func() { e.scr.ScrollUp(1) })
		} else {
			y++
		}
	}
	e.scr.setCursor(x, y, false)
	e.atPhantom = false
}
