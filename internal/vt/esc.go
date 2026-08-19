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
