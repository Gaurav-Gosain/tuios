package vt

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

func (e *Emulator) handleEsc(cmd ansi.Cmd) {
	e.flushGrapheme()
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

func (e *Emulator) screenAlignmentPattern() {
	cell := uv.Cell{Content: "E", Width: 1}
	e.scr.Fill(&cell)

	e.scr.scroll = e.scr.buf.Bounds()

	e.atPhantom = false
	e.scr.setCursor(0, 0, false)
}

func (e *Emulator) saveCharsets() {
	x := &e.scr.savedExtra
	x.charsetIDs = e.charsetIDs
	x.charsets = e.charsets
	x.gl, x.gr = e.gl, e.gr
}

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

func (e *Emulator) softReset() {
	curX, curY := e.scr.CursorPosition()

	e.scr.setCursorHidden(false)
	e.setMode(ansi.ModeTextCursorEnable, ansi.ModeSet)
	e.setMode(ansi.ModeInsertReplace, ansi.ModeReset)
	e.setMode(ansi.ModeOrigin, ansi.ModeReset)
	e.setMode(ansi.ModeNumericKeypad, ansi.ModeReset)
	e.setMode(ansi.ModeCursorKeys, ansi.ModeReset)
	e.setMode(ansi.ModeLeftRightMargin, ansi.ModeReset)

	e.scr.scroll = e.scr.buf.Bounds()
	e.atPhantom = false

	e.charsets = [4]CharSet{}
	e.charsetIDs = defaultCharsetIDs
	e.gl, e.gr = 0, 1
	e.gsingle = 0

	e.scr.cur.Pen = uv.Style{}
	e.scr.cur.Link = uv.Link{}
	e.scr.cur.Protected = false

	e.scr.saved = Cursor{}
	e.saveCharsets()

	e.scr.setCursor(curX, curY, false)
}

func (e *Emulator) fullReset() {
	e.scrs[0].Reset()
	e.scrs[1].Reset()
	e.resetTabStops()

	e.resetModes()

	e.colors = [256]color.Color{}
	e.refreshPaletteClaims()

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

	e.modifyOtherKeys.Store(0)

	if e.kittyKbd != nil {
		e.kittyKbd.Reset()
		e.kittyKbd.SelectScreen(e.scr == &e.scrs[1])
		e.updateKittyKeyboardCache()
	}
}
