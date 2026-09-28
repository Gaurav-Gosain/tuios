package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// handleControl handles a control character.
func (e *Emulator) handleControl(r byte) {
	e.flushGrapheme() // Flush any pending grapheme before handling control codes.
	if !e.handleCc(r) {
		e.logf("unhandled sequence: ControlCode %q", r)
	}
}

// linefeed is the same as [index], except that it respects [ansi.LNM] mode.
func (e *Emulator) linefeed() {
	e.index()
	if e.isModeSet(ansi.ModeLineFeedNewLine) {
		e.carriageReturn()
	}
}

// index moves the cursor down one line, scrolling up if necessary. This
// always resets the phantom state i.e. pending wrap state.
func (e *Emulator) index() {
	x, y := e.scr.CursorPosition()
	scroll := e.scr.ScrollRegion()
	if y == scroll.Max.Y-1 && x >= scroll.Min.X && x < scroll.Max.X {
		e.scr.ScrollUp(1)
	} else if y < scroll.Max.Y-1 || !uv.Pos(x, y).In(scroll) {
		e.scr.moveCursor(0, 1)
	}
	e.atPhantom = false
}

// noteSoftWrap records that the cursor's row carries on to the next row,
// because autowrap is about to move the cursor there. A wrap inside left and
// right margins is not a whole row carrying on, so it is not recorded.
func (e *Emulator) noteSoftWrap(left, right int) {
	if left != 0 || right != e.scr.Width() {
		return
	}
	_, y := e.scr.CursorPosition()
	e.scr.buf.setSoftWrapped(y, true)
}

// RowSoftWrapped reports whether row y of the active screen carries on to
// row y+1 because autowrap moved the text there, rather than because the
// program wrote a newline. known is always true for this backend.
func (e *Emulator) RowSoftWrapped(y int) (wrapped, known bool) {
	return e.scr.buf.SoftWrapped(y), true
}

// ScrollbackSoftWrapped reports whether scrollback line index (oldest first)
// carries on to the next line by autowrap. The newest line carries on to the
// screen's first row. known is always true for this backend.
func (e *Emulator) ScrollbackSoftWrapped(index int) (wrapped, known bool) {
	sb := e.scrs[0].Scrollback()
	if sb == nil {
		return false, true
	}
	return sb.LineWrapped(index), true
}

// horizontalTabSet sets a horizontal tab stop at the current cursor position.
func (e *Emulator) horizontalTabSet() {
	x, _ := e.scr.CursorPosition()
	e.tabstops.Set(x)
}

// reverseIndex moves the cursor up one line, or scrolling down. This does not
// reset the phantom state i.e. pending wrap state.
func (e *Emulator) reverseIndex() {
	x, y := e.scr.CursorPosition()
	scroll := e.scr.ScrollRegion()
	if y == scroll.Min.Y && x >= scroll.Min.X && x < scroll.Max.X {
		e.scr.ScrollDown(1)
	} else {
		e.scr.moveCursor(0, -1)
	}
}

// backspace moves the cursor back one cell, if possible.
func (e *Emulator) backspace() {
	// This acts like [ansi.CUB]
	e.moveCursor(-1, 0)
}
