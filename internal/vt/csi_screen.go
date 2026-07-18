package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
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
	e.atPhantom = false
	// ECH does not move the cursor.
}
