package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// handleEsc handles an escape sequence.
func (e *Emulator) handleEsc(cmd ansi.Cmd) {
	e.flushGrapheme() // Flush any pending grapheme before handling ESC sequences.
	if !e.handlers.handleEsc(int(cmd)) {
		var str string
		if inter := cmd.Intermediate(); inter != 0 {
			str += string(inter) + " "
		}
		if final := cmd.Final(); final != 0 {
			str += string(final)
		}
		e.logf("unhandled sequence: ESC %q", str)
	}
}

// screenAlignmentPattern fills the screen with 'E' as in [ansi.DECALN]. It is
// how vttest and esctest check that a terminal's idea of the grid matches
// theirs, so it runs before most of what those suites go on to test.
//
// The pattern covers the whole screen, not the scroll region, and it homes the
// cursor. It uses the default pen rather than the cursor's, which is what makes
// it usable as an alignment check at all.
func (e *Emulator) screenAlignmentPattern() {
	cell := uv.Cell{Content: "E", Width: 1}
	e.scr.Fill(&cell)
	e.atPhantom = false
	e.scr.setCursor(0, 0, false)
}

// saveCharsets records the character set selection alongside the cursor, which
// is where DEC puts it: DECSC saves it and DECRC brings it back.
func (e *Emulator) saveCharsets() {
	e.savedCharsetIDs = e.charsetIDs
	e.savedCharsets = e.charsets
	e.savedGL, e.savedGR = e.gl, e.gr
}

// restoreCharsets is the DECRC half of saveCharsets.
func (e *Emulator) restoreCharsets() {
	e.charsetIDs = e.savedCharsetIDs
	e.charsets = e.savedCharsets
	e.gl, e.gr = e.savedGL, e.savedGR
	e.gsingle = 0
}

// softReset performs a soft terminal reset as in [ansi.DECSTR].
//
// The difference from RIS is what it leaves alone. A soft reset is what a
// program runs to put the terminal back into a state it can reason about
// without destroying the session around it, so the screen, the scrollback, the
// tab stops, the title and the window size all survive. What goes is the state
// that changes the meaning of everything printed afterwards: the scroll region,
// origin mode, the character sets, the saved cursor, and a hidden cursor.
//
// DECAWM is deliberately not touched. The VT220 manual has a soft reset turn
// autowrap off, but no terminal in use does that, and following the manual here
// would break the line wrapping of every program that soft-resets and then
// prints, which is most of them.
func (e *Emulator) softReset() {
	e.scr.setCursorHidden(false)
	e.setMode(ansi.ModeTextCursorEnable, ansi.ModeSet)
	e.setMode(ansi.ModeOrigin, ansi.ModeReset)
	e.setMode(ansi.ModeLeftRightMargin, ansi.ModeReset)

	e.scr.scroll = e.scr.buf.Bounds()
	e.atPhantom = false

	e.charsets = [4]CharSet{}
	e.charsetIDs = defaultCharsetIDs
	e.gl, e.gr = 0, 1
	e.gsingle = 0

	// The saved cursor goes back to the origin with a default pen, so a DECRC
	// after a soft reset lands somewhere defined.
	e.scr.saved = Cursor{}
	e.saveCharsets()
}

// fullReset performs a full terminal reset as in [ansi.RIS].
func (e *Emulator) fullReset() {
	e.scrs[0].Reset()
	e.scrs[1].Reset()
	e.resetTabStops()

	// XXX: Do we reset all modes here? Investigate.
	e.resetModes()

	e.gl, e.gr = 0, 1
	e.gsingle = 0
	e.charsets = [4]CharSet{}
	e.charsetIDs = defaultCharsetIDs
	e.atPhantom = false
	e.grapheme = e.grapheme[:0]
	e.openGrapheme = openGrapheme{}
	e.lastChar = 0
	e.lastState = parser.GroundState

	// Reset kitty keyboard protocol state
	if e.kittyKbd != nil {
		e.kittyKbd.Reset()
		e.updateKittyKeyboardCache()
	}
}
