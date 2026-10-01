package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
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
	// LNM is an ANSI mode, so RestoreModes, which writes DEC modes only,
	// never touches it, and setMode keeps this cache in step.
	if e.cachedLineFeedNewLine.Load() {
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

// MainRowTail returns the cells of main screen row y past the width that a
// reflow held for an open prompt (see freezePrompt), or nil. A reader that
// keeps a row whole, such as a saved history, appends them.
func (e *Emulator) MainRowTail(y int) uv.Line {
	return e.scrs[0].buf.rowTail(y)
}

// RowPadded reports whether row y of the active screen wrapped a column
// early before a wide character. See Terminal.RowPadded.
func (e *Emulator) RowPadded(y int) bool {
	buf := e.scr.buf
	return y >= 0 && y < len(buf.wrap) && buf.wrap[y]&rowPadded != 0
}

// ScrollbackPadded is RowPadded for a history line, oldest first.
func (e *Emulator) ScrollbackPadded(index int) bool {
	sb := e.scrs[0].Scrollback()
	return sb != nil && sb.lineFlags(index)&rowPadded != 0
}

// RestorePads sets the padding flags a snapshot carries. See
// Terminal.RestorePads.
func (e *Emulator) RestorePads(screen, history []bool) {
	buf := e.scr.buf
	for y := range min(buf.Height(), len(screen)) {
		if screen[y] && buf.wrap[y]&rowWrapped != 0 {
			buf.wrap[y] |= rowPadded
		}
	}
	sb := e.scrs[0].Scrollback()
	if sb == nil || len(history) == 0 {
		return
	}
	base := sb.Len() - len(history)
	for i, p := range history {
		if p && base+i >= 0 && sb.lineFlags(base+i)&rowWrapped != 0 {
			sb.wraps[sb.slot(base+i)] |= rowPadded
		}
	}
}

// RestoreSoftWraps sets the soft-wrap flags a snapshot carries. See
// Terminal.RestoreSoftWraps.
func (e *Emulator) RestoreSoftWraps(screen, history []bool) {
	buf := e.scr.buf
	for y := range buf.Height() {
		buf.setSoftWrapped(y, y < len(screen) && screen[y])
	}
	if main := &e.scrs[0]; main != e.scr {
		clear(main.buf.wrap)
	}
	sb := e.scrs[0].Scrollback()
	if sb == nil || sb.Len() == 0 {
		return
	}
	if len(history) == 0 {
		sb.setWrapped(sb.Len()-1, false)
		return
	}
	base := sb.Len() - len(history)
	for i, w := range history {
		sb.setWrapped(base+i, w)
	}
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
