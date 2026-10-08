package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// eraseCharacter erases n characters starting from the cursor position. It
// does not move the cursor. This is equivalent to [ansi.ECH].
func (e *Emulator) eraseCharacter(n int) {
	if n <= 0 {
		n = 1
	}
	x, y := e.scr.CursorPosition()
	// Clamp to the cells left on the line. ECH cannot erase past the right
	// margin, and an unclamped count from the guest (ESC[999999999X) would
	// otherwise drive FillArea through a billion out-of-bounds cells while
	// holding the window IO lock, freezing the pane.
	if rem := e.scr.Width() - x; n > rem {
		n = rem
	}
	if n <= 0 {
		e.atPhantom = false
		return
	}
	rect := uv.Rect(x, y, n, 1)
	e.scr.FillArea(e.scr.blankCell(), rect)
	// Erasing any of the row ends it where it wraps, as ghostty has it: the
	// row's wrap flag goes whether or not the erase reached the last column.
	e.scr.buf.setSoftWrapped(y, false)
	e.atPhantom = false
	// ECH does not move the cursor.
}

// markPrinted gives n cells of row y from column x the pen's protection.
// SetCell has already left them unprotected.
func (e *Emulator) markPrinted(x, y, n int) {
	if e.scr.cur.Protected {
		e.scr.buf.setProtected(x, y, max(n, 1), true)
	}
}

// eraseFill is what an erase blanks an area with: FillArea, or with
// selective set the same fill skipping every cell DECSCA protected.
func (e *Emulator) eraseFill(selective bool) func(c *uv.Cell, area uv.Rectangle) {
	if !selective {
		return func(c *uv.Cell, area uv.Rectangle) { e.scr.FillArea(c, area) }
	}
	return e.fillUnprotected
}

// fillUnprotected is FillArea over the cells of area DECSCA did not protect.
// Each unprotected run along a row is filled as an area of its own.
func (e *Emulator) fillUnprotected(c *uv.Cell, area uv.Rectangle) {
	buf := e.scr.buf
	if buf.prot == nil {
		e.scr.FillArea(c, area)
		return
	}
	area = area.Intersect(buf.Bounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; {
			if buf.Protected(x, y) {
				x++
				continue
			}
			start := x
			for x < area.Max.X && !buf.Protected(x, y) {
				x++
			}
			e.scr.FillArea(c, uv.Rect(start, y, x-start, 1))
		}
	}
}

// eraseDisplay is ED, and with selective set DECSED. fill blanks an area.
func (e *Emulator) eraseDisplay(params ansi.Params, selective bool, fill func(*uv.Cell, uv.Rectangle)) bool {
	n, _, _ := params.Param(0, 0)
	width, height := e.Width(), e.Height()
	x, y := e.scr.CursorPosition()
	switch n {
	case 0: // Erase screen below (from after cursor position)
		rect1 := uv.Rect(x, y, width, 1)            // cursor to end of line
		rect2 := uv.Rect(0, y+1, width, height-y-1) // next line onwards
		fill(e.scr.blankCell(), rect1)
		fill(e.scr.blankCell(), rect2)
		// Don't clear images for ED 0: commonly used by apps
		// But clear text sizing placements if clearing from top (ctrl+l pattern: CUP(1,1) + ED 0)
		if x == 0 && y == 0 && e.cb.ScreenClear != nil {
			e.cb.ScreenClear()
		}
	case 1: // Erase screen above (including cursor)
		// The cursor's own row is erased only as far as the cursor, the
		// way EL 1 does it. Clearing the whole row instead takes out text
		// to the right of the cursor that the guest still expects to be
		// there, which shows up as the top of a redrawn screen losing its
		// last line.
		if y > 0 {
			fill(e.scr.blankCell(), uv.Rect(0, 0, width, y))
		}
		fill(e.scr.blankCell(), uv.Rect(0, y, min(x+1, width), 1))
		// Don't clear images for ED 1: commonly used by apps
	case 2: // erase screen (clear command)
		if selective {
			fill(nil, e.scr.Bounds())
		} else {
			e.scr.Clear()
		}
		e.KittyState().ClearPlacements()
		// Drop on-screen semantic markers so stale prompt/command markers
		// don't cause output extraction to read overwritten cells.
		if e.semanticMarkers != nil {
			e.semanticMarkers.RemoveOnScreen(e.ScrollbackLen())
		}
		if e.cb.ScreenClear != nil {
			e.cb.ScreenClear()
		}
	case 3: // Erase Saved Lines, the scrollback only
		// The visible screen is deliberately untouched. xterm, tmux, kitty
		// and ghostty all read CSI 3 J as dropping the saved lines and
		// nothing else, and the two are separate requests: `clear` sends
		// ED 2 and ED 3 together, so clearing the screen here looks right
		// under `clear` and destroys the screen for anything that sends
		// ED 3 on its own to drop history.
		//
		// The markers come right without help. Clearing the ring fires the
		// trim callback, which shifts every marker down by the lines that
		// went and drops the ones that fell off the front, leaving the
		// on-screen ones where the screen still has them.
		e.scr.ClearScrollback()
	default:
		return false
	}
	return true
}

// eraseLine is EL, and with selective set DECSEL. fill blanks an area.
func (e *Emulator) eraseLine(params ansi.Params, selective bool, fill func(*uv.Cell, uv.Rectangle)) bool {
	n, _, _ := params.Param(0, 0)
	// NOTE: Erase Line (EL) erases all character attributes but not cell
	// bg color.
	x, y := e.scr.CursorPosition()
	w := e.scr.Width()

	switch n {
	case 0: // Erase from cursor to end of line
		if !selective {
			e.eraseCharacter(w - x)
			break
		}
		// eraseCharacter's bookkeeping, around a fill that skips protected
		// cells.
		fill(e.scr.blankCell(), uv.Rect(x, y, w-x, 1))
		e.scr.buf.setSoftWrapped(y, false)
		e.atPhantom = false
	case 1: // Erase from start of line to cursor
		fill(e.scr.blankCell(), uv.Rect(0, y, x+1, 1))
	case 2: // Erase entire line
		fill(e.scr.blankCell(), uv.Rect(0, y, w, 1))
	default:
		return false
	}
	return true
}
