package vt

import (
	"image/color"

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

	// The pattern covers the screen, so the margins that were confining output
	// to part of it go with it. xterm calls resetmargins here, and vttest runs
	// its margin checks straight after the alignment check on the assumption
	// that it did.
	e.scr.scroll = e.scr.buf.Bounds()

	e.atPhantom = false
	e.scr.setCursor(0, 0, false)
}

// saveCharsets records the character set selection alongside the cursor, which
// is where DEC puts it: DECSC saves it and DECRC brings it back.
func (e *Emulator) saveCharsets() {
	x := &e.scr.savedExtra
	x.charsetIDs = e.charsetIDs
	x.charsets = e.charsets
	x.gl, x.gr = e.gl, e.gr
}

// restoreCharsets is the DECRC half of saveCharsets.
func (e *Emulator) restoreCharsets() {
	x := &e.scr.savedExtra
	e.charsetIDs = x.charsetIDs
	for i, id := range e.charsetIDs {
		if id == 0 {
			e.charsetIDs[i] = 'B'
		}
	}
	e.charsets = x.charsets
	e.gl, e.gr = x.gl, x.gr
	e.gsingle = 0
}

// softReset performs a soft terminal reset as in [ansi.DECSTR].
//
// The difference from RIS is what it leaves alone. A soft reset is what a
// program runs to put the terminal into a state it can reason about without
// destroying the session around it, so the screen, the scrollback, the tab
// stops, the title and the window size all survive. What goes is the state that
// changes the meaning of everything printed afterwards.
//
// The list is the one DEC documents for the VT510, restricted to the state this
// emulator actually keeps: the cursor enabled, insert/replace back to replace,
// origin mode absolute, the keypad numeric, normal arrow keys, the scroll
// region back to the full page, the left and right margins with it, G0 to G3
// and GL and GR back to their defaults, SGR back to normal, and the saved
// cursor to home, and DECSCA protection off for what is printed next. The
// modes DEC also lists but this emulator has no notion of, KAM, DECNRCM,
// DECSASD, DECKPM, DECRLM and DECPCTERM, are left out:
// a guest cannot set them (see handleMode), so there is nothing to clear.
//
// Two things it deliberately does not do, both of which programs depend on: it
// does not move the cursor, and it does not clear the screen.
//
// DECAWM is deliberately left alone, which is a deviation from the spec rather
// than an omission. DEC has a soft reset turn autowrap off; xterm and iTerm2
// both decline, and esctest marks its own test for it as an intentional
// deviation. Following the spec would stop the line wrapping of every program
// that soft-resets and then prints, which is most of them.
func (e *Emulator) softReset() {
	// Several of the modes below home the cursor when they are set or reset on
	// their own, DECOM among them, and a soft reset must not move it. Putting
	// it back afterwards keeps that true however the list grows.
	curX, curY := e.scr.CursorPosition()

	e.scr.setCursorHidden(false)
	e.setMode(ansi.ModeTextCursorEnable, ansi.ModeSet)
	e.setMode(ansi.ModeInsertReplace, ansi.ModeReset)
	e.setMode(ansi.ModeOrigin, ansi.ModeReset)
	e.setMode(ansi.ModeNumericKeypad, ansi.ModeReset)
	e.setMode(ansi.ModeCursorKeys, ansi.ModeReset)
	e.setMode(ansi.ModeLeftRightMargin, ansi.ModeReset)

	// The region, and with it both pairs of margins.
	e.scr.scroll = e.scr.buf.Bounds()
	e.atPhantom = false

	e.charsets = [4]CharSet{}
	e.charsetIDs = defaultCharsetIDs
	e.gl, e.gr = 0, 1
	e.gsingle = 0

	// SGR back to normal. The hyperlink goes with it: a program that soft-resets
	// in the middle of an open OSC 8 would otherwise have every character it
	// printed afterwards belong to somebody's URL.
	e.scr.cur.Pen = uv.Style{}
	e.scr.cur.Link = uv.Link{}
	// DEC lists DECSCA among what a soft reset turns off.
	e.scr.cur.Protected = false

	// The saved cursor goes back to the origin with a default pen, so a DECRC
	// after a soft reset lands somewhere defined.
	e.scr.saved = Cursor{}
	e.saveCharsets()

	e.scr.setCursor(curX, curY, false)
}

// fullReset performs a full terminal reset as in [ansi.RIS].
func (e *Emulator) fullReset() {
	e.scrs[0].Reset()
	e.scrs[1].Reset()
	e.resetTabStops()

	// XXX: Do we reset all modes here? Investigate.
	e.resetModes()

	// RIS puts the palette back, as xterm's does. Only the guest's own OSC 4
	// layer goes: the user's theme is not the guest's to reset.
	e.colors = [256]color.Color{}
	e.refreshPaletteClaims()

	// Titles a guest saved with XTWINOPS 22 are not restored past a reset.
	e.titleStack = nil

	e.gl, e.gr = 0, 1
	e.gsingle = 0
	e.charsets = [4]CharSet{}
	e.charsetIDs = defaultCharsetIDs
	e.atPhantom = false
	e.parkedX = -1
	e.grapheme = e.grapheme[:0]
	e.openGrapheme = openGrapheme{}
	e.lastCluster, e.lastClusterWidth = "", 0
	e.lastState = parser.GroundState

	// xterm's RIS puts the key modifier resources back to their initial
	// values, and modifyOtherKeys starts off.
	e.modifyOtherKeys.Store(0)

	// Reset kitty keyboard protocol state
	if e.kittyKbd != nil {
		e.kittyKbd.Reset()
		e.kittyKbd.SelectScreen(e.scr == &e.scrs[1])
		e.updateKittyKeyboardCache()
	}
}
