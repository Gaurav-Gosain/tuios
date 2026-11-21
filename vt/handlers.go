package vt

import (
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/progstatus"
)

// DcsHandler is a function that handles a DCS escape sequence.
type DcsHandler func(params ansi.Params, data []byte) bool

// CsiHandler is a function that handles a CSI escape sequence.
type CsiHandler func(params ansi.Params) bool

// OscHandler is a function that handles an OSC escape sequence.
type OscHandler func(data []byte) bool

// ApcHandler is a function that handles an APC escape sequence.
type ApcHandler func(data []byte) bool

// SosHandler is a function that handles an SOS escape sequence.
type SosHandler func(data []byte) bool

// PmHandler is a function that handles a PM escape sequence.
type PmHandler func(data []byte) bool

// EscHandler is a function that handles an ESC escape sequence.
type EscHandler func() bool

// CcHandler is a function that handles a control character.
type CcHandler func() bool

type handlers struct {
	ccHandlers  [256][]CcHandler
	dcsHandlers map[int][]DcsHandler
	csiHandlers map[int][]CsiHandler
	oscHandlers map[int][]OscHandler
	escHandler  map[int][]EscHandler
	apcHandlers []ApcHandler
	sosHandlers []SosHandler
	pmHandlers  []PmHandler
}

//go:noinline
func (h *handlers) RegisterDcsHandler(cmd int, handler DcsHandler) {
	h.dcsHandlers[cmd] = append(h.dcsHandlers[cmd], handler)
}

//go:noinline
func (h *handlers) RegisterCsiHandler(cmd int, handler CsiHandler) {
	h.csiHandlers[cmd] = append(h.csiHandlers[cmd], handler)
}

//go:noinline
func (h *handlers) RegisterOscHandler(cmd int, handler OscHandler) {
	h.oscHandlers[cmd] = append(h.oscHandlers[cmd], handler)
}

func (h *handlers) RegisterApcHandler(handler ApcHandler) {
	h.apcHandlers = append(h.apcHandlers, handler)
}

func (h *handlers) RegisterSosHandler(handler SosHandler) {
	h.sosHandlers = append(h.sosHandlers, handler)
}

func (h *handlers) RegisterPmHandler(handler PmHandler) {
	h.pmHandlers = append(h.pmHandlers, handler)
}

//go:noinline
func (h *handlers) RegisterEscHandler(cmd int, handler EscHandler) {
	h.escHandler[cmd] = append(h.escHandler[cmd], handler)
}

func (h *handlers) registerCcHandler(r byte, handler CcHandler) {
	h.ccHandlers[r] = append(h.ccHandlers[r], handler)
}

func (h *handlers) handleCc(r byte) bool {
	hs := h.ccHandlers[r]
	for _, h := range slices.Backward(hs) {
		if h() {
			return true
		}
	}
	return false
}

func (h *handlers) handleDcs(cmd ansi.Cmd, params ansi.Params, data []byte) bool {
	if handlers, ok := h.dcsHandlers[int(cmd)]; ok {
		for _, handler := range slices.Backward(handlers) {
			if handler(params, data) {
				return true
			}
		}
	}
	return false
}

func (h *handlers) handleCsi(cmd ansi.Cmd, params ansi.Params) bool {
	if handlers, ok := h.csiHandlers[int(cmd)]; ok {
		for _, handler := range slices.Backward(handlers) {
			if handler(params) {
				return true
			}
		}
	}
	return false
}

func (h *handlers) handleOsc(cmd int, data []byte) bool {
	if handlers, ok := h.oscHandlers[cmd]; ok {
		for _, handler := range slices.Backward(handlers) {
			if handler(data) {
				return true
			}
		}
	}
	return false
}

func (h *handlers) handleApc(data []byte) bool {
	for _, v := range slices.Backward(h.apcHandlers) {
		if v(data) {
			return true
		}
	}
	return false
}

func (h *handlers) handleSos(data []byte) bool {
	for _, v := range slices.Backward(h.sosHandlers) {
		if v(data) {
			return true
		}
	}
	return false
}

func (h *handlers) handlePm(data []byte) bool {
	for _, v := range slices.Backward(h.pmHandlers) {
		if v(data) {
			return true
		}
	}
	return false
}

func (h *handlers) handleEsc(cmd int) bool {
	if handlers, ok := h.escHandler[cmd]; ok {
		for _, handler := range slices.Backward(handlers) {
			if handler() {
				return true
			}
		}
	}
	return false
}

func (e *Emulator) registerDefaultHandlers() {
	e.registerDefaultCcHandlers()
	e.registerDefaultCsiHandlers()
	e.registerDefaultEscHandlers()
	e.registerDefaultOscHandlers()
	e.registerReportHandlers()
}

func (e *Emulator) registerDefaultCcHandlers() {
	for i := byte(ansi.NUL); i <= ansi.US; i++ {
		switch i {
		case ansi.NUL:
			e.registerCcHandler(i, func() bool {
				return true
			})
		case ansi.BEL:
			e.registerCcHandler(i, func() bool {
				if e.cb.Bell != nil {
					e.cb.Bell()
				}
				return true
			})
		case ansi.BS:
			e.registerCcHandler(i, func() bool {
				e.backspace()
				return true
			})
		case ansi.HT:
			e.registerCcHandler(i, func() bool {
				e.nextTab(1)
				return true
			})
		case ansi.LF, ansi.VT, ansi.FF:
			e.registerCcHandler(i, func() bool {
				e.linefeed()
				return true
			})
		case ansi.CR:
			e.registerCcHandler(i, func() bool {
				e.carriageReturn()
				return true
			})
		case ansi.SO:
			e.registerCcHandler(i, func() bool {
				e.gl = 1
				return true
			})
		case ansi.SI:
			e.registerCcHandler(i, func() bool {
				e.gl = 0
				return true
			})
		}
	}

	for i := byte(ansi.PAD); i <= byte(ansi.APC); i++ {
		switch i {
		case ansi.HTS:
			e.registerCcHandler(i, func() bool {
				e.horizontalTabSet()
				return true
			})
		case ansi.RI:
			e.registerCcHandler(i, func() bool {
				e.reverseIndex()
				return true
			})
		case ansi.IND:
			e.registerCcHandler(i, func() bool {
				e.index()
				return true
			})
		case ansi.SS2:
			e.registerCcHandler(i, func() bool {
				e.gsingle = 2
				return true
			})
		case ansi.SS3:
			e.registerCcHandler(i, func() bool {
				e.gsingle = 3
				return true
			})
		}
	}
}

func (e *Emulator) registerDefaultOscHandlers() {
	for _, cmd := range []int{
		0,
		1,
		2,
	} {
		e.RegisterOscHandler(cmd, func(data []byte) bool {
			e.handleTitle(cmd, data)
			return true
		})
	}

	e.RegisterOscHandler(7, func(data []byte) bool {
		e.handleWorkingDirectory(7, data)
		return true
	})

	e.RegisterOscHandler(8, func(data []byte) bool {
		e.handleHyperlink(8, data)
		return true
	})

	for _, cmd := range []int{
		10,
		11,
		12,
		110,
		111,
		112,
	} {
		e.RegisterOscHandler(cmd, func(data []byte) bool {
			e.handleDefaultColor(cmd, data)
			return true
		})
	}

	e.RegisterOscHandler(4, func(data []byte) bool {
		e.handlePaletteColor(data)
		return true
	})

	e.RegisterOscHandler(104, func(data []byte) bool {
		e.handleResetPaletteColor(data)
		return true
	})

	e.RegisterOscHandler(52, func(data []byte) bool {
		e.handleClipboard(data)
		return true
	})

	e.RegisterOscHandler(66, func(data []byte) bool {
		e.handleTextSizing(data)
		return true
	})

	e.RegisterOscHandler(133, func(data []byte) bool {
		e.handleSemanticZone(data)
		return true
	})

	e.RegisterOscHandler(9, func(data []byte) bool {
		return e.handleNotify9(data)
	})

	e.RegisterOscHandler(777, func(data []byte) bool {
		return e.handleNotify777(data)
	})

	e.RegisterOscHandler(progstatus.Command, func(data []byte) bool {
		e.handleProgramStatus(data)
		return true
	})

	e.RegisterOscHandler(99, func(data []byte) bool {
		return e.handleNotify99(data)
	})
}

func (e *Emulator) registerDefaultEscHandlers() {
	e.RegisterEscHandler('=', func() bool {
		e.setMode(ansi.ModeNumericKeypad, ansi.ModeSet)
		return true
	})

	e.RegisterEscHandler('>', func() bool {
		e.setMode(ansi.ModeNumericKeypad, ansi.ModeReset)
		return true
	})

	e.RegisterEscHandler('7', func() bool {
		e.saveCursor()
		return true
	})

	e.RegisterEscHandler('8', func() bool {
		e.restoreCursor()
		return true
	})

	for _, cmd := range []int{
		ansi.Command(0, '(', 'A'),
		ansi.Command(0, ')', 'A'),
		ansi.Command(0, '*', 'A'),
		ansi.Command(0, '+', 'A'),
		ansi.Command(0, '(', 'B'),
		ansi.Command(0, ')', 'B'),
		ansi.Command(0, '*', 'B'),
		ansi.Command(0, '+', 'B'),
		ansi.Command(0, '(', '0'),
		ansi.Command(0, ')', '0'),
		ansi.Command(0, '*', '0'),
		ansi.Command(0, '+', '0'),
	} {
		e.RegisterEscHandler(cmd, func() bool {
			c := ansi.Cmd(cmd)
			set := c.Intermediate() - '('
			switch c.Final() {
			case 'A':
				e.charsets[set] = charsetUK
			case 'B':
				e.charsets[set] = nil
			case '0':
				e.charsets[set] = charsetSpecialDrawing
			default:
				return false
			}
			e.charsetIDs[set] = byte(c.Final())
			return true
		})
	}

	e.RegisterEscHandler('D', func() bool {
		e.index()
		return true
	})

	e.RegisterEscHandler('E', func() bool {
		e.index()
		e.carriageReturn()
		return true
	})

	e.RegisterEscHandler('H', func() bool {
		e.horizontalTabSet()
		return true
	})

	e.RegisterEscHandler('M', func() bool {
		e.reverseIndex()
		return true
	})

	e.RegisterEscHandler(ansi.Command(0, '#', '8'), func() bool {
		e.screenAlignmentPattern()
		return true
	})

	e.RegisterEscHandler('\\', func() bool {
		return true
	})

	e.RegisterEscHandler('c', func() bool {
		e.fullReset()
		if e.cb.ProgramStatus != nil {
			e.cb.ProgramStatus(progstatus.Event{Reset: true})
		}
		return true
	})

	e.RegisterEscHandler('N', func() bool {
		e.gsingle = 2
		return true
	})

	e.RegisterEscHandler('O', func() bool {
		e.gsingle = 3
		return true
	})

	e.RegisterEscHandler('n', func() bool {
		e.gl = 2
		return true
	})

	e.RegisterEscHandler('o', func() bool {
		e.gl = 3
		return true
	})

	e.RegisterEscHandler('|', func() bool {
		e.gr = 3
		return true
	})

	e.RegisterEscHandler('}', func() bool {
		e.gr = 2
		return true
	})

	e.RegisterEscHandler('~', func() bool {
		e.gr = 1
		return true
	})
}

func csiCount(params ansi.Params, i int) int {
	n, _, _ := params.Param(i, 1)
	if n < 1 {
		return 1
	}
	return n
}

func (e *Emulator) registerDefaultCsiHandlers() {
	e.RegisterCsiHandler('@', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.scr.InsertCell(n)
		return true
	})

	e.RegisterCsiHandler('A', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(0, -n)
		return true
	})

	e.RegisterCsiHandler('B', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(0, n)
		return true
	})

	e.RegisterCsiHandler('C', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(n, 0)
		return true
	})

	e.RegisterCsiHandler('D', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(-n, 0)
		return true
	})

	e.RegisterCsiHandler('E', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(0, n)
		e.carriageReturn()
		return true
	})

	e.RegisterCsiHandler('F', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(0, -n)
		e.carriageReturn()
		return true
	})

	e.RegisterCsiHandler('G', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		_, y := e.scr.CursorPosition()
		e.setCursor(n-1, y)
		return true
	})

	e.RegisterCsiHandler('H', func(params ansi.Params) bool {
		width, height := e.Width(), e.Height()
		row, _, _ := params.Param(0, 1)
		col, _, _ := params.Param(1, 1)
		if row < 1 {
			row = 1
		}
		if col < 1 {
			col = 1
		}
		y := min(height-1, row-1)
		x := min(width-1, col-1)
		e.setCursorPosition(x, y)
		return true
	})

	e.RegisterCsiHandler('I', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.nextTab(n)
		return true
	})

	eraseDisplay := func(selective bool) func(ansi.Params) bool {
		fill := e.eraseFill(selective)
		return func(params ansi.Params) bool {
			return e.eraseDisplay(params, selective, fill)
		}
	}
	e.RegisterCsiHandler('J', eraseDisplay(false))
	e.RegisterCsiHandler(ansi.Command('?', 0, 'J'), eraseDisplay(true))

	eraseLine := func(selective bool) func(ansi.Params) bool {
		fill := e.eraseFill(selective)
		return func(params ansi.Params) bool {
			return e.eraseLine(params, selective, fill)
		}
	}
	e.RegisterCsiHandler('K', eraseLine(false))
	e.RegisterCsiHandler(ansi.Command('?', 0, 'K'), eraseLine(true))

	e.RegisterCsiHandler(ansi.Command(0, '"', 'q'), func(params ansi.Params) bool {
		n, _, _ := params.Param(0, 0)
		switch n {
		case 1:
			e.scr.cur.Protected = true
		case 0, 2:
			e.scr.cur.Protected = false
		}
		return true
	})

	e.RegisterCsiHandler('L', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		if e.scr.InsertLine(n) {
			scroll := e.scr.ScrollRegion()
			_, y := e.scr.CursorPosition()
			e.scr.setCursor(scroll.Min.X, y, false)
		}
		return true
	})

	e.RegisterCsiHandler('M', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		if e.scr.DeleteLine(n) {
			scroll := e.scr.ScrollRegion()
			_, y := e.scr.CursorPosition()
			e.scr.setCursor(scroll.Min.X, y, false)
		}
		return true
	})

	e.RegisterCsiHandler('P', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.scr.DeleteCell(n)
		return true
	})

	e.RegisterCsiHandler('S', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.scr.ScrollUp(n)
		return true
	})

	e.RegisterCsiHandler('T', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.scr.ScrollDown(n)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'W'), func(params ansi.Params) bool {
		if len(params) == 1 && params[0] == 5 {
			e.resetTabStops()
			return true
		}
		return false
	})

	e.RegisterCsiHandler('X', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.eraseCharacter(n)
		return true
	})

	e.RegisterCsiHandler('Z', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.prevTab(n)
		return true
	})

	e.RegisterCsiHandler('`', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		width := e.Width()
		_, y := e.scr.CursorPosition()
		e.setCursorPosition(min(width-1, n-1), y)
		return true
	})

	e.RegisterCsiHandler('a', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		width := e.Width()
		x, y := e.scr.CursorPosition()
		e.setCursorPosition(min(width-1, x+n), y)
		return true
	})

	e.RegisterCsiHandler('j', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(-n, 0)
		return true
	})

	e.RegisterCsiHandler('k', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.moveCursor(0, -n)
		return true
	})

	e.RegisterCsiHandler('b', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		e.repeatPreviousCharacter(n)
		return true
	})

	e.RegisterCsiHandler('c', func(params ansi.Params) bool {
		n, _, _ := params.Param(0, 0)
		if n != 0 {
			return false
		}

		_, _ = io.WriteString(e.pipe, ansi.PrimaryDeviceAttributes(DeviceAttributes(e.sixelOn())...))
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'S'), func(params ansi.Params) bool {
		item, _, _ := params.Param(0, 0)
		action, _, _ := params.Param(1, 0)
		cw, ch := e.CellSize()
		_, _ = io.WriteString(e.pipe, sixelGraphicsReply(item, action, e.sixelOn(), e.Width()*cw, e.Height()*ch))
		return true
	})

	e.RegisterCsiHandler(ansi.Command('>', 0, 'c'), func(params ansi.Params) bool {
		n, _, _ := params.Param(0, 0)
		if n != 0 {
			return false
		}

		_, _ = io.WriteString(e.pipe, ansi.SecondaryDeviceAttributes(
			1,
			10,
			0,
		))
		return true
	})

	e.RegisterCsiHandler('d', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		height := e.Height()
		x, _ := e.scr.CursorPosition()
		e.setCursorPosition(x, min(height-1, n-1))
		return true
	})

	e.RegisterCsiHandler('e', func(params ansi.Params) bool {
		n := csiCount(params, 0)
		height := e.Height()
		x, y := e.scr.CursorPosition()
		e.setCursorPosition(x, min(height-1, y+n))
		return true
	})

	e.RegisterCsiHandler('f', func(params ansi.Params) bool {
		width, height := e.Width(), e.Height()
		row, _, _ := params.Param(0, 1)
		col, _, _ := params.Param(1, 1)
		y := min(height-1, row-1)
		x := min(width-1, col-1)
		e.setCursor(x, y)
		return true
	})

	e.RegisterCsiHandler('g', func(params ansi.Params) bool {
		value, _, _ := params.Param(0, 0)
		switch value {
		case 0:
			x, _ := e.scr.CursorPosition()
			e.tabstops.Reset(x)
		case 3:
			e.tabstops.Clear()
		default:
			return false
		}

		return true
	})

	e.RegisterCsiHandler('h', func(params ansi.Params) bool {
		e.handleMode(params, true, true)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'h'), func(params ansi.Params) bool {
		e.handleMode(params, true, false)
		return true
	})

	e.RegisterCsiHandler('l', func(params ansi.Params) bool {
		e.handleMode(params, false, true)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'l'), func(params ansi.Params) bool {
		e.handleMode(params, false, false)
		return true
	})

	e.RegisterCsiHandler('m', func(params ansi.Params) bool {
		e.handleSgr(params)
		return true
	})

	e.RegisterCsiHandler('n', func(params ansi.Params) bool {
		n, _, ok := params.Param(0, 1)
		if !ok || n == 0 {
			return false
		}

		switch n {
		case 5:
			_, _ = io.WriteString(e.pipe, ansi.DeviceStatusReport(ansi.ANSIStatusReport(0)))
		case 6:
			line, col := e.reportedCursorPosition()
			_, _ = io.WriteString(e.pipe, ansi.CursorPositionReport(line, col))
		default:
			return false
		}

		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'n'), func(params ansi.Params) bool {
		n, _, ok := params.Param(0, 1)
		if !ok || n == 0 {
			return false
		}

		switch n {
		case 5:
			_, _ = io.WriteString(e.pipe, ansi.DeviceStatusReport(ansi.DECStatusReport(0)))
		case 6:
			line, col := e.reportedCursorPosition()
			_, _ = io.WriteString(e.pipe, ansi.ExtendedCursorPositionReport(line, col, 1))
		case 996:
			_, _ = io.WriteString(e.pipe, colorSchemeNotice(e.reportedBackground()))
		default:
			return false
		}

		return true
	})

	e.RegisterCsiHandler('u', func(ansi.Params) bool {
		e.restoreCursor()
		return true
	})

	e.RegisterCsiHandler(ansi.Command(0, '!', 'p'), func(ansi.Params) bool {
		e.softReset()
		return true
	})

	e.RegisterDcsHandler(ansi.Command(0, '$', 'q'), func(_ ansi.Params, data []byte) bool {
		e.reportSetting(string(data))
		return true
	})

	e.RegisterCsiHandler('t', func(params ansi.Params) bool {
		n, _, ok := params.Param(0, 0)

		debugLog := func(msg string) {
			if l := debugLogger(); l != nil {
				l.Printf("VT-XTWINOPS: %s", msg)
			}
			if e.logger != nil {
				e.logger.Printf("XTWINOPS: %s", msg)
			}
		}

		if !ok || n == 0 {
			debugLog(fmt.Sprintf("invalid params ok=%v n=%d", ok, n))
			return false
		}

		cellWidth, cellHeight := e.CellSize()
		debugLog(fmt.Sprintf("handling CSI %d t (cellSize=%dx%d, termSize=%dx%d)",
			n, cellWidth, cellHeight, e.Width(), e.Height()))

		switch n {
		case 14:
			pixelHeight := e.Height() * cellHeight
			pixelWidth := e.Width() * cellWidth
			response := ansi.WindowOp(4, pixelHeight, pixelWidth)
			debugLog(fmt.Sprintf("responding to CSI 14 t with: %q (pixels: %dx%d)", response, pixelWidth, pixelHeight))
			_, _ = io.WriteString(e.pipe, response)
		case 16:
			response := ansi.WindowOp(6, cellHeight, cellWidth)
			debugLog(fmt.Sprintf("responding to CSI 16 t with: %q", response))
			_, _ = io.WriteString(e.pipe, response)
		case 18:
			response := ansi.WindowOp(8, e.Height(), e.Width())
			debugLog(fmt.Sprintf("responding to CSI 18 t with: %q", response))
			_, _ = io.WriteString(e.pipe, response)
		case 22:
			which, _, _ := params.Param(1, 0)
			return e.pushTitle(which)
		case 23:
			which, _, _ := params.Param(1, 0)
			return e.popTitle(which)
		default:
			debugLog(fmt.Sprintf("unsupported command CSI %d t", n))
			return false
		}

		return true
	})

	e.RegisterCsiHandler(ansi.Command(0, '$', 'p'), func(params ansi.Params) bool {
		e.handleRequestMode(params, true)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', '$', 'p'), func(params ansi.Params) bool {
		e.handleRequestMode(params, false)
		return true
	})

	e.RegisterCsiHandler(ansi.Command(0, ' ', 'q'), func(params ansi.Params) bool {
		n := 1
		if param, _, ok := params.Param(0, 0); ok && param > n {
			n = param
		}
		blink := n == 0 || n%2 == 1
		style := n / 2
		if !blink {
			style--
		}
		e.cursorStyle, e.cursorSteady = CursorStyle(style), !blink
		return true
	})

	e.RegisterCsiHandler('r', func(params ansi.Params) bool {
		height := e.Height()

		top, _, _ := params.Param(0, 1)
		if top < 1 {
			top = 1
		}

		bottom, _, _ := params.Param(1, height)
		if bottom < 1 {
			bottom = height
		}

		if bottom > height {
			bottom = height
		}
		if top > height {
			top = height
		}

		if top >= bottom {
			return false
		}

		e.scr.setVerticalMargins(top-1, bottom)

		e.setCursorPosition(0, 0)
		return true
	})

	e.RegisterCsiHandler('s', func(params ansi.Params) bool {
		if e.isModeSet(ansi.ModeLeftRightMargin) {
			left, _, _ := params.Param(0, 1)
			if left < 1 {
				left = 1
			}

			width := e.Width()
			right, _, _ := params.Param(1, width)
			if right < 1 {
				right = width
			}

			if right > width {
				right = width
			}
			if left > width {
				left = width
			}

			if left >= right {
				return false
			}

			e.scr.setHorizontalMargins(left-1, right)

			e.setCursorPosition(0, 0)
		} else {
			e.saveCursor()
		}

		return true
	})
}
